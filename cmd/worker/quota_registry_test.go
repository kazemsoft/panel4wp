package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type inventoryDocker struct {
	resourceDocker
	mu        sync.Mutex
	inventory string
}

func (f *inventoryDocker) Output(ctx context.Context, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if args[0] == "volume" {
		id := strings.TrimSuffix(strings.TrimPrefix(args[len(args)-2], "wph-"), "_wordpress_data")
		return []byte(fmt.Sprintf(`{"name":"wph-%s_wordpress_data","driver":"local","project":"wph-%s","volume":"wordpress_data","options":false}
{"name":"wph-%s_database_data","driver":"local","project":"wph-%s","volume":"database_data","options":false}`, id, id, id, id)), nil
	}
	if args[0] == "run" && args[len(args)-1] == "--inventory" {
		return []byte(f.inventory), nil
	}
	return nil, fmt.Errorf("unexpected reservation Docker command: %s", args[0])
}

func registryWorker(t *testing.T) (*worker, *inventoryDocker) {
	t.Helper()
	f := &inventoryDocker{inventory: `{"verified":true,"reason":"verified","filesystem_uuid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","scanned_inodes":20,"wordpress_inodes":2,"database_inodes":1,"project_ids":[1000000,1000001,4000000001]}`}
	w := &worker{root: t.TempDir(), docker: f, storageImage: "sha256:" + strings.Repeat("a", 64)}
	registrySite(t, w, quotaTestID)
	return w, f
}

func registrySite(t *testing.T, w *worker, id string) {
	t.Helper()
	if err := os.MkdirAll(w.siteDir(id), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.siteDir(id), "compose.yaml"), []byte("existing site data"), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestQuotaRegistryReservationsSurviveRestartAndDeletion(t *testing.T) {
	w, f := registryWorker(t)
	first, err := w.reserveQuotaProject(context.Background(), quotaTestID)
	if err != nil || first.ProjectID != 1000002 {
		t.Fatal(first, err)
	}
	restarted := &worker{root: w.root, docker: f, storageImage: w.storageImage}
	retry, err := restarted.reserveQuotaProject(context.Background(), quotaTestID)
	if err != nil || retry != first {
		t.Fatal("retry changed reservation", retry, err)
	}
	if err := os.RemoveAll(w.siteDir(quotaTestID)); err != nil {
		t.Fatal(err)
	}
	other := "1111111111111111"
	registrySite(t, restarted, other)
	second, err := restarted.reserveQuotaProject(context.Background(), other)
	if err != nil || second.ProjectID != first.ProjectID+1 {
		t.Fatal("burned identifier reused", second, err)
	}
	if marked, err := w.quotaMarked(other); marked || err != nil {
		t.Fatal("reservation activated quota", err)
	}
}

func TestQuotaRegistryConcurrentWorkersCannotReserveSameID(t *testing.T) {
	w, f := registryWorker(t)
	var wg sync.WaitGroup
	results := make(chan quotaReservation, 12)
	errors := make(chan error, 12)
	for n := 0; n < 12; n++ {
		id := fmt.Sprintf("%016x", n+100)
		registrySite(t, w, id)
		wg.Add(1)
		go func() {
			defer wg.Done()
			other := &worker{root: w.root, docker: f, storageImage: w.storageImage}
			entry, err := other.reserveQuotaProject(context.Background(), id)
			if err != nil {
				errors <- err
			} else {
				results <- entry
			}
		}()
	}
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
	seen := map[uint32]bool{}
	for entry := range results {
		if seen[entry.ProjectID] {
			t.Fatal("duplicate reservation")
		}
		seen[entry.ProjectID] = true
	}
	if len(seen) != 12 {
		t.Fatal("missing reservations", seen)
	}
}

func TestQuotaRegistryMissingCorruptOrAliasedStateFailsClosed(t *testing.T) {
	for _, failure := range []string{"ledger-missing", "directory-missing", "sentinel-missing", "malformed", "duplicate-key", "duplicate-id", "ledger-symlink", "sentinel-invalid"} {
		t.Run(failure, func(t *testing.T) {
			w, _ := registryWorker(t)
			entry, err := w.reserveQuotaProject(context.Background(), quotaTestID)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(w.root, ".quota", "projects.json")
			switch failure {
			case "ledger-missing":
				os.Remove(path)
			case "directory-missing":
				os.RemoveAll(filepath.Dir(path))
			case "sentinel-missing":
				os.Remove(filepath.Join(w.root, ".quota-initialized"))
			case "malformed":
				os.WriteFile(path, []byte("{"), 0600)
			case "duplicate-key":
				os.WriteFile(path, []byte(`{"version":1,"reservations":[],"reservations":[]}`), 0600)
			case "duplicate-id":
				duplicate := entry
				duplicate.SiteID = "1111111111111111"
				body, _ := json.Marshal(quotaLedger{Version: 1, Reservations: []quotaReservation{entry, duplicate}})
				os.WriteFile(path, body, 0600)
			case "ledger-symlink":
				body, _ := os.ReadFile(path)
				target := filepath.Join(t.TempDir(), "outside.json")
				os.WriteFile(target, body, 0600)
				os.Remove(path)
				os.Symlink(target, path)
			case "sentinel-invalid":
				os.WriteFile(filepath.Join(w.root, ".quota-initialized"), []byte("unknown"), 0600)
			}
			if got, err := w.reserveQuotaProject(context.Background(), quotaTestID); err == nil || got.ProjectID != 0 {
				t.Fatal("unsafe metadata accepted", got, err)
			}
		})
	}
}

func TestQuotaRegistryRejectsUnsafeInventoryAndOccupiedRetries(t *testing.T) {
	for _, failure := range []string{"incomplete", "missing-projects", "duplicate-project", "overflow", "overlap", "foreign-project", "filesystem-changed", "occupied-retry"} {
		t.Run(failure, func(t *testing.T) {
			w, f := registryWorker(t)
			entry, err := w.reserveQuotaProject(context.Background(), quotaTestID)
			if err != nil {
				t.Fatal(err)
			}
			var inventory quotaInventory
			json.Unmarshal([]byte(f.inventory), &inventory)
			switch failure {
			case "incomplete":
				inventory.Verified = false
			case "missing-projects":
				inventory.ProjectIDs = nil
			case "duplicate-project":
				inventory.ProjectIDs = []uint32{10, 10}
			case "overflow":
				inventory.ScannedInodes = 1000001
			case "overlap":
				inventory.WordPressInodes = 20
			case "foreign-project":
				inventory.Verified = false
				inventory.Reason = "foreign-project"
			case "filesystem-changed":
				inventory.FilesystemUUID = strings.Repeat("c", 32)
			case "occupied-retry":
				inventory.ProjectIDs = []uint32{entry.ProjectID}
			}
			body, _ := json.Marshal(inventory)
			f.inventory = string(body)
			before, _ := os.ReadFile(filepath.Join(w.root, ".quota", "projects.json"))
			if got, err := w.reserveQuotaProject(context.Background(), quotaTestID); err == nil || got.ProjectID != 0 {
				t.Fatal("unsafe inventory accepted", got, err)
			}
			after, _ := os.ReadFile(filepath.Join(w.root, ".quota", "projects.json"))
			if string(before) != string(after) {
				t.Fatal("failure rewrote ledger")
			}
		})
	}
}

func TestQuotaRegistryIncompleteLedgerCannotRecycleDeletedSite(t *testing.T) {
	for _, body := range []string{`null`, `{}`, `{"reservations":[]}`, `{"version":1}`, `{"version":1,"reservations":null}`} {
		t.Run(body, func(t *testing.T) {
			w, _ := registryWorker(t)
			if _, err := w.reserveQuotaProject(context.Background(), quotaTestID); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(w.siteDir(quotaTestID)); err != nil {
				t.Fatal(err)
			}
			id := "1111111111111111"
			if err := os.MkdirAll(w.siteDir(id), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(w.siteDir(id), "compose.yaml"), []byte("services: {}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(w.root, ".quota", "projects.json")
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			if got, err := w.reserveQuotaProject(context.Background(), id); err == nil || got.ProjectID != 0 {
				t.Fatal("incomplete ledger recycled reservation", got, err)
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != body {
				t.Fatal("rejected ledger was rewritten", err)
			}
		})
	}
}

func TestQuotaRegistryLockHonorsDeadline(t *testing.T) {
	w, _ := registryWorker(t)
	if _, err := w.reserveQuotaProject(context.Background(), quotaTestID); err != nil {
		t.Fatal(err)
	}
	lock, _, err := w.lockQuotaLedger(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if _, err := w.reserveQuotaProject(ctx, quotaTestID); err == nil {
		t.Fatal("lock contention ignored deadline")
	}
}
