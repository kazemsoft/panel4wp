package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/example/wp-host-panel/internal/core"
)

type fakeDocker struct{ calls [][]string }

func (f *fakeDocker) Run(_ context.Context, args ...string) error {
	f.calls = append(f.calls, append([]string(nil), args...))
	return nil
}

func TestCreateAndDeleteSite(t *testing.T) {
	base := t.TempDir()
	f := &fakeDocker{}
	w := &worker{root: filepath.Join(base, "sites"), routes: filepath.Join(base, "routes"), docker: f}
	site := core.Site{ID: "0123456789abcdef", Domain: "one.localhost", Title: "سایت آزمایشی", AdminEmail: "admin@example.com"}
	req := core.CreateRequest{Site: site, DBPassword: strings.Repeat("a", 48), AdminPassword: strings.Repeat("b", 48)}
	if err := w.create(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	compose, err := os.ReadFile(filepath.Join(w.siteDir(site.ID), "compose.yaml"))
	if err != nil || !strings.Contains(string(compose), "internal: true") || !strings.Contains(string(compose), "wph-wp-"+site.ID) {
		t.Fatalf("bad site compose: %v, %s", err, compose)
	}
	route, err := os.ReadFile(filepath.Join(w.routes, site.ID+".caddy"))
	if err != nil || !strings.Contains(string(route), "http://one.localhost") {
		t.Fatalf("bad route: %v, %s", err, route)
	}
	if len(f.calls) != 4 {
		t.Fatalf("expected compose up, network connect, caddy reload, WP CLI: %#v", f.calls)
	}
	if err := w.action(context.Background(), site.ID, "delete"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(w.siteDir(site.ID)); !os.IsNotExist(err) {
		t.Fatalf("site directory remains: %v", err)
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
