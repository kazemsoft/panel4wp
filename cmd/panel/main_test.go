package main

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/kazemsoft/panel4wp/internal/core"
	"github.com/kazemsoft/panel4wp/internal/store"
)

func TestMultipartCSRF(t *testing.T) {
	a := &app{sessionKey: []byte(strings.Repeat("s", 64))}
	payload := strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
	session := payload + "." + a.sign(payload)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("csrf", a.csrf(session)); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("file", "hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("hello"))
	_ = writer.Close()
	req := httptest.NewRequest(http.MethodPost, "/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: "wph_session", Value: session})
	if !a.checkCSRF(req) {
		t.Fatal("valid multipart CSRF token rejected")
	}
}

func TestMoveFileBuildsSafeDestination(t *testing.T) {
	var received core.FileRequest
	workerCalls := 0
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		workerCalls++
		if r.URL.Path != "/files/move" {
			t.Fatalf("unexpected worker path %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer worker.Close()
	a := &app{workerURL: worker.URL, workerToken: strings.Repeat("t", 64), client: worker.Client()}
	site := core.Site{ID: "0123456789abcdef", Status: core.StatusRunning}

	req := httptest.NewRequest(http.MethodPost, "/sites/"+site.ID+"/file-move", strings.NewReader(url.Values{"path": {"images/logo.png"}, "destination": {"archive/2026"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp := httptest.NewRecorder()
	a.fileAction(resp, req, site, "file-move")
	if resp.Code != http.StatusSeeOther || received.Path != "images/logo.png" || received.Destination != "archive/2026/logo.png" {
		t.Fatalf("move failed: status=%d request=%#v", resp.Code, received)
	}

	req = httptest.NewRequest(http.MethodPost, "/sites/"+site.ID+"/file-move", strings.NewReader(url.Values{"path": {"images/logo.png"}, "destination": {"../escape"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp = httptest.NewRecorder()
	a.fileAction(resp, req, site, "file-move")
	if resp.Code != http.StatusBadRequest || workerCalls != 1 {
		t.Fatalf("unsafe move was not rejected: status=%d calls=%d", resp.Code, workerCalls)
	}
}

func TestDatabaseProxyRequiresPanelAuthentication(t *testing.T) {
	a := &app{sessionKey: []byte(strings.Repeat("s", 64))}
	req := httptest.NewRequest(http.MethodGet, "/sites/0123456789abcdef/database/", nil)
	resp := httptest.NewRecorder()
	a.ServeHTTP(resp, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("database proxy bypassed authentication: %d", resp.Code)
	}
}

func TestDatabaseProxyDoesNotForwardPanelCookies(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "wph_session", Value: "secret"})
	req.AddCookie(&http.Cookie{Name: "wph_flash", Value: "result"})
	req.AddCookie(&http.Cookie{Name: "phpMyAdmin", Value: "database-session"})
	removePanelCookies(req)
	if got := req.Header.Get("Cookie"); got != "phpMyAdmin=database-session" {
		t.Fatalf("unexpected proxied cookies: %q", got)
	}
}

func TestLoginCreateAndRejectMissingCSRF(t *testing.T) {
	workerCalls := 0
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/create" || r.Header.Get("X-Worker-Token") != strings.Repeat("t", 64) {
			t.Errorf("unexpected worker request: %s, %s", r.URL.Path, r.Header.Get("X-Worker-Token"))
		}
		workerCalls++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer worker.Close()
	hash, _ := bcrypt.GenerateFromPassword([]byte("correct horse battery staple"), bcrypt.MinCost)
	a := &app{store: store.New(filepath.Join(t.TempDir(), "sites.json")), workerURL: worker.URL, workerToken: strings.Repeat("t", 64), adminHash: hash, sessionKey: []byte(strings.Repeat("s", 64)), client: worker.Client(), loginFails: make(map[string][]time.Time)}
	server := httptest.NewServer(a)
	defer server.Close()
	client := &http.Client{Transport: server.Client().Transport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.PostForm(server.URL+"/login", url.Values{"password": {"correct horse battery staple"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || len(resp.Cookies()) == 0 {
		t.Fatalf("login failed: %d", resp.StatusCode)
	}
	cookie := resp.Cookies()[0]
	getReq, _ := http.NewRequest(http.MethodGet, server.URL+"/", nil)
	getReq.AddCookie(cookie)
	getResp, err := client.Do(getReq)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(getResp.Body)
	getResp.Body.Close()
	csrf := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindSubmatch(page)
	if len(csrf) != 2 {
		t.Fatal("CSRF token not rendered")
	}
	values := url.Values{"title": {"Test site"}, "email": {"owner@example.com"}, "domain": {"example.com"}, "csrf": {string(csrf[1])}}
	createReq, _ := http.NewRequest(http.MethodPost, server.URL+"/sites", strings.NewReader(values.Encode()))
	createReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	createReq.AddCookie(cookie)
	createResp, err := client.Do(createReq)
	if err != nil {
		t.Fatal(err)
	}
	createResp.Body.Close()
	if createResp.StatusCode != http.StatusSeeOther || workerCalls != 1 {
		t.Fatalf("create failed: %d, worker calls %d", createResp.StatusCode, workerCalls)
	}
	var flashCookie *http.Cookie
	for _, candidate := range createResp.Cookies() {
		if candidate.Name == "wph_flash" {
			flashCookie = candidate
		}
	}
	if flashCookie == nil {
		t.Fatal("create result did not set a flash cookie")
	}
	resultReq, _ := http.NewRequest(http.MethodGet, server.URL+"/", nil)
	resultReq.AddCookie(cookie)
	resultReq.AddCookie(flashCookie)
	resultResp, err := client.Do(resultReq)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resultResp.Body)
	resultResp.Body.Close()
	if !strings.Contains(string(body), "Site created") {
		t.Fatalf("one-time creation result missing: %s", body)
	}
	sites, err := a.store.List()
	if err != nil || len(sites) != 1 || sites[0].Domain != "example.com" || sites[0].MemoryMB != 768 || sites[0].CPUs != 1 {
		t.Fatalf("site missing: %#v, %v", sites, err)
	}
	values.Del("csrf")
	badReq, _ := http.NewRequest(http.MethodPost, server.URL+"/sites", strings.NewReader(values.Encode()))
	badReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	badReq.AddCookie(cookie)
	badResp, err := client.Do(badReq)
	if err != nil {
		t.Fatal(err)
	}
	badResp.Body.Close()
	if badResp.StatusCode != http.StatusForbidden || workerCalls != 1 {
		t.Fatalf("missing CSRF passed: %d, calls %d", badResp.StatusCode, workerCalls)
	}
}

func TestScheduledBackupRunsAndPrunesOnlyScheduledCopies(t *testing.T) {
	now := time.Date(2026, 9, 26, 8, 0, 0, 0, time.UTC)
	var deleted []string
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/backup":
			var req core.BackupRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			_ = json.NewEncoder(w).Encode(core.Backup{ID: req.BackupID, CreatedAt: now})
		case "/backup/delete":
			var req core.DeleteBackupRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			deleted = append(deleted, req.BackupID)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer worker.Close()

	dataStore := store.New(filepath.Join(t.TempDir(), "sites.json"))
	site := core.Site{
		ID: "0123456789abcdef", Domain: "demo.localhost", Title: "Demo", AdminEmail: "owner@example.com", Status: core.StatusRunning,
		BackupPolicy: core.BackupPolicy{Enabled: true, IntervalHours: 24, Retention: 2, NextRunAt: now.Add(-time.Minute)},
		Backups: []core.Backup{
			{ID: "20260920T080000Z-11111111", CreatedAt: now.Add(-6 * 24 * time.Hour), Source: core.BackupSourceManual},
			{ID: "20260921T080000Z-22222222", CreatedAt: now.Add(-5 * 24 * time.Hour), Source: core.BackupSourceScheduled},
			{ID: "20260922T080000Z-33333333", CreatedAt: now.Add(-4 * 24 * time.Hour), Source: core.BackupSourceScheduled},
		},
	}
	if err := dataStore.Put(site); err != nil {
		t.Fatal(err)
	}
	a := &app{store: dataStore, workerURL: worker.URL, workerToken: strings.Repeat("t", 64), client: worker.Client()}
	a.runScheduledBackups(now)

	got, ok, err := dataStore.Get(site.ID)
	if err != nil || !ok {
		t.Fatalf("site missing: %v", err)
	}
	if got.BackupPolicy.LastSuccessAt != now || got.BackupPolicy.NextRunAt != now.Add(24*time.Hour) || got.BackupPolicy.LastError != "" {
		t.Fatalf("unexpected policy state: %#v", got.BackupPolicy)
	}
	if scheduledBackupCount(got.Backups) != 2 || len(got.Backups) != 3 {
		t.Fatalf("retention removed the wrong backups: %#v", got.Backups)
	}
	if len(deleted) != 1 || deleted[0] != "20260921T080000Z-22222222" {
		t.Fatalf("unexpected retention deletions: %#v", deleted)
	}
}

func TestScheduledBackupPostponesStoppedSite(t *testing.T) {
	now := time.Date(2026, 9, 26, 8, 0, 0, 0, time.UTC)
	dataStore := store.New(filepath.Join(t.TempDir(), "sites.json"))
	site := core.Site{ID: "0123456789abcdef", Domain: "demo.localhost", Status: core.StatusStopped, BackupPolicy: core.BackupPolicy{Enabled: true, IntervalHours: 12, Retention: 7, NextRunAt: now.Add(-time.Minute)}}
	if err := dataStore.Put(site); err != nil {
		t.Fatal(err)
	}
	a := &app{store: dataStore}
	a.runScheduledBackups(now)
	got, _, _ := dataStore.Get(site.ID)
	if got.BackupPolicy.LastError != "site is not running" || got.BackupPolicy.NextRunAt != now.Add(12*time.Hour) {
		t.Fatalf("stopped site was not postponed: %#v", got.BackupPolicy)
	}
}
