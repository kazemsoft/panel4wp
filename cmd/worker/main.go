package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
    mem_limit: %[2]dm
    cpus: %[3].2f
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
    mem_limit: %[2]dm
    cpus: %[3].2f
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
	root        string
	backupsRoot string
	routes      string
	token       string
	docker      runner
}

type backupManifest struct {
	Version         int       `json:"version"`
	SiteID          string    `json:"site_id"`
	Domain          string    `json:"domain"`
	CreatedAt       time.Time `json:"created_at"`
	DatabaseSHA256  string    `json:"database_sha256"`
	WordPressSHA256 string    `json:"wordpress_sha256"`
}

func (w *worker) siteDir(id string) string { return filepath.Join(w.root, id) }

func (w *worker) backupDir(siteID, backupID string) string {
	return filepath.Join(w.backupsRoot, siteID, backupID)
}

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

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func (w *worker) backup(ctx context.Context, req core.BackupRequest) (core.Backup, error) {
	if err := core.ValidateSite(req.Site); err != nil {
		return core.Backup{}, err
	}
	if !core.ValidBackupID(req.BackupID) || w.backupsRoot == "" {
		return core.Backup{}, errors.New("invalid backup request")
	}
	if _, err := os.Stat(filepath.Join(w.siteDir(req.Site.ID), "compose.yaml")); err != nil {
		return core.Backup{}, err
	}
	parent := filepath.Join(w.backupsRoot, req.Site.ID)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return core.Backup{}, err
	}
	tmp, err := os.MkdirTemp(parent, ".creating-")
	if err != nil {
		return core.Backup{}, err
	}
	defer os.RemoveAll(tmp)
	dbContainer := "wph-db-" + req.Site.ID
	const dumpPath = "/tmp/wph-backup.sql"
	dumpCommand := `MARIADB_PWD="$(cat /run/secrets/db_root_password)" mariadb-dump --single-transaction --quick --lock-tables=false -u root wordpress > ` + dumpPath
	if err := w.docker.Run(ctx, "exec", dbContainer, "sh", "-c", dumpCommand); err != nil {
		return core.Backup{}, err
	}
	defer w.docker.Run(context.Background(), "exec", dbContainer, "rm", "-f", dumpPath)
	if err := w.docker.Run(ctx, "cp", dbContainer+":"+dumpPath, filepath.Join(tmp, "database.sql")); err != nil {
		return core.Backup{}, err
	}
	volume := "wph-" + req.Site.ID + "_wordpress_data"
	if err := w.docker.Run(ctx, "run", "--rm", "--volume", volume+":/source:ro", "--volume", tmp+":/backup", "alpine:3.22", "tar", "-czf", "/backup/wordpress.tar.gz", "-C", "/source", "."); err != nil {
		return core.Backup{}, err
	}
	dbHash, err := fileSHA256(filepath.Join(tmp, "database.sql"))
	if err != nil {
		return core.Backup{}, err
	}
	wpHash, err := fileSHA256(filepath.Join(tmp, "wordpress.tar.gz"))
	if err != nil {
		return core.Backup{}, err
	}
	createdAt := time.Now().UTC()
	manifest := backupManifest{Version: 1, SiteID: req.Site.ID, Domain: req.Site.Domain, CreatedAt: createdAt, DatabaseSHA256: dbHash, WordPressSHA256: wpHash}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return core.Backup{}, err
	}
	if err := os.WriteFile(filepath.Join(tmp, "manifest.json"), data, 0600); err != nil {
		return core.Backup{}, err
	}
	destination := w.backupDir(req.Site.ID, req.BackupID)
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		return core.Backup{}, errors.New("backup already exists")
	}
	if err := os.Rename(tmp, destination); err != nil {
		return core.Backup{}, err
	}
	return core.Backup{ID: req.BackupID, CreatedAt: createdAt}, nil
}

func (w *worker) verifyBackup(siteID, backupID string) (string, error) {
	if !core.ValidID(siteID) || !core.ValidBackupID(backupID) || w.backupsRoot == "" {
		return "", errors.New("invalid backup")
	}
	dir := w.backupDir(siteID, backupID)
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return "", err
	}
	var manifest backupManifest
	if json.Unmarshal(data, &manifest) != nil || manifest.Version != 1 || manifest.SiteID != siteID {
		return "", errors.New("invalid backup manifest")
	}
	for name, expected := range map[string]string{"database.sql": manifest.DatabaseSHA256, "wordpress.tar.gz": manifest.WordPressSHA256} {
		actual, err := fileSHA256(filepath.Join(dir, name))
		if err != nil || subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) != 1 {
			return "", fmt.Errorf("backup checksum failed for %s", name)
		}
	}
	return dir, nil
}

func (w *worker) restore(ctx context.Context, req core.RestoreRequest) error {
	if err := core.ValidateSite(req.Site); err != nil {
		return err
	}
	backupDir, err := w.verifyBackup(req.Site.ID, req.BackupID)
	if err != nil {
		return err
	}
	if req.SafetyBackupID == req.BackupID {
		return errors.New("safety backup must differ from restore point")
	}
	if _, err := w.verifyBackup(req.Site.ID, req.SafetyBackupID); err != nil {
		return fmt.Errorf("safety backup is missing or invalid: %w", err)
	}
	id := req.Site.ID
	if err := w.docker.Run(ctx, w.composeArgs(id, "stop", "wordpress")...); err != nil {
		return err
	}
	restart := true
	defer func() {
		if restart {
			_ = w.docker.Run(context.Background(), w.composeArgs(id, "start", "wordpress")...)
		}
	}()
	volume := "wph-" + id + "_wordpress_data"
	restoreFiles := `rm -rf /target/* /target/.[!.]* /target/..?*; tar -xzf /backup/wordpress.tar.gz -C /target`
	if err := w.docker.Run(ctx, "run", "--rm", "--volume", volume+":/target", "--volume", backupDir+":/backup:ro", "alpine:3.22", "sh", "-c", restoreFiles); err != nil {
		return err
	}
	dbContainer := "wph-db-" + id
	const restorePath = "/tmp/wph-restore.sql"
	if err := w.docker.Run(ctx, "cp", filepath.Join(backupDir, "database.sql"), dbContainer+":"+restorePath); err != nil {
		return err
	}
	defer w.docker.Run(context.Background(), "exec", dbContainer, "rm", "-f", restorePath)
	restoreDB := `export MARIADB_PWD="$(cat /run/secrets/db_root_password)"; mariadb -u root -e 'DROP DATABASE IF EXISTS wordpress; CREATE DATABASE wordpress CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;' && mariadb -u root wordpress < ` + restorePath
	if err := w.docker.Run(ctx, "exec", dbContainer, "sh", "-c", restoreDB); err != nil {
		return err
	}
	if err := w.docker.Run(ctx, w.composeArgs(id, "start", "wordpress")...); err != nil {
		return err
	}
	restart = false
	return nil
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
	memory, cpus := core.ResourceLimits(req.Site)
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(fmt.Sprintf(composeTemplate, req.Site.ID, memory, cpus)), 0600); err != nil {
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
		if err := os.RemoveAll(w.siteDir(id)); err != nil {
			return err
		}
		if w.backupsRoot != "" {
			return os.RemoveAll(filepath.Join(w.backupsRoot, id))
		}
		return nil
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
	var result any
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
	} else if req.URL.Path == "/backup" {
		var body core.BackupRequest
		if decodeErr := json.NewDecoder(req.Body).Decode(&body); decodeErr != nil {
			http.Error(resp, "invalid request", http.StatusBadRequest)
			return
		}
		result, err = w.backup(ctx, body)
	} else if req.URL.Path == "/restore" {
		var body core.RestoreRequest
		if decodeErr := json.NewDecoder(req.Body).Decode(&body); decodeErr != nil {
			http.Error(resp, "invalid request", http.StatusBadRequest)
			return
		}
		err = w.restore(ctx, body)
	} else {
		http.NotFound(resp, req)
		return
	}
	if err != nil {
		log.Printf("worker operation failed: %v", err)
		http.Error(resp, err.Error(), http.StatusInternalServerError)
		return
	}
	if result == nil {
		resp.WriteHeader(http.StatusNoContent)
		return
	}
	resp.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(resp).Encode(result); err != nil {
		log.Printf("worker response failed: %v", err)
	}
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
	w := &worker{root: filepath.Join(dataDir, "sites"), backupsRoot: filepath.Join(dataDir, "backups"), routes: "/routes", token: token, docker: dockerRunner{}}
	log.Fatal(http.ListenAndServe(":8081", w))
}
