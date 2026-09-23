package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kazemsoft/panel4wp/internal/core"
	"github.com/kazemsoft/panel4wp/internal/settings"
)

type fakeDocker struct {
	calls  [][]string
	inputs [][]byte
}

func (f *fakeDocker) Run(_ context.Context, args ...string) error {
	f.calls = append(f.calls, append([]string(nil), args...))
	return nil
}

type outputDocker struct {
	fakeDocker
	output []byte
}

type sizeDocker struct {
	fakeDocker
	outputs [][]byte
}

func (f *sizeDocker) Output(_ context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string(nil), args...))
	out := f.outputs[0]
	f.outputs = f.outputs[1:]
	return out, nil
}

func (f *outputDocker) Output(_ context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string(nil), args...))
	return f.output, nil
}

func (f *fakeDocker) Output(_ context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string(nil), args...))
	return []byte("[]"), nil
}

func (f *fakeDocker) Input(_ context.Context, input []byte, args ...string) error {
	f.calls = append(f.calls, append([]string(nil), args...))
	f.inputs = append(f.inputs, append([]byte(nil), input...))
	return nil
}

func TestCreateAndDeleteSite(t *testing.T) {
	base := t.TempDir()
	f := &fakeDocker{}
	w := &worker{root: filepath.Join(base, "sites"), backupsRoot: filepath.Join(base, "backups"), routes: filepath.Join(base, "routes"), panelDomain: "http://localhost", docker: f}
	site := core.Site{ID: "0123456789abcdef", Domain: "one.localhost", Title: "سایت آزمایشی", AdminEmail: "admin@example.com"}
	req := core.CreateRequest{Site: site, DBPassword: strings.Repeat("a", 48), AdminPassword: strings.Repeat("b", 48)}
	if err := w.create(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	compose, err := os.ReadFile(filepath.Join(w.siteDir(site.ID), "compose.yaml"))
	if err != nil || !strings.Contains(string(compose), "internal: true") || !strings.Contains(string(compose), "name: wphost-sites-proxy") || !strings.Contains(string(compose), "name: wphost-tools-proxy") || !strings.Contains(string(compose), "wph-pma-"+site.ID) || !strings.Contains(string(compose), "PMA_PASSWORD_FILE: /run/secrets/db_password") || !strings.Contains(string(compose), "http://localhost/sites/"+site.ID+"/database/") || strings.Contains(string(compose), "ports:") || !strings.Contains(string(compose), "wph-wp-"+site.ID) || !strings.Contains(string(compose), "mem_limit: 512m") || strings.Contains(string(compose), "%!") {
		t.Fatalf("bad site compose: %v, %s", err, compose)
	}
	route, err := os.ReadFile(filepath.Join(w.routes, site.ID+".caddy"))
	if err != nil || !strings.Contains(string(route), "http://one.localhost") {
		t.Fatalf("bad route: %v, %s", err, route)
	}
	if len(f.calls) != 2 {
		t.Fatalf("expected compose up and WP CLI: %#v", f.calls)
	}
	if err := os.MkdirAll(filepath.Join(w.backupsRoot, site.ID, "old-backup"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := w.action(context.Background(), site.ID, "delete"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(w.siteDir(site.ID)); !os.IsNotExist(err) {
		t.Fatalf("site directory remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(w.backupsRoot, site.ID)); !os.IsNotExist(err) {
		t.Fatalf("backup directory remains: %v", err)
	}
}

func TestDatabaseManagerLifecycleAndLegacyComposeUpgrade(t *testing.T) {
	base := t.TempDir()
	f := &fakeDocker{}
	w := &worker{root: filepath.Join(base, "sites"), panelDomain: "panel.example.com", docker: f, toolTimers: make(map[string]*time.Timer)}
	id := "0123456789abcdef"
	if err := os.MkdirAll(w.siteDir(id), 0700); err != nil {
		t.Fatal(err)
	}
	legacy := "services:\n  cli:\n    image: wordpress:cli\nnetworks:\n  database:\n    internal: true\n"
	if err := os.WriteFile(filepath.Join(w.siteDir(id), "compose.yaml"), []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	if err := w.databaseAction(context.Background(), id, "start"); err != nil {
		t.Fatal(err)
	}
	updated, _ := os.ReadFile(filepath.Join(w.siteDir(id), "compose.yaml"))
	if !strings.Contains(string(updated), "wph-pma-"+id) || !strings.Contains(string(updated), "https://panel.example.com/sites/"+id+"/database/") {
		t.Fatalf("database tool was not added safely: %s", updated)
	}
	if len(f.calls) != 2 || !strings.Contains(strings.Join(f.calls[0], " "), "--profile tools up -d phpmyadmin") {
		t.Fatalf("unexpected Docker calls: %#v", f.calls)
	}
	if err := w.databaseAction(context.Background(), id, "stop"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(f.calls[len(f.calls)-1], " "), "rm -sf phpmyadmin") {
		t.Fatalf("database manager was not removed: %#v", f.calls)
	}
}

func TestStatsParsesOnlyRequestedSiteContainers(t *testing.T) {
	id := "0123456789abcdef"
	f := &outputDocker{output: []byte(
		`{"Name":"wph-wp-0123456789abcdef","CPUPerc":"1.25%","MemUsage":"64MiB / 384MiB","MemPerc":"16.7%","NetIO":"1kB / 2kB","BlockIO":"3MB / 4MB","PIDs":"12"}` + "\n" +
			`{"Name":"wph-db-0123456789abcdef","CPUPerc":"0.50%","MemUsage":"96MiB / 384MiB","MemPerc":"25.0%","NetIO":"2kB / 1kB","BlockIO":"5MB / 6MB","PIDs":"20"}` + "\n" +
			`{"Name":"wph-wp-ffffffffffffffff","CPUPerc":"99%"}` + "\n")}
	w := &worker{docker: f}
	got, err := w.stats(context.Background(), core.StatsRequest{SiteIDs: []string{id}})
	if err != nil {
		t.Fatal(err)
	}
	if got[id].WordPress == nil || got[id].WordPress.CPU != "1.25%" || got[id].Database == nil || got[id].Database.MemoryPC != "25.0%" {
		t.Fatalf("unexpected stats: %#v", got)
	}
	if len(got) != 1 || len(f.calls) != 1 || !strings.Contains(strings.Join(f.calls[0], " "), "wph-wp-"+id+" wph-db-"+id) {
		t.Fatalf("unexpected Docker request: %#v, result %#v", f.calls, got)
	}
	if _, err := w.stats(context.Background(), core.StatsRequest{SiteIDs: []string{"../escape"}}); err == nil {
		t.Fatal("invalid site ID reached stats")
	}
}

func TestUpdateSiteUsesIsolatedCLI(t *testing.T) {
	base := t.TempDir()
	f := &fakeDocker{}
	w := &worker{root: filepath.Join(base, "sites"), docker: f}
	site := core.Site{ID: "0123456789abcdef", Domain: "update.localhost", Title: "Update", AdminEmail: "admin@example.com"}
	if err := os.MkdirAll(w.siteDir(site.ID), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.siteDir(site.ID), "compose.yaml"), []byte("services: {}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := w.updateSite(context.Background(), core.UpdateRequest{Site: site}); err != nil {
		t.Fatal(err)
	}
	call := strings.Join(f.calls[0], " ")
	if !strings.Contains(call, "run --rm --no-deps cli sh -c") || !strings.Contains(call, "wp core update-db") || !strings.Contains(call, "wp plugin update --all") || !strings.Contains(call, "wp theme update --all") {
		t.Fatalf("unexpected update command: %s", call)
	}
}

func TestStorageMeasuresIsolatedVolumesAndBackups(t *testing.T) {
	base := t.TempDir()
	id := "0123456789abcdef"
	backupDir := filepath.Join(base, "backups", id)
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backupDir, "archive.bin"), make([]byte, 1234), 0600); err != nil {
		t.Fatal(err)
	}
	f := &sizeDocker{outputs: [][]byte{[]byte("10\t/data\n"), []byte("20\t/data\n")}}
	w := &worker{backupsRoot: filepath.Join(base, "backups"), docker: f}
	got, err := w.storage(context.Background(), core.StorageRequest{SiteIDs: []string{id}})
	if err != nil {
		t.Fatal(err)
	}
	if got[id].WordPressBytes != 10*1024 || got[id].DatabaseBytes != 20*1024 || got[id].BackupBytes != 1234 {
		t.Fatalf("unexpected storage result: %#v", got[id])
	}
	if len(f.calls) != 2 || !strings.Contains(strings.Join(f.calls[0], " "), "wph-"+id+"_wordpress_data:/data:ro") || !strings.Contains(strings.Join(f.calls[1], " "), "wph-"+id+"_database_data:/data:ro") {
		t.Fatalf("unexpected volume calls: %#v", f.calls)
	}
	if _, err := w.storage(context.Background(), core.StorageRequest{SiteIDs: []string{"../escape"}}); err == nil {
		t.Fatal("unsafe storage request accepted")
	}
}

func TestApplyMailWritesMUPluginWithoutLiteralCredentials(t *testing.T) {
	f := &fakeDocker{}
	w := &worker{docker: f}
	mail := settings.Mail{Enabled: true, Host: "smtp.example.com", Port: 587, Encryption: "starttls", Username: "mailer", Password: "secret-password", FromEmail: "hello@example.com", FromName: "Example"}
	if err := w.applyMail(context.Background(), settings.ApplyRequest{SiteID: "0123456789abcdef", Mail: mail}); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 || !strings.Contains(strings.Join(f.calls[0], " "), "wph-wp-0123456789abcdef") {
		t.Fatalf("unexpected Docker calls: %#v", f.calls)
	}
	if len(f.inputs) != 1 || strings.Contains(string(f.inputs[0]), mail.Password) || !strings.Contains(string(f.inputs[0]), "base64_decode") {
		t.Fatalf("mail plugin did not encode credentials: %s", f.inputs[0])
	}
}

func TestCreateRetryKeepsDatabasePasswordAndRotatesAdminPassword(t *testing.T) {
	base := t.TempDir()
	w := &worker{root: filepath.Join(base, "sites"), routes: filepath.Join(base, "routes"), docker: &fakeDocker{}}
	site := core.Site{ID: "fedcba9876543210", Domain: "retry.localhost", Title: "Retry", AdminEmail: "admin@example.com"}
	first := core.CreateRequest{Site: site, DBPassword: strings.Repeat("a", 48), AdminPassword: strings.Repeat("b", 48)}
	if err := w.create(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	second := core.CreateRequest{Site: site, DBPassword: strings.Repeat("c", 48), AdminPassword: strings.Repeat("d", 48)}
	if err := w.create(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	secretDir := filepath.Join(w.siteDir(site.ID), "secrets")
	dbPassword, err := os.ReadFile(filepath.Join(secretDir, "db_password"))
	if err != nil || string(dbPassword) != first.DBPassword {
		t.Fatalf("database password changed on retry: %v", err)
	}
	adminPassword, err := os.ReadFile(filepath.Join(secretDir, "admin_password"))
	if err != nil || string(adminPassword) != second.AdminPassword {
		t.Fatalf("administrator password was not rotated: %v", err)
	}
}

func TestRejectInvalidSiteBeforeDocker(t *testing.T) {
	f := &fakeDocker{}
	w := &worker{root: t.TempDir(), routes: t.TempDir(), docker: f}
	site := core.Site{ID: "../escape", Domain: "bad.example.com", Title: "bad", AdminEmail: "a@example.com"}
	if err := w.create(context.Background(), core.CreateRequest{Site: site, DBPassword: strings.Repeat("a", 48), AdminPassword: strings.Repeat("b", 48)}); err == nil {
		t.Fatal("accepted path traversal")
	}
	if len(f.calls) != 0 {
		t.Fatal("invalid request reached Docker")
	}
}

func TestVerifyBackupRejectsTampering(t *testing.T) {
	base := t.TempDir()
	w := &worker{backupsRoot: filepath.Join(base, "backups")}
	siteID, backupID := "0123456789abcdef", "20260923T010203Z-aabbccdd"
	dir := w.backupDir(siteID, backupID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"database.sql": "database", "wordpress.tar.gz": "archive"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	dbHash, _ := fileSHA256(filepath.Join(dir, "database.sql"))
	wpHash, _ := fileSHA256(filepath.Join(dir, "wordpress.tar.gz"))
	manifest, _ := json.Marshal(backupManifest{Version: 1, SiteID: siteID, Domain: "one.localhost", CreatedAt: time.Now(), DatabaseSHA256: dbHash, WordPressSHA256: wpHash})
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.verifyBackup(siteID, backupID); err != nil {
		t.Fatalf("valid backup rejected: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "database.sql"), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.verifyBackup(siteID, backupID); err == nil {
		t.Fatal("tampered backup accepted")
	}
	if _, err := w.verifyBackup("../escape", backupID); err == nil {
		t.Fatal("path traversal accepted")
	}
}

func TestDeleteBackupIsScopedAndValidated(t *testing.T) {
	base := t.TempDir()
	w := &worker{backupsRoot: filepath.Join(base, "backups")}
	siteID, backupID := "0123456789abcdef", "20260923T010203Z-aabbccdd"
	dir := w.backupDir(siteID, backupID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := w.deleteBackup(core.DeleteBackupRequest{SiteID: siteID, BackupID: backupID}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backup remains: %v", err)
	}
	if err := w.deleteBackup(core.DeleteBackupRequest{SiteID: "../escape", BackupID: backupID}); err == nil {
		t.Fatal("unsafe site ID accepted")
	}
}

func TestFileRequestsRejectTraversalBeforeDocker(t *testing.T) {
	base := t.TempDir()
	f := &fakeDocker{}
	w := &worker{root: filepath.Join(base, "sites"), docker: f}
	if err := os.MkdirAll(w.siteDir("0123456789abcdef"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.siteDir("0123456789abcdef"), "compose.yaml"), []byte("services: {}"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := w.listFiles(context.Background(), core.FileRequest{SiteID: "0123456789abcdef", Path: "../secrets"})
	if err == nil {
		t.Fatal("path traversal accepted")
	}
	if len(f.calls) != 0 {
		t.Fatal("unsafe path reached Docker")
	}
	if _, err := w.listFiles(context.Background(), core.FileRequest{SiteID: "0123456789abcdef", Path: ""}); err != nil {
		t.Fatalf("safe root listing rejected: %v", err)
	}
}

func TestMigrateLegacyNetworkConfig(t *testing.T) {
	base := t.TempDir()
	f := &fakeDocker{}
	w := &worker{root: filepath.Join(base, "sites"), docker: f}
	id := "0123456789abcdef"
	if err := os.MkdirAll(w.siteDir(id), 0700); err != nil {
		t.Fatal(err)
	}
	legacy := "services: {}\n" + legacyFrontendNetwork + "\n"
	composePath := filepath.Join(w.siteDir(id), "compose.yaml")
	if err := os.WriteFile(composePath, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	if err := w.migrateLegacyNetworks(context.Background()); err != nil {
		t.Fatal(err)
	}
	updated, err := os.ReadFile(composePath)
	if err != nil || !strings.Contains(string(updated), sharedFrontendNetwork) || strings.Contains(string(updated), legacyFrontendNetwork) {
		t.Fatalf("legacy network was not migrated: %v, %s", err, updated)
	}
}
