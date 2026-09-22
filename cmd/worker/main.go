package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/example/wp-host-panel/internal/core"
)

const composeTemplate = `services:
  wordpress:
    image: wordpress:php8.3-apache
    container_name: wph-wp-%[1]s
    restart: unless-stopped
    environment:
      WORDPRESS_DB_HOST: db:3306
      WORDPRESS_DB_USER: wordpress
      WORDPRESS_DB_NAME: wordpress
      WORDPRESS_DB_PASSWORD_FILE: /run/secrets/db_password
    secrets: [db_password]
    volumes:
      - wordpress_data:/var/www/html
    networks: [frontend, database]
    depends_on:
      db:
        condition: service_healthy
    mem_limit: 512m
    cpus: 1.0
    pids_limit: 200
    security_opt: [no-new-privileges:true]
  db:
    image: mariadb:11.8
    container_name: wph-db-%[1]s
    restart: unless-stopped
    environment:
      MARIADB_DATABASE: wordpress
      MARIADB_USER: wordpress
      MARIADB_PASSWORD_FILE: /run/secrets/db_password
      MARIADB_ROOT_PASSWORD_FILE: /run/secrets/db_root_password
    secrets: [db_password, db_root_password]
    volumes:
      - database_data:/var/lib/mysql
    networks: [database]
    healthcheck:
      test: [CMD, healthcheck.sh, --connect, --innodb_initialized]
      interval: 10s
      timeout: 5s
      retries: 12
    mem_limit: 512m
    cpus: 1.0
    pids_limit: 200
    security_opt: [no-new-privileges:true]
  cli:
    image: wordpress:cli-php8.3
    profiles: [tools]
    environment:
      WORDPRESS_DB_HOST: db:3306
      WORDPRESS_DB_USER: wordpress
      WORDPRESS_DB_NAME: wordpress
      WORDPRESS_DB_PASSWORD_FILE: /run/secrets/db_password
    secrets: [db_password, admin_password, site_url, site_title, admin_email]
    volumes:
      - wordpress_data:/var/www/html
    networks: [database]
volumes:
  wordpress_data:
  database_data:
networks:
  frontend:
  database:
    internal: true
secrets:
  db_password:
    file: ./secrets/db_password
  db_root_password:
    file: ./secrets/db_root_password
  admin_password:
    file: ./secrets/admin_password
  site_url:
    file: ./secrets/site_url
  site_title:
    file: ./secrets/site_title
  admin_email:
    file: ./secrets/admin_email
`

const wpInstallScript = `if wp core is-installed >/dev/null 2>&1; then wp user update admin --user_pass="$(cat /run/secrets/admin_password)" --user_email="$(cat /run/secrets/admin_email)"; else wp core install --url="$(cat /run/secrets/site_url)" --title="$(cat /run/secrets/site_title)" --admin_user=admin --admin_password="$(cat /run/secrets/admin_password)" --admin_email="$(cat /run/secrets/admin_email)" --skip-email; fi`

type runner interface {
	Run(context.Context, ...string) error
}

type dockerRunner struct{}

func (dockerRunner) Run(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker %s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

type worker struct {
	root   string
	routes string
	token  string
	docker runner
}

func (w *worker) siteDir(id string) string { return filepath.Join(w.root, id) }

func (w *worker) composeArgs(id string, args ...string) []string {
	base := []string{"compose", "-p", "wph-" + id, "-f", filepath.Join(w.siteDir(id), "compose.yaml")}
	return append(base, args...)
}

func writeSecret(dir, name, value string) error {
	if strings.ContainsRune(value, 0) {
		return errors.New("invalid secret")
	}
	// Compose file-backed secrets are mounted into processes with different UIDs.
	// The parent directory is 0700, so host users cannot traverse to these files.
	return os.WriteFile(filepath.Join(dir, name), []byte(value), 0644)
}

func (w *worker) create(ctx context.Context, req core.CreateRequest) error {
	if err := core.ValidateSite(req.Site); err != nil {
		return err
	}
	if len(req.DBPassword) < 32 || len(req.AdminPassword) < 16 {
		return errors.New("weak generated credentials")
	}
	dir := w.siteDir(req.Site.ID)
	secretDir := filepath.Join(dir, "secrets")
	if err := os.MkdirAll(secretDir, 0700); err != nil {
		return err
	}
	secrets := map[string]string{
		"db_password":    req.DBPassword,
		"admin_password": req.AdminPassword,
		"site_url":       "https://" + req.Site.Domain,
		"site_title":     req.Site.Title,
		"admin_email":    req.Site.AdminEmail,
	}
	if strings.HasSuffix(req.Site.Domain, ".localhost") {
		secrets["site_url"] = "http://" + req.Site.Domain
	}
	rootPasswordPath := filepath.Join(secretDir, "db_root_password")
	if _, err := os.Stat(rootPasswordPath); errors.Is(err, os.ErrNotExist) {
		p, err := core.RandomPassword()
		if err != nil {
			return err
		}
		secrets["db_root_password"] = p
	} else if err != nil {
		return err
	}
	for name, value := range secrets {
		path := filepath.Join(secretDir, name)
		// Database credentials must survive retries. The administrator password is
		// deliberately rotated so a password can be shown after a recovered install.
		_, statErr := os.Stat(path)
		if statErr == nil && name != "admin_password" {
			continue // retry never changes credentials or the installed site's URL
		}
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
		if err := writeSecret(secretDir, name, value); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(fmt.Sprintf(composeTemplate, req.Site.ID)), 0600); err != nil {
		return err
	}
	if err := w.docker.Run(ctx, w.composeArgs(req.Site.ID, "up", "-d", "--wait")...); err != nil {
		return err
	}
	network := "wph-" + req.Site.ID + "_frontend"
	if err := w.docker.Run(ctx, "network", "connect", network, "wph-caddy"); err != nil && !strings.Contains(err.Error(), "already exists") {
		return err
	}
	if err := os.MkdirAll(w.routes, 0700); err != nil {
		return err
	}
	address := req.Site.Domain
	if strings.HasSuffix(address, ".localhost") {
		address = "http://" + address
	}
	route := fmt.Sprintf("%s {\n  reverse_proxy wph-wp-%s:80\n}\n", address, req.Site.ID)
	if err := os.WriteFile(filepath.Join(w.routes, req.Site.ID+".caddy"), []byte(route), 0600); err != nil {
		return err
	}
	if err := w.docker.Run(ctx, "exec", "wph-caddy", "caddy", "reload", "--config", "/etc/caddy/Caddyfile"); err != nil {
		return err
	}
	return w.docker.Run(ctx, w.composeArgs(req.Site.ID, "run", "--rm", "--no-deps", "cli", "sh", "-c", wpInstallScript)...)
}

func (w *worker) action(ctx context.Context, id, action string) error {
	if !core.ValidID(id) {
		return errors.New("invalid site ID")
	}
	if _, err := os.Stat(filepath.Join(w.siteDir(id), "compose.yaml")); err != nil {
		return err
	}
	switch action {
	case "start":
		if err := w.docker.Run(ctx, w.composeArgs(id, "up", "-d", "--wait")...); err != nil {
			return err
		}
		network := "wph-" + id + "_frontend"
		if err := w.docker.Run(ctx, "network", "connect", network, "wph-caddy"); err != nil && !strings.Contains(err.Error(), "already exists") {
			return err
		}
		return nil
	case "stop":
		return w.docker.Run(ctx, w.composeArgs(id, "stop")...)
	case "delete":
		if err := os.Remove(filepath.Join(w.routes, id+".caddy")); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := w.docker.Run(ctx, "exec", "wph-caddy", "caddy", "reload", "--config", "/etc/caddy/Caddyfile"); err != nil {
			return err
		}
		network := "wph-" + id + "_frontend"
		if err := w.docker.Run(ctx, "network", "disconnect", "-f", network, "wph-caddy"); err != nil && !strings.Contains(err.Error(), "not connected") && !strings.Contains(err.Error(), "not found") {
			return err
		}
		if err := w.docker.Run(ctx, w.composeArgs(id, "down", "--volumes")...); err != nil {
			return err
		}
		return os.RemoveAll(w.siteDir(id))
	default:
		return errors.New("invalid action")
	}
}

func (w *worker) ServeHTTP(resp http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost || subtle.ConstantTimeCompare([]byte(req.Header.Get("X-Worker-Token")), []byte(w.token)) != 1 {
		http.Error(resp, "forbidden", http.StatusForbidden)
		return
	}
	req.Body = http.MaxBytesReader(resp, req.Body, 8192)
	ctx, cancel := context.WithTimeout(req.Context(), 20*time.Minute)
	defer cancel()
	var err error
	if req.URL.Path == "/create" {
		var body core.CreateRequest
		if decodeErr := json.NewDecoder(req.Body).Decode(&body); decodeErr != nil {
			http.Error(resp, "invalid request", http.StatusBadRequest)
			return
		}
		err = w.create(ctx, body)
	} else if req.URL.Path == "/action" {
		var body struct{ ID, Action string }
		if decodeErr := json.NewDecoder(req.Body).Decode(&body); decodeErr != nil {
			http.Error(resp, "invalid request", http.StatusBadRequest)
			return
		}
		err = w.action(ctx, body.ID, body.Action)
	} else {
		http.NotFound(resp, req)
		return
	}
	if err != nil {
		log.Printf("worker operation failed: %v", err)
		http.Error(resp, err.Error(), http.StatusInternalServerError)
		return
	}
	resp.WriteHeader(http.StatusNoContent)
}

func main() {
	token := os.Getenv("WORKER_TOKEN")
	dataDir := os.Getenv("WPH_DATA_DIR")
	if len(token) < 32 {
		log.Fatal("WORKER_TOKEN must be at least 32 characters")
	}
	if !filepath.IsAbs(dataDir) {
		log.Fatal("WPH_DATA_DIR must be an absolute host path")
	}
	w := &worker{root: filepath.Join(dataDir, "sites"), routes: "/routes", token: token, docker: dockerRunner{}}
	log.Fatal(http.ListenAndServe(":8081", w))
}
