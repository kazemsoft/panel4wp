package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kazemsoft/panel4wp/internal/core"
)

func TestStorePersistsSites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "sites.json")
	s := New(path)
	site := core.Site{ID: "0123456789abcdef", Domain: "test.localhost", Status: core.StatusCreating, CreatedAt: time.Now()}
	if err := s.Put(site); err != nil {
		t.Fatal(err)
	}
	reopened := New(path)
	got, ok, err := reopened.Get(site.ID)
	if err != nil || !ok || got.Domain != site.Domain {
		t.Fatalf("site not persisted: %#v, %v, %v", got, ok, err)
	}
	if err := reopened.Delete(site.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.List(); err != nil || len(got) != 0 {
		t.Fatalf("site not deleted: %#v, %v", got, err)
	}
}

func TestOldSiteMetadataLoadsWithoutBackupPolicy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sites.json")
	legacy := `{"0123456789abcdef":{"id":"0123456789abcdef","domain":"test.localhost","status":"running","created_at":"2026-09-20T00:00:00Z"}}`
	if err := os.WriteFile(path, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	site, ok, err := New(path).Get("0123456789abcdef")
	if err != nil || !ok {
		t.Fatalf("legacy site did not load: %v", err)
	}
	if site.BackupPolicy.Enabled || site.BackupPolicy.IntervalHours != 0 || !site.BackupPolicy.NextRunAt.IsZero() {
		t.Fatalf("legacy site gained an active schedule: %#v", site.BackupPolicy)
	}
}
