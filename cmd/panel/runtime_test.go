package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kazemsoft/panel4wp/internal/core"
	"github.com/kazemsoft/panel4wp/internal/store"
)

func TestReconciliationAfterPanelRestart(t *testing.T) {
	for _, tc := range []struct {
		name, kind, receiptState string
		status, want             core.Status
		deleted, backup          bool
	}{
		{"external stop", "", "", core.StatusStopped, core.StatusStopped, false, false},
		{"database crash", "", "", core.StatusUnhealthy, core.StatusUnhealthy, false, false},
		{"lost start response", "start", "succeeded", core.StatusRunning, core.StatusRunning, false, false},
		{"lost creation password", "create", "succeeded", core.StatusRunning, core.StatusFailed, false, false},
		{"confirmed deletion", "delete", "succeeded", core.StatusUnhealthy, "", true, false},
		{"interrupted restore", "restore", "interrupted", core.StatusStopped, core.StatusStopped, false, false},
		{"completed backup", "scheduled-backup", "succeeded", core.StatusRunning, core.StatusRunning, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := "0123456789abcdef"
			opID := "1111111111111111"
			snapshot := core.SiteRuntime{Status: tc.status, Detail: "service detail"}
			site := core.Site{ID: id, Status: core.StatusRunning}
			if tc.kind != "" {
				site.Operation = &core.Operation{ID: opID, Kind: tc.kind, State: "pending"}
				snapshot.Operation = &core.Operation{ID: opID, Kind: tc.kind, State: tc.receiptState}
				if tc.backup {
					snapshot.Operation.Backup = &core.Backup{ID: "20261005T120000Z-12345678", CreatedAt: time.Now()}
				}
			}
			dbPath := filepath.Join(t.TempDir(), "sites.json")
			if err := store.New(dbPath).Put(site); err != nil {
				t.Fatal(err)
			}
			worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/runtime" {
					t.Errorf("reconciliation issued mutation %s", r.URL.Path)
				}
				json.NewEncoder(w).Encode(snapshot)
			}))
			defer worker.Close()
			restarted := &app{store: store.New(dbPath), workerURL: worker.URL, client: worker.Client()}
			if err := restarted.reconcileRuntime(context.Background()); err != nil {
				t.Fatal(err)
			}
			got, found, err := restarted.store.Get(id)
			if err != nil {
				t.Fatal(err)
			}
			if tc.deleted {
				if found {
					t.Fatal("confirmed deleted site remained")
				}
				return
			}
			if !found || got.Status != tc.want || got.HealthCheckedAt.IsZero() {
				t.Fatalf("unexpected recovery: %#v", got)
			}
			if tc.kind != "" && got.Operation.State == "pending" {
				t.Fatal("operation left pending")
			}
			if tc.kind == "restore" && !strings.Contains(got.Error, "safety backup") {
				t.Fatal("restore warning missing")
			}
			if tc.backup && (len(got.Backups) != 1 || got.Backups[0].Source != core.BackupSourceScheduled) {
				t.Fatalf("backup recovery failed: %#v", got.Backups)
			}
			// A second poll must not duplicate recovered backups or erase the warning.
			if err := restarted.reconcileRuntime(context.Background()); err != nil {
				t.Fatal(err)
			}
			twice, _, _ := restarted.store.Get(id)
			if len(twice.Backups) != len(got.Backups) || twice.Error != got.Error {
				t.Fatal("recovery was not idempotent")
			}
		})
	}
}

func TestUnavailableAndBusyWorkerDoNotInventSiteState(t *testing.T) {
	for _, code := range []int{http.StatusServiceUnavailable, http.StatusConflict} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }))
			defer worker.Close()
			a := &app{store: store.New(filepath.Join(t.TempDir(), "sites.json")), workerURL: worker.URL, client: worker.Client()}
			site := core.Site{ID: "0123456789abcdef", Status: core.StatusRunning, Operation: &core.Operation{ID: "1111111111111111", State: "pending", Kind: "restore"}}
			a.store.Put(site)
			a.reconcileRuntime(context.Background())
			got, _, _ := a.store.Get(site.ID)
			if got.Status != site.Status || got.Operation.State != "pending" {
				t.Fatal("unknown response changed operation/site state")
			}
			if code == 503 && visibleStatus(got) != "unknown" {
				t.Fatal("unavailable worker displayed as running")
			}
			if code == 409 && got.HealthError != "" {
				t.Fatal("active worker was treated as down")
			}
		})
	}
}

func TestTrackedOperationIsDurableBeforeRequestAndMetadataCommit(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sites.json")
	a := &app{store: store.New(dbPath)}
	site := core.Site{ID: "0123456789abcdef", Status: core.StatusStopped}
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		persisted, _, err := store.New(dbPath).Get(site.ID)
		if err != nil || !operationPending(persisted) || persisted.Operation.ID != r.Header.Get("X-Operation-ID") {
			t.Error("side effect preceded durable marker")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer worker.Close()
	a.workerURL, a.client = worker.URL, worker.Client()
	if err := a.callTracked(&site, "start", "/action", map[string]string{"id": site.ID, "action": "start"}, nil); err != nil {
		t.Fatal(err)
	}
	persisted, _, _ := a.store.Get(site.ID)
	if !operationPending(persisted) {
		t.Fatal("receipt committed before final site metadata")
	}
	site.Status = core.StatusRunning
	a.store.Put(site)
	if operationPending(site) {
		t.Fatal("completed operation remained pending in memory")
	}
}

func TestPendingSiteRejectsDuplicateMutation(t *testing.T) {
	a := &app{store: store.New(filepath.Join(t.TempDir(), "sites.json"))}
	site := core.Site{ID: "0123456789abcdef", Operation: &core.Operation{State: "pending"}}
	a.store.Put(site)
	r := httptest.NewRequest(http.MethodPost, "/sites/"+site.ID+"/stop", nil)
	w := httptest.NewRecorder()
	a.siteAction(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate mutation allowed: %d", w.Code)
	}
}

func TestLoginClientIdentityRequiresTrustedProxy(t *testing.T) {
	key := strings.Repeat("s", 64)
	a := &app{sessionKey: []byte(key), loginFails: make(map[string][]time.Time)}
	request := func(ip, token string) *http.Request {
		r := httptest.NewRequest("POST", "/login", nil)
		r.RemoteAddr = "172.18.0.2:1234"
		r.Header.Set("X-Forwarded-For", ip)
		r.Header.Set("X-Panel-Client-IP", ip)
		r.Header.Set("X-Panel-Proxy-Token", token)
		return r
	}
	if got := a.loginClientIP(request("192.0.2.1", "forged")); got != "172.18.0.2" {
		t.Fatalf("forged identity accepted: %s", got)
	}
	ipA := a.loginClientIP(request("192.0.2.1", key))
	ipB := a.loginClientIP(request("192.0.2.2", key))
	for i := 0; i < 8; i++ {
		if !a.loginAllowed(ipA) {
			t.Fatal("early limit")
		}
	}
	if a.loginAllowed(ipA) || !a.loginAllowed(ipB) {
		t.Fatal("clients behind proxy share a limit")
	}
}

func TestRestoreFromStoppedSiteDoesNotStartPublicServiceBeforeSuccess(t *testing.T) {
	site := core.Site{ID: "0123456789abcdef", Domain: "test.localhost", Status: core.StatusStopped, Backups: []core.Backup{{ID: "20261005T120000Z-12345678"}}}
	calls := []string{}
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path)
		switch r.URL.Path {
		case "/action":
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["action"] != "prepare-restore" {
				t.Error("started public WordPress before restore")
			}
			w.WriteHeader(204)
		case "/backup":
			var body core.BackupRequest
			json.NewDecoder(r.Body).Decode(&body)
			json.NewEncoder(w).Encode(core.Backup{ID: body.BackupID, CreatedAt: time.Now()})
		case "/restore":
			w.WriteHeader(204)
		default:
			t.Error("unexpected mutation")
		}
	}))
	defer worker.Close()
	a := &app{store: store.New(filepath.Join(t.TempDir(), "sites.json")), workerURL: worker.URL, client: worker.Client()}
	a.store.Put(site)
	r := httptest.NewRequest("POST", "/sites/"+site.ID+"/restore", strings.NewReader("confirm=test.localhost&backup_id=20261005T120000Z-12345678"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp := httptest.NewRecorder()
	a.restoreSite(resp, r, site)
	got, _, _ := a.store.Get(site.ID)
	if resp.Code != 303 || got.Status != core.StatusRunning || len(got.Backups) != 2 || strings.Join(calls, ",") != "/action,/backup,/restore" {
		t.Fatalf("unsafe restore flow: %d %#v %v", resp.Code, got, calls)
	}
}
