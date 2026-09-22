package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/example/wp-host-panel/internal/core"
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
