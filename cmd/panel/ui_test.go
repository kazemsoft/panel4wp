package main

import (
	"bytes"
	"context"
	"github.com/kazemsoft/panel4wp/internal/core"
	"github.com/kazemsoft/panel4wp/internal/store"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestIndependentPagesPreserveActions(t *testing.T) {
	site := core.Site{ID: "abc123", Domain: "demo.localhost", Title: `<script>alert(1)</script>`, Status: core.StatusRunning, Backups: []core.Backup{{ID: "backup123"}}}
	for pageName, actions := range map[string][]string{"dashboard": {}, "site": {"stop", "delete"}, "backups": {"backup", "restore", "backup-delete"}, "updates": {"update"}, "database": {"database-start"}} {
		t.Run(pageName, func(t *testing.T) {
			var output bytes.Buffer
			if err := page(view{LoggedIn: true, Page: pageName, CSRF: "csrf-test", Sites: []core.Site{site}, Language: "en", Direction: "ltr"}).Render(context.Background(), &output); err != nil {
				t.Fatal(err)
			}
			html := output.String()
			for _, action := range actions {
				if !strings.Contains(html, `action="/sites/abc123/`+action+`"`) {
					t.Errorf("missing action %s", action)
				}
			}
			if strings.Contains(html, site.Title) {
				t.Fatal("unescaped title")
			}
			if pageName == "dashboard" {
				for _, forbidden := range []string{`action="/sites/`, `id="settings"`, `id="activity"`, `id="create-site"`} {
					if strings.Contains(html, forbidden) {
						t.Errorf("dashboard contains %s", forbidden)
					}
				}
				if !strings.Contains(html, `href="/sites/abc123"`) {
					t.Error("missing manage link")
				}
			}
		})
	}
}

func TestSidebarHighlightsCurrentPageAndStartsContentAtTop(t *testing.T) {
	tests := map[string]string{
		"dashboard": `class="nav-link active" href="/"`,
		"new":       `class="nav-link active" href="/"`,
		"site":      `class="nav-link active" href="/"`,
		"settings":  `class="nav-link active" href="/settings"`,
		"activity":  `class="nav-link active" href="/activity"`,
		"roadmap":   `class="nav-link roadmap-nav active" href="/roadmap"`,
	}
	for pageName, activeLink := range tests {
		t.Run(pageName, func(t *testing.T) {
			var output bytes.Buffer
			if err := page(view{LoggedIn: true, Page: pageName, Language: "en", Direction: "ltr"}).Render(context.Background(), &output); err != nil {
				t.Fatal(err)
			}
			html := output.String()
			if !strings.Contains(html, activeLink) {
				t.Fatalf("missing active navigation link %q", activeLink)
			}
			for _, required := range []string{`data-sidebar-toggle`, `class="brand-mark"`, `icon-settings`, `/assets/app.js`} {
				if !strings.Contains(html, required) {
					t.Errorf("missing shell element %q", required)
				}
			}
			if !strings.Contains(html, `href="https://github.com/kazemsoft/panel4wp"`) || !strings.Contains(html, "Star on GitHub") {
				t.Fatal("GitHub support callout is missing")
			}
		})
	}
}

func TestFileManagerConfirmsDeletionAndPreservesOperations(t *testing.T) {
	var output bytes.Buffer
	v := filesView{Site: core.Site{ID: "abc123", Domain: "demo.localhost"}, CSRF: "csrf-test", Entries: []fileEntryView{{FileEntry: core.FileEntry{Name: "test.txt", Type: "file"}, Path: "test.txt"}}}
	v.Language, v.Direction = "en", "ltr"
	if err := filesPage(v).Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{`action="/sites/abc123/download"`, `action="/sites/abc123/file-delete"`, `action="/sites/abc123/upload"`, `action="/sites/abc123/mkdir"`, `Confirm delete`, `value="csrf-test"`} {
		if !strings.Contains(output.String(), required) {
			t.Errorf("missing UI element %s", required)
		}
	}
}

func TestWorkspaceRoutes(t *testing.T) {
	a := &app{store: store.New(filepath.Join(t.TempDir(), "sites.json")), sessionKey: []byte(strings.Repeat("s", 64))}
	if err := a.store.Put(core.Site{ID: "abc123", Domain: "demo.localhost", Title: "Demo", Status: core.StatusRunning}); err != nil {
		t.Fatal(err)
	}
	payload := strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
	for _, route := range []string{"/sites/new", "/settings", "/activity", "/sites/abc123", "/sites/abc123/backups", "/sites/abc123/updates", "/sites/abc123/database"} {
		t.Run(route, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, route, nil)
			w := httptest.NewRecorder()
			a.ServeHTTP(w, r)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("unauthenticated route: %d", w.Code)
			}
			r = httptest.NewRequest(http.MethodGet, route, nil)
			r.AddCookie(&http.Cookie{Name: "wph_session", Value: payload + "." + a.sign(payload)})
			w = httptest.NewRecorder()
			a.ServeHTTP(w, r)
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "</html>") {
				t.Fatalf("route failed: %d", w.Code)
			}
		})
	}
}

func TestBrowserLanguageAssetsAndExplicitSelection(t *testing.T) {
	a := &app{store: store.New(filepath.Join(t.TempDir(), "sites.json")), sessionKey: []byte(strings.Repeat("s", 64))}

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Accept-Language", "fa-IR,fa;q=0.9,en;q=0.8")
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `<html lang="fa" dir="rtl">`) || !strings.Contains(w.Body.String(), `action="/language"`) {
		t.Fatalf("browser language was not rendered: %d", w.Code)
	}

	r = httptest.NewRequest(http.MethodGet, "/language?lang=ja&next=%2Fsettings", nil)
	w = httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/settings" {
		t.Fatalf("language redirect failed: %d %q", w.Code, w.Header().Get("Location"))
	}
	if cookies := w.Result().Cookies(); len(cookies) != 1 || cookies[0].Value != "ja" {
		t.Fatalf("language cookie missing: %#v", cookies)
	}

	for _, asset := range []string{"/assets/app.css", "/assets/app.js", "/assets/htmx.min.js", "/assets/fonts/vazirmatn-arabic-wght-normal.woff2"} {
		r = httptest.NewRequest(http.MethodGet, asset, nil)
		w = httptest.NewRecorder()
		a.ServeHTTP(w, r)
		if w.Code != http.StatusOK || w.Body.Len() == 0 || w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("asset %s failed: %d", asset, w.Code)
		}
	}
}

func TestLanguageRedirectRejectsExternalDestination(t *testing.T) {
	for _, raw := range []string{"https://example.com", "//example.com/path", ""} {
		if got := safeLanguageNext(raw); got != "/" {
			t.Errorf("safeLanguageNext(%q)=%q", raw, got)
		}
	}
}
