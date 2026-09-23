package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kazemsoft/panel4wp/internal/core"
)

type fakeDocker struct{ calls [][]string }

func (f *fakeDocker) Run(_ context.Context, args ...string) error {
	f.calls = append(f.calls, append([]string(nil), args...))
	return nil
}

func (f *fakeDocker) Output(_ context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string(nil), args...))
	return []byte("[]"), nil
}

func (f *fakeDocker) Input(_ context.Context, _ []byte, args ...string) error {
	f.calls = append(f.calls, append([]string(nil), args...))
	return nil
}

func TestCreateAndDeleteSite(t *testing.T) {
	base := t.TempDir()
	f := &fakeDocker{}
	w := &worker{root: filepath.Join(base, "sites"), backupsRoot: filepath.Join(base, "backups"), routes: filepath.Join(base, "routes"), docker: f}
	site := core.Site{ID: "0123456789abcdef", Domain: "one.localhost", Title: "سایت آزمایشی", AdminEmail: "admin@example.com"}
	req := core.CreateRequest{Site: site, DBPassword: strings.Repeat("a", 48), AdminPassword: strings.Repeat("b", 48)}
	if err := w.create(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	compose, err := os.ReadFile(filepath.Join(w.siteDir(site.ID), "compose.yaml"))
	if err != nil || !strings.Contains(string(compose), "internal: true") || !strings.Contains(string(compose), "name: wphost-sites-proxy") || !strings.Contains(string(compose), "wph-wp-"+site.ID) || !strings.Contains(string(compose), "mem_limit: 512m") || strings.Contains(string(compose), "%!") {
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
