package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/example/wp-host-panel/internal/store"
)

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
	body, _ := io.ReadAll(createResp.Body)
	createResp.Body.Close()
	if createResp.StatusCode != http.StatusOK || !strings.Contains(string(body), "Site created") || workerCalls != 1 {
		t.Fatalf("create failed: %d, %s, worker calls %d", createResp.StatusCode, body, workerCalls)
	}
	sites, err := a.store.List()
	if err != nil || len(sites) != 1 || sites[0].Domain != "example.com" {
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
