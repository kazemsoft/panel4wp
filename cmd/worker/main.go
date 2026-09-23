package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kazemsoft/panel4wp/internal/core"
	"github.com/kazemsoft/panel4wp/internal/settings"
)

const composeTemplate = `services:
  wordpress:
    image: wordpress:php8.3-apache
    container_name: wph-wp-%s
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
    mem_limit: %dm
    cpus: %.2f
    pids_limit: 200
    security_opt: [no-new-privileges:true]
  db:
    image: mariadb:11.8
    container_name: wph-db-%s
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
    mem_limit: %dm
    cpus: %.2f
    pids_limit: 200
    security_opt: [no-new-privileges:true]
  phpmyadmin:
    image: phpmyadmin:5.2-apache
    container_name: wph-pma-%s
    profiles: [tools]
    restart: "no"
    environment:
      PMA_HOST: db
      PMA_USER: wordpress
      PMA_PASSWORD_FILE: /run/secrets/db_password
      PMA_ABSOLUTE_URI: %s
    secrets: [db_password]
    networks: [database, tools_proxy]
    depends_on:
      db:
        condition: service_healthy
    mem_limit: 256m
    cpus: 0.50
    pids_limit: 100
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
    external: true
    name: wphost-sites-proxy
  database:
    internal: true
  tools_proxy:
    external: true
    name: wphost-tools-proxy
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

const wpInstallScript = `i=0; while [ ! -f wp-includes/version.php ] && [ "$i" -lt 120 ]; do i=$((i+1)); sleep 1; done; if [ ! -f wp-includes/version.php ]; then echo "WordPress files were not ready after 120 seconds" >&2; exit 1; fi; if wp core is-installed >/dev/null 2>&1; then wp user update admin --user_pass="$(cat /run/secrets/admin_password)" --user_email="$(cat /run/secrets/admin_email)"; else wp core install --url="$(cat /run/secrets/site_url)" --title="$(cat /run/secrets/site_title)" --admin_user=admin --admin_password="$(cat /run/secrets/admin_password)" --admin_email="$(cat /run/secrets/admin_email)" --skip-email; fi`

const wpUpdateScript = `set -eu; wp core update; wp core update-db; wp plugin update --all; wp theme update --all`

type runner interface {
	Run(context.Context, ...string) error
	Output(context.Context, ...string) ([]byte, error)
	Input(context.Context, []byte, ...string) error
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

func (dockerRunner) Output(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("docker %s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func (dockerRunner) Input(ctx context.Context, input []byte, args ...string) error {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdin = bytes.NewReader(input)
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
	panelDomain string
	docker      runner
	toolsMu     sync.Mutex
	toolTimers  map[string]*time.Timer
}

type backupManifest struct {
	Version         int       `json:"version"`
	SiteID          string    `json:"site_id"`
	Domain          string    `json:"domain"`
	CreatedAt       time.Time `json:"created_at"`
	DatabaseSHA256  string    `json:"database_sha256"`
	WordPressSHA256 string    `json:"wordpress_sha256"`
}

const legacyFrontendNetwork = "networks:\n  frontend:\n  database:\n    internal: true"
const sharedFrontendNetwork = "networks:\n  frontend:\n    external: true\n    name: wphost-sites-proxy\n  database:\n    internal: true"

const databaseToolLifetime = 15 * time.Minute

const databaseToolTemplate = `  phpmyadmin:
    image: phpmyadmin:5.2-apache
    container_name: wph-pma-%s
    profiles: [tools]
    restart: "no"
    environment:
      PMA_HOST: db
      PMA_USER: wordpress
      PMA_PASSWORD_FILE: /run/secrets/db_password
      PMA_ABSOLUTE_URI: %s
    secrets: [db_password]
    networks: [database, tools_proxy]
    depends_on:
      db:
        condition: service_healthy
    mem_limit: 256m
    cpus: 0.50
    pids_limit: 100
    security_opt: [no-new-privileges:true]
`

const listFilesPHP = `$root=realpath("/var/www/html/wp-content");$rel=$argv[1]??"";$p=realpath($root.($rel===""?"":"/".$rel));if($root===false||$p===false||($p!==$root&&!str_starts_with($p,$root."/"))||!is_dir($p)){fwrite(STDERR,"invalid directory");exit(3);}$out=[];foreach(scandir($p) as $n){if($n==="."||$n==="..")continue;$f=$p."/".$n;$s=lstat($f);$type=is_link($f)?"link":(is_dir($f)?"directory":"file");$out[]=["name"=>$n,"type"=>$type,"size"=>$type==="file"?(int)$s["size"]:0,"modified"=>(int)$s["mtime"]];}usort($out,fn($a,$b)=>($a["type"]==="directory"?0:1)<=>($b["type"]==="directory"?0:1)?:strnatcasecmp($a["name"],$b["name"]));echo json_encode($out,JSON_UNESCAPED_UNICODE|JSON_INVALID_UTF8_SUBSTITUTE);`

const readFilePHP = `$root=realpath("/var/www/html/wp-content");$p=realpath($root."/".$argv[1]);if($root===false||$p===false||!str_starts_with($p,$root."/")||!is_file($p)||is_link($p)){fwrite(STDERR,"invalid file");exit(3);}if(filesize($p)>10485760){fwrite(STDERR,"file exceeds 10 MB");exit(4);}readfile($p);`

const writeFilePHP = `$root=realpath("/var/www/html/wp-content");$target=$root."/".$argv[1];$parent=realpath(dirname($target));if($root===false||$parent===false||($parent!==$root&&!str_starts_with($parent,$root."/"))||(file_exists($target)&&is_link($target))){fwrite(STDERR,"invalid file");exit(3);}$data=file_get_contents("php://stdin");if(strlen($data)>10485760){fwrite(STDERR,"file exceeds 10 MB");exit(4);}if(file_put_contents($target,$data,LOCK_EX)===false){fwrite(STDERR,"write failed");exit(5);}chmod($target,0644);`

const makeDirPHP = `$root=realpath("/var/www/html/wp-content");$target=$root."/".$argv[1];$parent=realpath(dirname($target));if($root===false||$parent===false||($parent!==$root&&!str_starts_with($parent,$root."/"))||file_exists($target)){fwrite(STDERR,"invalid directory");exit(3);}if(!mkdir($target,0755)){fwrite(STDERR,"mkdir failed");exit(5);}`

const deleteFilePHP = `$root=realpath("/var/www/html/wp-content");$p=realpath($root."/".$argv[1]);if($root===false||$p===false||!str_starts_with($p,$root."/")||is_link($p)){fwrite(STDERR,"invalid path");exit(3);}$ok=is_dir($p)?rmdir($p):unlink($p);if(!$ok){fwrite(STDERR,"delete failed; directories must be empty");exit(5);}`

func (w *worker) siteDir(id string) string { return filepath.Join(w.root, id) }

func (w *worker) backupDir(siteID, backupID string) string {
	return filepath.Join(w.backupsRoot, siteID, backupID)
}

func (w *worker) composeArgs(id string, args ...string) []string {
	base := []string{"compose", "-p", "wph-" + id, "-f", filepath.Join(w.siteDir(id), "compose.yaml")}
	return append(base, args...)
}

func (w *worker) databaseURL(id string) string {
	base := strings.TrimRight(w.panelDomain, "/")
	if !strings.Contains(base, "://") {
		base = "https://" + base
	}
	return base + "/sites/" + id + "/database/"
}

func (w *worker) ensureDatabaseTool(id string) error {
	composePath := filepath.Join(w.siteDir(id), "compose.yaml")
	data, err := os.ReadFile(composePath)
	if err != nil {
		return err
	}
	content := string(data)
	if strings.Contains(content, "  phpmyadmin:\n") {
		return nil
	}
	if !strings.Contains(content, "  cli:\n") || !strings.Contains(content, "  database:\n    internal: true\n") {
		return errors.New("site compose cannot be upgraded with database manager")
	}
	tool := fmt.Sprintf(databaseToolTemplate, id, strconv.Quote(w.databaseURL(id)))
	content = strings.Replace(content, "  cli:\n", tool+"  cli:\n", 1)
	content = strings.Replace(content, "  database:\n    internal: true\n", "  database:\n    internal: true\n  tools_proxy:\n    external: true\n    name: wphost-tools-proxy\n", 1)
	return os.WriteFile(composePath, []byte(content), 0600)
}

func (w *worker) scheduleDatabaseStop(id string) {
	w.toolsMu.Lock()
	defer w.toolsMu.Unlock()
	if w.toolTimers == nil {
		w.toolTimers = make(map[string]*time.Timer)
	}
	if timer := w.toolTimers[id]; timer != nil {
		timer.Stop()
	}
	w.toolTimers[id] = time.AfterFunc(databaseToolLifetime, func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := w.docker.Run(ctx, w.composeArgs(id, "rm", "-sf", "phpmyadmin")...); err != nil {
			log.Printf("stop expired database manager for %s: %v", id, err)
		}
		w.toolsMu.Lock()
		delete(w.toolTimers, id)
		w.toolsMu.Unlock()
	})
}

func (w *worker) databaseAction(ctx context.Context, id, action string) error {
	if !core.ValidID(id) {
		return errors.New("invalid site ID")
	}
	if _, err := os.Stat(filepath.Join(w.siteDir(id), "compose.yaml")); err != nil {
		return err
	}
	if err := w.ensureDatabaseTool(id); err != nil {
		return err
	}
	switch action {
	case "start":
		if err := w.docker.Run(ctx, w.composeArgs(id, "--profile", "tools", "up", "-d", "phpmyadmin")...); err != nil {
			return err
		}
		var readyErr error
		for attempt := 0; attempt < 30; attempt++ {
			readyErr = w.docker.Run(ctx, "exec", "wph-pma-"+id, "php", "-r", `$s=@fsockopen("127.0.0.1",80);exit($s?0:1);`)
			if readyErr == nil {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
		}
		if readyErr != nil {
			return fmt.Errorf("database manager did not become ready: %w", readyErr)
		}
		w.scheduleDatabaseStop(id)
		return nil
	case "stop":
		w.toolsMu.Lock()
		if timer := w.toolTimers[id]; timer != nil {
			timer.Stop()
			delete(w.toolTimers, id)
		}
		w.toolsMu.Unlock()
		return w.docker.Run(ctx, w.composeArgs(id, "rm", "-sf", "phpmyadmin")...)
	default:
		return errors.New("invalid database action")
	}
}

func (w *worker) stats(ctx context.Context, req core.StatsRequest) (map[string]core.SiteStats, error) {
	if len(req.SiteIDs) == 0 || len(req.SiteIDs) > 100 {
		return nil, errors.New("stats request must contain 1 to 100 sites")
	}
	seen := make(map[string]bool, len(req.SiteIDs))
	args := []string{"stats", "--no-stream", "--format", "{{json .}}"}
	for _, id := range req.SiteIDs {
		if !core.ValidID(id) || seen[id] {
			return nil, errors.New("invalid site ID in stats request")
		}
		seen[id] = true
		args = append(args, "wph-wp-"+id, "wph-db-"+id)
	}
	out, err := w.docker.Output(ctx, args...)
	if err != nil {
		return nil, err
	}
	result := make(map[string]core.SiteStats, len(req.SiteIDs))
	type dockerStats struct {
		Name, CPUPerc, MemUsage, MemPerc, NetIO, BlockIO, PIDs string
	}
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		var item dockerStats
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			return nil, fmt.Errorf("parse Docker stats: %w", err)
		}
		var id, kind string
		switch {
		case strings.HasPrefix(item.Name, "wph-wp-"):
			id, kind = strings.TrimPrefix(item.Name, "wph-wp-"), "wordpress"
		case strings.HasPrefix(item.Name, "wph-db-"):
			id, kind = strings.TrimPrefix(item.Name, "wph-db-"), "database"
		default:
			continue
		}
		if !seen[id] {
			continue
		}
		value := &core.ContainerStats{CPU: item.CPUPerc, Memory: item.MemUsage, MemoryPC: item.MemPerc, NetIO: item.NetIO, BlockIO: item.BlockIO, PIDs: item.PIDs}
		site := result[id]
		if kind == "wordpress" {
			site.WordPress = value
		} else {
			site.Database = value
		}
		result[id] = site
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (w *worker) updateSite(ctx context.Context, req core.UpdateRequest) error {
	if err := core.ValidateSite(req.Site); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(w.siteDir(req.Site.ID), "compose.yaml")); err != nil {
		return err
	}
	return w.docker.Run(ctx, w.composeArgs(req.Site.ID, "run", "--rm", "--no-deps", "cli", "sh", "-c", wpUpdateScript)...)
}

func directorySize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if entry.Type().IsRegular() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			total += info.Size()
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	return total, err
}

func (w *worker) volumeSize(ctx context.Context, volume string) (int64, error) {
	out, err := w.docker.Output(ctx, "run", "--rm", "--volume", volume+":/data:ro", "alpine:3.22", "du", "-sk", "/data")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return 0, errors.New("Docker volume size returned no data")
	}
	kib, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil || kib < 0 {
		return 0, errors.New("invalid Docker volume size")
	}
	return kib * 1024, nil
}

func (w *worker) storage(ctx context.Context, req core.StorageRequest) (map[string]core.SiteStorage, error) {
	if len(req.SiteIDs) == 0 || len(req.SiteIDs) > 50 {
		return nil, errors.New("storage request must contain 1 to 50 sites")
	}
	seen := make(map[string]bool, len(req.SiteIDs))
	result := make(map[string]core.SiteStorage, len(req.SiteIDs))
	for _, id := range req.SiteIDs {
		if !core.ValidID(id) || seen[id] {
			return nil, errors.New("invalid site ID in storage request")
		}
		seen[id] = true
		wordpressBytes, err := w.volumeSize(ctx, "wph-"+id+"_wordpress_data")
		if err != nil {
			return nil, fmt.Errorf("WordPress storage for %s: %w", id, err)
		}
		databaseBytes, err := w.volumeSize(ctx, "wph-"+id+"_database_data")
		if err != nil {
			return nil, fmt.Errorf("database storage for %s: %w", id, err)
		}
		backupBytes, err := directorySize(filepath.Join(w.backupsRoot, id))
		if err != nil {
			return nil, fmt.Errorf("backup storage for %s: %w", id, err)
		}
		result[id] = core.SiteStorage{WordPressBytes: wordpressBytes, DatabaseBytes: databaseBytes, BackupBytes: backupBytes}
	}
	return result, nil
}

func phpValue(value string) string {
	return `base64_decode('` + base64.StdEncoding.EncodeToString([]byte(value)) + `')`
}

func (w *worker) applyMail(ctx context.Context, req settings.ApplyRequest) error {
	if !core.ValidID(req.SiteID) {
		return errors.New("invalid site ID")
	}
	container := "wph-wp-" + req.SiteID
	const plugin = "/var/www/html/wp-content/mu-plugins/panel4wp-smtp.php"
	if !req.Mail.Enabled {
		return w.docker.Run(ctx, "exec", container, "rm", "-f", plugin)
	}
	if err := settings.ValidateMail(req.Mail); err != nil {
		return err
	}
	secure := ""
	if req.Mail.Encryption == "starttls" {
		secure = "  $mail->SMTPSecure = 'tls'; $mail->SMTPAutoTLS = true;\n"
	} else if req.Mail.Encryption == "tls" {
		secure = "  $mail->SMTPSecure = 'ssl';\n"
	}
	auth := req.Mail.Username != ""
	php := fmt.Sprintf(`<?php
add_action('phpmailer_init', static function ($mail) {
  $mail->isSMTP();
  $mail->Host = %s;
  $mail->Port = %d;
  $mail->SMTPAuth = %t;
  $mail->Username = %s;
  $mail->Password = %s;
%s});
add_filter('wp_mail_from', static fn () => %s);
add_filter('wp_mail_from_name', static fn () => %s);
`, phpValue(req.Mail.Host), req.Mail.Port, auth, phpValue(req.Mail.Username), phpValue(req.Mail.Password), secure, phpValue(req.Mail.FromEmail), phpValue(req.Mail.FromName))
	command := "umask 077; mkdir -p /var/www/html/wp-content/mu-plugins; cat > " + plugin + "; chown 33:33 " + plugin + "; chmod 0600 " + plugin
	return w.docker.Input(ctx, []byte(php), "exec", "-i", container, "sh", "-c", command)
}

func (w *worker) migrateLegacyNetworks(ctx context.Context) error {
	entries, err := os.ReadDir(w.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		id := entry.Name()
		if !entry.IsDir() || !core.ValidID(id) {
			continue
		}
		composePath := filepath.Join(w.siteDir(id), "compose.yaml")
		data, err := os.ReadFile(composePath)
		if err != nil {
			return err
		}
		if !strings.Contains(string(data), legacyFrontendNetwork) {
			continue
		}
		updated := strings.Replace(string(data), legacyFrontendNetwork, sharedFrontendNetwork, 1)
		if err := os.WriteFile(composePath, []byte(updated), 0600); err != nil {
			return err
		}
		running, inspectErr := w.docker.Output(ctx, "inspect", "--format", "{{.State.Running}}", "wph-wp-"+id)
		if inspectErr == nil && strings.TrimSpace(string(running)) == "true" {
			if err := w.docker.Run(ctx, w.composeArgs(id, "up", "-d", "--wait")...); err != nil {
				return fmt.Errorf("migrate site %s: %w", id, err)
			}
		}
	}
	return nil
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
	dumpCommand := `cnf=/tmp/wph-client.cnf; trap 'rm -f "$cnf"' 0; umask 077; printf '[client]\npassword=%s\n' "$(cat /run/secrets/db_root_password)" > "$cnf"; mariadb-dump --defaults-extra-file="$cnf" --single-transaction --quick --lock-tables=false -u root wordpress > ` + dumpPath
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

func (w *worker) validateFileRequest(req core.FileRequest, allowEmpty bool) error {
	if !core.ValidID(req.SiteID) || !core.ValidRelativePath(req.Path, allowEmpty) {
		return errors.New("invalid file path")
	}
	if _, err := os.Stat(filepath.Join(w.siteDir(req.SiteID), "compose.yaml")); err != nil {
		return errors.New("site does not exist")
	}
	return nil
}

func (w *worker) listFiles(ctx context.Context, req core.FileRequest) ([]core.FileEntry, error) {
	if err := w.validateFileRequest(req, true); err != nil {
		return nil, err
	}
	out, err := w.docker.Output(ctx, "exec", "wph-wp-"+req.SiteID, "php", "-d", "display_errors=stderr", "-r", listFilesPHP, req.Path)
	if err != nil {
		return nil, err
	}
	var entries []core.FileEntry
	if len(out) > 4<<20 || json.Unmarshal(out, &entries) != nil {
		return nil, errors.New("invalid or excessive directory listing")
	}
	return entries, nil
}

func (w *worker) readFile(ctx context.Context, req core.FileRequest) (core.FileContent, error) {
	if err := w.validateFileRequest(req, false); err != nil {
		return core.FileContent{}, err
	}
	out, err := w.docker.Output(ctx, "exec", "wph-wp-"+req.SiteID, "php", "-d", "display_errors=stderr", "-r", readFilePHP, req.Path)
	if err != nil {
		return core.FileContent{}, err
	}
	if len(out) > 10<<20 {
		return core.FileContent{}, errors.New("file exceeds 10 MB")
	}
	return core.FileContent{Name: filepath.Base(req.Path), Content: out}, nil
}

func (w *worker) writeFile(ctx context.Context, req core.FileRequest) error {
	if err := w.validateFileRequest(req, false); err != nil {
		return err
	}
	if len(req.Content) > 10<<20 {
		return errors.New("file exceeds 10 MB")
	}
	return w.docker.Input(ctx, req.Content, "exec", "-i", "wph-wp-"+req.SiteID, "php", "-d", "display_errors=stderr", "-r", writeFilePHP, req.Path)
}

func (w *worker) fileAction(ctx context.Context, req core.FileRequest, action string) error {
	if err := w.validateFileRequest(req, false); err != nil {
		return err
	}
	script := deleteFilePHP
	if action == "mkdir" {
		script = makeDirPHP
	} else if action != "delete" {
		return errors.New("invalid file action")
	}
	return w.docker.Run(ctx, "exec", "wph-wp-"+req.SiteID, "php", "-d", "display_errors=stderr", "-r", script, req.Path)
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
	restoreDB := `cnf=/tmp/wph-client.cnf; trap 'rm -f "$cnf"' 0; umask 077; printf '[client]\npassword=%s\n' "$(cat /run/secrets/db_root_password)" > "$cnf"; mariadb --defaults-extra-file="$cnf" -u root -e 'DROP DATABASE IF EXISTS wordpress; CREATE DATABASE wordpress CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;' && mariadb --defaults-extra-file="$cnf" -u root wordpress < ` + restorePath
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
	compose := fmt.Sprintf(composeTemplate, req.Site.ID, memory, cpus, req.Site.ID, memory, cpus, req.Site.ID, strconv.Quote(w.databaseURL(req.Site.ID)))
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(compose), 0600); err != nil {
		return err
	}
	if err := w.docker.Run(ctx, w.composeArgs(req.Site.ID, "up", "-d", "--wait")...); err != nil {
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
	return w.docker.Run(ctx, w.composeArgs(req.Site.ID, "run", "--rm", "--no-deps", "cli", "sh", "-c", wpInstallScript)...)
}

func (w *worker) action(ctx context.Context, id, action string) error {
	if !core.ValidID(id) {
		return errors.New("invalid site ID")
	}
	if _, err := os.Stat(filepath.Join(w.siteDir(id), "compose.yaml")); err != nil {
		return err
	}
	if action == "stop" || action == "delete" {
		if err := w.databaseAction(ctx, id, "stop"); err != nil {
			return fmt.Errorf("stop database manager: %w", err)
		}
	}
	switch action {
	case "start":
		if err := w.docker.Run(ctx, w.composeArgs(id, "up", "-d", "--wait")...); err != nil {
			return err
		}
		return nil
	case "stop":
		return w.docker.Run(ctx, w.composeArgs(id, "stop")...)
	case "delete":
		if err := os.Remove(filepath.Join(w.routes, id+".caddy")); err != nil && !errors.Is(err, os.ErrNotExist) {
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
	maxBody := int64(8192)
	if req.URL.Path == "/files/write" {
		maxBody = 15 << 20
	}
	req.Body = http.MaxBytesReader(resp, req.Body, maxBody)
	ctx, cancel := context.WithTimeout(req.Context(), 20*time.Minute)
	defer cancel()
	var err error
	var result any
	reloadCaddy := false
	if req.URL.Path == "/create" {
		var body core.CreateRequest
		if decodeErr := json.NewDecoder(req.Body).Decode(&body); decodeErr != nil {
			http.Error(resp, "invalid request", http.StatusBadRequest)
			return
		}
		err = w.create(ctx, body)
		reloadCaddy = err == nil
	} else if req.URL.Path == "/action" {
		var body struct{ ID, Action string }
		if decodeErr := json.NewDecoder(req.Body).Decode(&body); decodeErr != nil {
			http.Error(resp, "invalid request", http.StatusBadRequest)
			return
		}
		err = w.action(ctx, body.ID, body.Action)
		reloadCaddy = err == nil && body.Action == "delete"
	} else if req.URL.Path == "/backup" {
		var body core.BackupRequest
		if decodeErr := json.NewDecoder(req.Body).Decode(&body); decodeErr != nil {
			http.Error(resp, "invalid request", http.StatusBadRequest)
			return
		}
		result, err = w.backup(ctx, body)
	} else if req.URL.Path == "/database" {
		var body struct{ ID, Action string }
		if decodeErr := json.NewDecoder(req.Body).Decode(&body); decodeErr != nil {
			http.Error(resp, "invalid request", http.StatusBadRequest)
			return
		}
		err = w.databaseAction(ctx, body.ID, body.Action)
	} else if req.URL.Path == "/stats" {
		var body core.StatsRequest
		if decodeErr := json.NewDecoder(req.Body).Decode(&body); decodeErr != nil {
			http.Error(resp, "invalid request", http.StatusBadRequest)
			return
		}
		result, err = w.stats(ctx, body)
	} else if req.URL.Path == "/update" {
		var body core.UpdateRequest
		if decodeErr := json.NewDecoder(req.Body).Decode(&body); decodeErr != nil {
			http.Error(resp, "invalid request", http.StatusBadRequest)
			return
		}
		err = w.updateSite(ctx, body)
	} else if req.URL.Path == "/storage" {
		var body core.StorageRequest
		if decodeErr := json.NewDecoder(req.Body).Decode(&body); decodeErr != nil {
			http.Error(resp, "invalid request", http.StatusBadRequest)
			return
		}
		result, err = w.storage(ctx, body)
	} else if req.URL.Path == "/mail" {
		var body settings.ApplyRequest
		if decodeErr := json.NewDecoder(req.Body).Decode(&body); decodeErr != nil {
			http.Error(resp, "invalid request", http.StatusBadRequest)
			return
		}
		err = w.applyMail(ctx, body)
	} else if req.URL.Path == "/restore" {
		var body core.RestoreRequest
		if decodeErr := json.NewDecoder(req.Body).Decode(&body); decodeErr != nil {
			http.Error(resp, "invalid request", http.StatusBadRequest)
			return
		}
		err = w.restore(ctx, body)
	} else if req.URL.Path == "/files/list" || req.URL.Path == "/files/read" || req.URL.Path == "/files/write" || req.URL.Path == "/files/delete" || req.URL.Path == "/files/mkdir" {
		var body core.FileRequest
		if decodeErr := json.NewDecoder(req.Body).Decode(&body); decodeErr != nil {
			http.Error(resp, "invalid request", http.StatusBadRequest)
			return
		}
		switch req.URL.Path {
		case "/files/list":
			result, err = w.listFiles(ctx, body)
		case "/files/read":
			result, err = w.readFile(ctx, body)
		case "/files/write":
			err = w.writeFile(ctx, body)
		case "/files/delete":
			err = w.fileAction(ctx, body, "delete")
		case "/files/mkdir":
			err = w.fileAction(ctx, body, "mkdir")
		}
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
	} else {
		resp.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(resp).Encode(result); err != nil {
			log.Printf("worker response failed: %v", err)
		}
	}
	if flusher, ok := resp.(http.Flusher); ok {
		flusher.Flush()
	}
	if reloadCaddy {
		go func() {
			// The administrator request itself is proxied through Caddy. Give that
			// small HTML response time to finish before swapping Caddy's config.
			time.Sleep(2 * time.Second)
			reloadCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := w.docker.Run(reloadCtx, "exec", "wph-caddy", "caddy", "reload", "--config", "/etc/caddy/Caddyfile"); err != nil {
				log.Printf("delayed Caddy reload failed: %v", err)
			}
		}()
	}
}

func main() {
	token := os.Getenv("WORKER_TOKEN")
	dataDir := os.Getenv("WPH_DATA_DIR")
	panelDomain := os.Getenv("PANEL_DOMAIN")
	if len(token) < 32 {
		log.Fatal("WORKER_TOKEN must be at least 32 characters")
	}
	if !filepath.IsAbs(dataDir) {
		log.Fatal("WPH_DATA_DIR must be an absolute host path")
	}
	if panelDomain == "" {
		log.Fatal("PANEL_DOMAIN is required")
	}
	w := &worker{root: filepath.Join(dataDir, "sites"), backupsRoot: filepath.Join(dataDir, "backups"), routes: "/routes", token: token, panelDomain: panelDomain, docker: dockerRunner{}, toolTimers: make(map[string]*time.Timer)}
	migrateCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := w.migrateLegacyNetworks(migrateCtx); err != nil {
		log.Fatal(err)
	}
	log.Fatal(http.ListenAndServe(":8081", w))
}
