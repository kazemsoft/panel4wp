package main

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kazemsoft/panel4wp/internal/core"
	"github.com/kazemsoft/panel4wp/internal/store"
)

func TestCustomResourceFormValidation(t *testing.T) {
	for _, cpu := range []string{"NaN", "Inf", "0", "0.3", "9"} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(url.Values{"plan": {"custom"}, "memory_mb": {"512"}, "cpus": {cpu}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if _, err := formResources(r); err == nil {
			t.Fatal("invalid CPU accepted", cpu)
		}
	}
	r := httptest.NewRequest("POST", "/", strings.NewReader("plan=custom&memory_mb=640&cpus=0.75"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	got, err := formResources(r)
	if err != nil || got.MemoryMB != 640 || math.Abs(got.CPUs-.75) > .00001 {
		t.Fatal(got, err)
	}
}

func TestResourceMutationRequiresAuthCSRFAndCommitsAppliedLimits(t *testing.T) {
	calls := 0
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body core.ResourceRequest
		json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path != "/resources" || r.Header.Get("X-Operation-ID") == "" {
			t.Error("untracked resource request")
		}
		json.NewEncoder(w).Encode(body.Resources)
	}))
	defer worker.Close()
	a := &app{store: store.New(filepath.Join(t.TempDir(), "sites.json")), sessionKey: []byte(strings.Repeat("s", 64)), workerURL: worker.URL, workerToken: strings.Repeat("t", 64), client: worker.Client()}
	a.store.PutResourcePlan(core.ResourcePlan{ID: "2222222222222222", Name: "Reusable plan", Resources: core.Resources{MemoryMB: 640, CPUs: .75}})
	site := core.Site{ID: "0123456789abcdef", Domain: "one.localhost", Title: "One", AdminEmail: "test@example.com", Status: core.StatusStopped, MemoryMB: 512, CPUs: 1}
	a.store.Put(site)
	payload := strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
	session := payload + "." + a.sign(payload)
	run := func(auth, csrf bool) *httptest.ResponseRecorder {
		fields := url.Values{"plan": {"saved-2222222222222222"}, "memory_mb": {"640"}, "cpus": {"0.75"}}
		if csrf {
			fields.Set("csrf", a.csrf(session))
		}
		r := httptest.NewRequest("POST", "/sites/"+site.ID+"/resources", strings.NewReader(fields.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if auth {
			r.AddCookie(&http.Cookie{Name: "wph_session", Value: session})
		}
		resp := httptest.NewRecorder()
		a.ServeHTTP(resp, r)
		return resp
	}
	if run(false, true).Code != 401 || run(true, false).Code != 403 || calls != 0 {
		t.Fatal("unauthorized mutation reached worker")
	}
	if got := run(true, true); got.Code != 303 {
		t.Fatal(got.Code, got.Body.String())
	}
	saved, _, _ := a.store.Get(site.ID)
	if calls != 1 || saved.MemoryMB != 640 || saved.CPUs != .75 || saved.Status != core.StatusStopped || saved.Operation.State != "succeeded" {
		t.Fatal("resource result not committed", saved, calls)
	}
}

func TestLostResourceResponseRecoversConfiguredLimits(t *testing.T) {
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(core.SiteRuntime{Status: core.StatusStopped, Resources: &core.Resources{MemoryMB: 640, CPUs: .75}, Operation: &core.Operation{ID: "1111111111111111", Kind: "resources", State: "interrupted"}})
	}))
	defer worker.Close()
	db := store.New(filepath.Join(t.TempDir(), "sites.json"))
	db.Put(core.Site{ID: "0123456789abcdef", MemoryMB: 512, CPUs: 1, Status: core.StatusRunning, Operation: &core.Operation{ID: "1111111111111111", Kind: "resources", State: "pending"}})
	a := &app{store: db, workerURL: worker.URL, workerToken: strings.Repeat("t", 64), client: worker.Client()}
	if err := a.reconcileRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
	site, _, _ := db.Get("0123456789abcdef")
	if operationPending(site) || site.Status != core.StatusStopped || site.MemoryMB != 640 || site.CPUs != .75 || site.Error == "" {
		t.Fatal("lost resource response not reconciled", site)
	}
}

func TestConcurrentResourceSubmissionIsRejectedInsteadOfQueued(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		json.NewEncoder(w).Encode(core.Resources{MemoryMB: 640, CPUs: .75})
	}))
	defer worker.Close()
	a := &app{store: store.New(filepath.Join(t.TempDir(), "sites.json")), workerURL: worker.URL, workerToken: strings.Repeat("t", 64), client: worker.Client()}
	a.store.Put(core.Site{ID: "0123456789abcdef", Status: core.StatusRunning, MemoryMB: 512, CPUs: 1})
	run := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/sites/0123456789abcdef/resources", strings.NewReader("plan=custom&memory_mb=640&cpus=0.75"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp := httptest.NewRecorder()
		a.siteAction(resp, r)
		return resp
	}
	first := make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- run() }()
	<-entered
	second := make(chan *httptest.ResponseRecorder, 1)
	go func() { second <- run() }()
	select {
	case resp := <-second:
		if resp.Code != 409 {
			t.Error("concurrent mutation not rejected", resp.Code)
		}
	case <-time.After(time.Second):
		t.Error("concurrent submission was queued")
	}
	close(release)
	if resp := <-first; resp.Code != 303 {
		t.Fatal("first operation failed", resp.Code)
	}
}
