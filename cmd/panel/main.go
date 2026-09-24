package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/kazemsoft/panel4wp/internal/audit"
	"github.com/kazemsoft/panel4wp/internal/core"
	"github.com/kazemsoft/panel4wp/internal/i18n"
	"github.com/kazemsoft/panel4wp/internal/settings"
	"github.com/kazemsoft/panel4wp/internal/store"
	frontend "github.com/kazemsoft/panel4wp/ui/assets"
)

type languageOption = i18n.Language

var assetHandler = http.StripPrefix("/assets/", http.FileServer(http.FS(frontend.Files)))

type fileEntryView struct {
	core.FileEntry
	Path string
}

type filesView struct {
	Site        core.Site
	CSRF        string
	CurrentPath string
	Parent      string
	HasParent   bool
	Entries     []fileEntryView
	Error       string
	Message     string
	Language    string
	Direction   string
	Languages   []languageOption
	PagePath    string
}

func (v filesView) T(key string) string { return i18n.T(v.Language, key) }

type view struct {
	Page            string
	PageTitle       string
	Guide           string
	SelectedID      string
	LoggedIn        bool
	CSRF            string
	Error           string
	Message         string
	Sites           []core.Site
	Stats           map[string]core.SiteStats
	StatsError      string
	Storage         map[string]core.SiteStorage
	Mail            settings.Mail
	MailPasswordSet bool
	Activity        []audit.Entry
	Language        string
	Direction       string
	Languages       []languageOption
	CurrentPath     string
}

func (v view) T(key string) string { return i18n.T(v.Language, key) }

func formatTime(t time.Time) string  { return t.Local().Format("2006-01-02 15:04") }
func formatUnixTime(ts int64) string { return formatTime(time.Unix(ts, 0)) }
func formatBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	if n < 1024*1024 {
		return fmt.Sprintf("%.1f KiB", float64(n)/1024)
	}
	if n < 1024*1024*1024 {
		return fmt.Sprintf("%.1f MiB", float64(n)/(1024*1024))
	}
	return fmt.Sprintf("%.2f GiB", float64(n)/(1024*1024*1024))
}
func isSitePage(pageName string) bool {
	switch pageName {
	case "dashboard", "new", "site", "backups", "updates", "database", "files":
		return true
	default:
		return false
	}
}
func sitePublicURL(domain string) string {
	if strings.HasSuffix(domain, ".localhost") {
		return "http://" + domain
	}
	return "https://" + domain
}
func mailAction(enabled bool) string {
	if enabled {
		return "/mail-disable"
	}
	return "/mail-enable"
}
func portValue(port int) string {
	if port == 0 {
		return ""
	}
	return strconv.Itoa(port)
}
func passwordPlaceholder(v view) string {
	if v.MailPasswordSet {
		return v.T("keep_password")
	}
	return v.T("smtp_password")
}

type app struct {
	store        *store.Store
	settings     *settings.Store
	audit        *audit.Log
	workerURL    string
	workerToken  string
	adminHash    []byte
	sessionKey   []byte
	secureCookie bool
	client       *http.Client
	opsMu        sync.Mutex
	loginMu      sync.Mutex
	loginFails   map[string][]time.Time
	flashMu      sync.Mutex
	flashes      map[string]flash
}

type flash struct {
	Message string
	Error   string
	Created time.Time
}

func (a *app) setFlash(w http.ResponseWriter, message, flashError string) error {
	id, err := core.NewID()
	if err != nil {
		return err
	}
	a.flashMu.Lock()
	if a.flashes == nil {
		a.flashes = make(map[string]flash)
	}
	cutoff := time.Now().Add(-5 * time.Minute)
	for key, item := range a.flashes {
		if item.Created.Before(cutoff) {
			delete(a.flashes, key)
		}
	}
	a.flashes[id] = flash{Message: message, Error: flashError, Created: time.Now()}
	a.flashMu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "wph_flash", Value: id, Path: "/", HttpOnly: true, Secure: a.secureCookie, SameSite: http.SameSiteStrictMode, MaxAge: 120})
	return nil
}

func (a *app) takeFlash(w http.ResponseWriter, r *http.Request) flash {
	cookie, err := r.Cookie("wph_flash")
	if err != nil {
		return flash{}
	}
	a.flashMu.Lock()
	item := a.flashes[cookie.Value]
	delete(a.flashes, cookie.Value)
	a.flashMu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "wph_flash", Path: "/", HttpOnly: true, Secure: a.secureCookie, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	return item
}

func (a *app) redirectWithFlash(w http.ResponseWriter, r *http.Request, message, flashError string) {
	a.redirectWithFlashTo(w, r, "/", message, flashError)
}

func (a *app) redirectWithFlashTo(w http.ResponseWriter, r *http.Request, target, message, flashError string) {
	if err := a.setFlash(w, message, flashError); err != nil {
		http.Error(w, "unable to create result message", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (a *app) render(w http.ResponseWriter, r *http.Request, v view) {
	if v.Page == "" {
		v.Page = "dashboard"
	}
	v.Language = i18n.Resolve(r)
	v.Direction = i18n.Direction(v.Language)
	v.Languages = i18n.Languages()
	v.CurrentPath = r.URL.RequestURI()
	headings := map[string][2]string{
		"dashboard": {"dashboard_title", "dashboard_guide"},
		"new":       {"new_title", "new_guide"},
		"settings":  {"settings_title", "settings_guide"},
		"activity":  {"activity_title", "activity_guide"},
		"site":      {"site_title_page", "site_guide"},
		"backups":   {"backups_title", "backups_guide"},
		"updates":   {"updates_title", "updates_guide"},
		"database":  {"database_title", "database_guide"},
		"roadmap":   {"roadmap_title", "roadmap_guide"},
	}
	v.PageTitle, v.Guide = i18n.T(v.Language, headings[v.Page][0]), i18n.T(v.Language, headings[v.Page][1])
	if v.LoggedIn {
		sites, err := a.store.List()
		if err != nil {
			http.Error(w, "unable to read sites", http.StatusInternalServerError)
			return
		}
		v.Sites = sites
		if v.SelectedID != "" {
			v.Sites = nil
			for _, site := range sites {
				if site.ID == v.SelectedID {
					v.Sites = append(v.Sites, site)
				}
			}
			if len(v.Sites) == 0 {
				http.NotFound(w, r)
				return
			}
			sites = v.Sites
		}
		if a.settings != nil {
			if mail, loadErr := a.settings.Load(); loadErr == nil {
				v.Mail, v.MailPasswordSet = mail, mail.Password != ""
			}
		}
		if a.audit != nil {
			v.Activity, _ = a.audit.List(20)
		}
		if r.URL.Query().Get("stats") == "1" && len(sites) > 0 {
			ids := make([]string, 0, len(sites))
			for _, site := range sites {
				if site.Status == core.StatusRunning {
					ids = append(ids, site.ID)
				}
			}
			if len(ids) > 0 {
				v.Stats = make(map[string]core.SiteStats, len(ids))
				for start := 0; start < len(ids); start += 100 {
					end := min(start+100, len(ids))
					var batch map[string]core.SiteStats
					if err := a.callWorker("/stats", core.StatsRequest{SiteIDs: ids[start:end]}, &batch); err != nil {
						v.StatsError = "Unable to load live usage: " + err.Error()
						break
					}
					for id, stats := range batch {
						v.Stats[id] = stats
					}
				}
				v.Storage = make(map[string]core.SiteStorage, len(ids))
				for start := 0; start < len(ids); start += 50 {
					end := min(start+50, len(ids))
					var batch map[string]core.SiteStorage
					if err := a.callWorker("/storage", core.StorageRequest{SiteIDs: ids[start:end]}, &batch); err != nil {
						v.StatsError = "Unable to load storage usage: " + err.Error()
						break
					}
					for id, usage := range batch {
						v.Storage[id] = usage
					}
				}
			}
		}
		if c, err := r.Cookie("wph_session"); err == nil {
			v.CSRF = a.csrf(c.Value)
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	if err := page(v).Render(r.Context(), w); err != nil {
		log.Printf("page render failed: %v", err)
	}
}

func (a *app) sign(payload string) string {
	h := hmac.New(sha256.New, a.sessionKey)
	h.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

func (a *app) csrf(session string) string { return a.sign("csrf:" + session) }

func (a *app) authenticated(r *http.Request) bool {
	c, err := r.Cookie("wph_session")
	if err != nil {
		return false
	}
	parts := strings.Split(c.Value, ".")
	if len(parts) != 2 {
		return false
	}
	expires, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || time.Now().Unix() >= expires {
		return false
	}
	return hmac.Equal([]byte(a.sign(parts[0])), []byte(parts[1]))
}

func (a *app) checkCSRF(r *http.Request) bool {
	c, err := r.Cookie("wph_session")
	if err != nil {
		return false
	}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		err = r.ParseMultipartForm(1 << 20)
	} else {
		err = r.ParseForm()
	}
	if err != nil {
		return false
	}
	return hmac.Equal([]byte(r.FormValue("csrf")), []byte(a.csrf(c.Value)))
}

func (a *app) loginAllowed(ip string) bool {
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	cutoff := time.Now().Add(-10 * time.Minute)
	old := a.loginFails[ip]
	kept := old[:0]
	for _, t := range old {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	a.loginFails[ip] = kept
	return len(kept) < 8
}

func (a *app) recordFailure(ip string) {
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	a.loginFails[ip] = append(a.loginFails[ip], time.Now())
}

func (a *app) callWorker(path string, body, result any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, a.workerURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Worker-Token", a.workerToken)
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	expected := http.StatusNoContent
	if result != nil {
		expected = http.StatusOK
	}
	if resp.StatusCode != expected {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 2000))
		return fmt.Errorf("worker: %s", strings.TrimSpace(string(message)))
	}
	if result != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(result)
	}
	return nil
}

func removePanelCookies(req *http.Request) {
	cookies := req.Cookies()
	req.Header.Del("Cookie")
	for _, cookie := range cookies {
		if cookie.Name != "wph_session" && cookie.Name != "wph_flash" && cookie.Name != i18n.CookieName {
			req.AddCookie(cookie)
		}
	}
}

func (a *app) proxyDatabase(w http.ResponseWriter, r *http.Request) {
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/sites/"), "/", 3)
	if len(parts) != 3 || !core.ValidID(parts[0]) || parts[1] != "database" {
		http.NotFound(w, r)
		return
	}
	site, ok, err := a.store.Get(parts[0])
	if err != nil || !ok || site.Status != core.StatusRunning {
		http.NotFound(w, r)
		return
	}
	prefix := "/sites/" + site.ID + "/database"
	target := &url.URL{Scheme: "http", Host: "wph-pma-" + site.ID}
	proxy := httputil.NewSingleHostReverseProxy(target)
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		removePanelCookies(req)
		req.URL.Path = strings.TrimPrefix(req.URL.Path, prefix)
		if req.URL.Path == "" {
			req.URL.Path = "/"
		}
		req.URL.RawPath = ""
		req.Host = target.Host
		req.Header.Set("X-Forwarded-Prefix", prefix)
	}
	proxy.ErrorHandler = func(resp http.ResponseWriter, _ *http.Request, proxyErr error) {
		log.Printf("database proxy for %s failed: %v", site.ID, proxyErr)
		http.Error(resp, "database manager is not running; open it again from the Sites page", http.StatusBadGateway)
	}
	w.Header().Set("Cache-Control", "no-store")
	proxy.ServeHTTP(w, r)
}

func safeLanguageNext(raw string) string {
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return "/"
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.IsAbs() || parsed.Host != "" {
		return "/"
	}
	return parsed.RequestURI()
}

func (a *app) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/health" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/assets/") {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		assetHandler.ServeHTTP(w, r)
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/language" {
		if !i18n.SetCookie(w, r.URL.Query().Get("lang"), a.secureCookie) {
			http.Error(w, "unsupported language", http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, safeLanguageNext(r.URL.Query().Get("next")), http.StatusSeeOther)
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/" {
		item := a.takeFlash(w, r)
		a.render(w, r, view{LoggedIn: a.authenticated(r), Message: item.Message, Error: item.Error})
		return
	}
	if r.Method == http.MethodPost && r.URL.Path == "/login" {
		ip, _, _ := net.SplitHostPort(r.RemoteAddr)
		if !a.loginAllowed(ip) {
			http.Error(w, "too many attempts", http.StatusTooManyRequests)
			return
		}
		if r.ParseForm() != nil || bcrypt.CompareHashAndPassword(a.adminHash, []byte(r.FormValue("password"))) != nil {
			a.recordFailure(ip)
			a.render(w, r, view{Error: i18n.T(i18n.Resolve(r), "invalid_password")})
			return
		}
		expiry := time.Now().Add(8 * time.Hour)
		payload := strconv.FormatInt(expiry.Unix(), 10)
		http.SetCookie(w, &http.Cookie{Name: "wph_session", Value: payload + "." + a.sign(payload), Path: "/", HttpOnly: true, Secure: a.secureCookie, SameSite: http.SameSiteStrictMode, Expires: expiry})
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if !a.authenticated(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if strings.Contains(r.URL.Path, "/database/") && strings.HasPrefix(r.URL.Path, "/sites/") {
		a.proxyDatabase(w, r)
		return
	}
	if r.Method == http.MethodGet {
		pageName := map[string]string{"/sites/new": "new", "/settings": "settings", "/activity": "activity", "/roadmap": "roadmap"}[r.URL.Path]
		selectedID := ""
		if pageName == "" && strings.HasPrefix(r.URL.Path, "/sites/") {
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/sites/"), "/")
			if len(parts) == 1 {
				pageName, selectedID = "site", parts[0]
			}
			if len(parts) == 2 && (parts[1] == "backups" || parts[1] == "updates" || parts[1] == "database") {
				pageName, selectedID = parts[1], parts[0]
			}
		}
		if pageName != "" {
			item := a.takeFlash(w, r)
			a.render(w, r, view{LoggedIn: true, Page: pageName, SelectedID: selectedID, Message: item.Message, Error: item.Error})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/sites/") && strings.HasSuffix(r.URL.Path, "/files") {
			a.showFiles(w, r)
			return
		}
		http.NotFound(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/upload") {
		r.Body = http.MaxBytesReader(w, r.Body, 11<<20)
	}
	if r.Method != http.MethodPost || !a.checkCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if r.URL.Path == "/logout" {
		http.SetCookie(w, &http.Cookie{Name: "wph_session", Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.secureCookie})
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if r.URL.Path == "/sites" {
		a.createSite(w, r)
		return
	}
	if r.URL.Path == "/settings/mail" {
		a.saveMailSettings(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/sites/") {
		a.siteAction(w, r)
		return
	}
	http.NotFound(w, r)
}

func (a *app) createSite(w http.ResponseWriter, r *http.Request) {
	a.opsMu.Lock()
	defer a.opsMu.Unlock()
	id, err := core.NewID()
	if err != nil {
		a.render(w, r, view{LoggedIn: true, Error: "Unable to generate site ID"})
		return
	}
	domain := r.FormValue("domain")
	if strings.TrimSpace(domain) == "" {
		domain = id + ".localhost"
	}
	domain, err = core.NormalizeDomain(domain)
	if err != nil {
		a.render(w, r, view{LoggedIn: true, Error: err.Error()})
		return
	}
	memory, cpus := 768, 1.0
	switch r.FormValue("plan") {
	case "small":
		memory, cpus = 384, 0.5
	case "", "standard":
	case "large":
		memory, cpus = 1536, 2
	default:
		a.render(w, r, view{LoggedIn: true, Error: "Invalid resource plan"})
		return
	}
	site := core.Site{ID: id, Domain: domain, Title: strings.TrimSpace(r.FormValue("title")), AdminEmail: strings.TrimSpace(r.FormValue("email")), MemoryMB: memory, CPUs: cpus, Status: core.StatusCreating, CreatedAt: time.Now().UTC()}
	if err := core.ValidateSite(site); err != nil {
		a.render(w, r, view{LoggedIn: true, Error: err.Error()})
		return
	}
	sites, err := a.store.List()
	if err != nil {
		http.Error(w, "unable to read sites", http.StatusInternalServerError)
		return
	}
	for _, existing := range sites {
		if existing.Domain == domain {
			a.render(w, r, view{LoggedIn: true, Error: "Domain already belongs to a site"})
			return
		}
	}
	dbPassword, err := core.RandomPassword()
	if err != nil {
		http.Error(w, "unable to create credentials", http.StatusInternalServerError)
		return
	}
	adminPassword, err := core.RandomPassword()
	if err != nil {
		http.Error(w, "unable to create credentials", http.StatusInternalServerError)
		return
	}
	if err := a.store.Put(site); err != nil {
		http.Error(w, "unable to save site", http.StatusInternalServerError)
		return
	}
	if err := a.callWorker("/create", core.CreateRequest{Site: site, DBPassword: dbPassword, AdminPassword: adminPassword}, nil); err != nil {
		site.Status, site.Error = core.StatusFailed, err.Error()
		_ = a.store.Put(site)
		a.render(w, r, view{LoggedIn: true, Error: "Site creation failed: " + err.Error()})
		a.record("create", site, false, err.Error())
		return
	}
	site.Status = core.StatusRunning
	if err := a.store.Put(site); err != nil {
		http.Error(w, "site created but status could not be saved", http.StatusInternalServerError)
		return
	}
	message := "Site created. WordPress username: admin. Password (save now): " + adminPassword
	a.record("create", site, true, "")
	a.redirectWithFlash(w, r, message, "")
}

func (a *app) siteAction(w http.ResponseWriter, r *http.Request) {
	a.opsMu.Lock()
	defer a.opsMu.Unlock()
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/sites/"), "/")
	if len(parts) != 2 || !core.ValidID(parts[0]) {
		http.NotFound(w, r)
		return
	}
	id, action := parts[0], parts[1]
	site, ok, err := a.store.Get(id)
	if err != nil || !ok {
		http.NotFound(w, r)
		return
	}
	if action == "upload" || action == "download" || action == "mkdir" || action == "file-delete" {
		a.fileAction(w, r, site, action)
		return
	}
	if action == "database-start" || action == "database-stop" {
		dbAction := strings.TrimPrefix(action, "database-")
		if err := a.callWorker("/database", map[string]string{"id": id, "action": dbAction}, nil); err != nil {
			a.record(action, site, false, err.Error())
			a.render(w, r, view{LoggedIn: true, Error: "Database manager: " + err.Error()})
			return
		}
		a.record(action, site, true, "")
		if dbAction == "start" {
			http.Redirect(w, r, "/sites/"+id+"/database/", http.StatusSeeOther)
		} else {
			a.redirectWithFlashTo(w, r, "/sites/"+site.ID+"/database", "Database manager stopped", "")
		}
		return
	}
	if action == "mail-enable" || action == "mail-disable" {
		enabled := action == "mail-enable"
		if err := a.applySiteMail(site, enabled); err != nil {
			a.record(action, site, false, err.Error())
			a.render(w, r, view{LoggedIn: true, Error: "Mail settings: " + err.Error()})
			return
		}
		site.MailEnabled = enabled
		_ = a.store.Put(site)
		a.record(action, site, true, "")
		a.redirectWithFlashTo(w, r, "/sites/"+site.ID, "Outgoing mail settings updated for "+site.Domain, "")
		return
	}
	if action == "delete" && r.FormValue("confirm") != site.Domain {
		a.render(w, r, view{LoggedIn: true, Error: "Domain confirmation did not match"})
		return
	}
	if action == "backup" {
		a.backupSite(w, r, site)
		return
	}
	if action == "backup-delete" {
		a.deleteBackup(w, r, site)
		return
	}
	if action == "update" {
		a.updateSite(w, r, site)
		return
	}
	if action == "restore" {
		a.restoreSite(w, r, site)
		return
	}
	if action != "start" && action != "stop" && action != "delete" && action != "retry" {
		http.NotFound(w, r)
		return
	}
	if action == "retry" {
		if site.Status != core.StatusFailed {
			http.Error(w, "not a failed site", http.StatusConflict)
			return
		}
		dbPassword, _ := core.RandomPassword()
		adminPassword, _ := core.RandomPassword()
		site.Status, site.Error = core.StatusCreating, ""
		_ = a.store.Put(site)
		err = a.callWorker("/create", core.CreateRequest{Site: site, DBPassword: dbPassword, AdminPassword: adminPassword}, nil)
		if err == nil {
			site.Status = core.StatusRunning
			_ = a.store.Put(site)
			a.record("retry", site, true, "")
			a.redirectWithFlashTo(w, r, "/sites/"+site.ID, "Site creation retried. WordPress username: admin. New password (save now): "+adminPassword, "")
			return
		}
	} else {
		if action == "delete" {
			site.Status = core.StatusDeleting
			_ = a.store.Put(site)
		}
		err = a.callWorker("/action", map[string]string{"id": id, "action": action}, nil)
		if err == nil {
			if action == "delete" {
				_ = a.store.Delete(id)
			} else if action == "stop" {
				site.Status = core.StatusStopped
				_ = a.store.Put(site)
			} else {
				site.Status = core.StatusRunning
				_ = a.store.Put(site)
			}
			if action == "start" && site.MailEnabled {
				if mailErr := a.applySiteMail(site, true); mailErr != nil {
					a.record("mail-apply", site, false, mailErr.Error())
				}
			}
			a.record(action, site, true, "")
			destination := "/sites/" + site.ID
			if action == "delete" {
				destination = "/"
			}
			a.redirectWithFlashTo(w, r, destination, "Site action completed", "")
			return
		}
	}
	site.Status, site.Error = core.StatusFailed, err.Error()
	_ = a.store.Put(site)
	a.record(action, site, false, err.Error())
	a.render(w, r, view{LoggedIn: true, Error: err.Error()})
}

func (a *app) backupSite(w http.ResponseWriter, r *http.Request, site core.Site) {
	if site.Status != core.StatusRunning {
		http.Error(w, "site must be running", http.StatusConflict)
		return
	}
	id, err := core.NewBackupID(time.Now())
	if err != nil {
		http.Error(w, "unable to generate backup ID", http.StatusInternalServerError)
		return
	}
	var backup core.Backup
	if err := a.callWorker("/backup", core.BackupRequest{Site: site, BackupID: id}, &backup); err != nil {
		a.record("backup", site, false, err.Error())
		a.render(w, r, view{LoggedIn: true, Error: "Backup failed: " + err.Error()})
		return
	}
	site.Backups = append(site.Backups, backup)
	if err := a.store.Put(site); err != nil {
		http.Error(w, "backup completed but metadata could not be saved", http.StatusInternalServerError)
		return
	}
	a.record("backup", site, true, id)
	a.redirectWithFlashTo(w, r, "/sites/"+site.ID+"/backups", "Backup completed and verified", "")
}

func (a *app) deleteBackup(w http.ResponseWriter, r *http.Request, site core.Site) {
	backupID := r.FormValue("backup_id")
	if r.FormValue("confirm") != site.Domain || !core.ValidBackupID(backupID) {
		a.render(w, r, view{LoggedIn: true, Error: "Domain confirmation or backup ID did not match"})
		return
	}
	found := false
	for _, backup := range site.Backups {
		if backup.ID == backupID {
			found = true
			break
		}
	}
	if !found {
		http.Error(w, "unknown backup", http.StatusBadRequest)
		return
	}
	if err := a.callWorker("/backup/delete", core.DeleteBackupRequest{SiteID: site.ID, BackupID: backupID}, nil); err != nil {
		a.record("backup-delete", site, false, err.Error())
		a.render(w, r, view{LoggedIn: true, Error: "Backup deletion failed: " + err.Error()})
		return
	}
	kept := site.Backups[:0]
	for _, backup := range site.Backups {
		if backup.ID != backupID {
			kept = append(kept, backup)
		}
	}
	site.Backups = kept
	if err := a.store.Put(site); err != nil {
		http.Error(w, "backup deleted but metadata could not be saved", http.StatusInternalServerError)
		return
	}
	a.record("backup-delete", site, true, backupID)
	a.redirectWithFlashTo(w, r, "/sites/"+site.ID+"/backups", "Backup deleted", "")
}

func (a *app) updateSite(w http.ResponseWriter, r *http.Request, site core.Site) {
	if site.Status != core.StatusRunning {
		http.Error(w, "site must be running", http.StatusConflict)
		return
	}
	backupID, err := core.NewBackupID(time.Now())
	if err != nil {
		http.Error(w, "unable to generate safety backup ID", http.StatusInternalServerError)
		return
	}
	var safety core.Backup
	if err := a.callWorker("/backup", core.BackupRequest{Site: site, BackupID: backupID}, &safety); err != nil {
		a.record("update", site, false, err.Error())
		a.render(w, r, view{LoggedIn: true, Error: "Update stopped because the safety backup failed: " + err.Error()})
		return
	}
	site.Backups = append(site.Backups, safety)
	if err := a.store.Put(site); err != nil {
		a.render(w, r, view{LoggedIn: true, Error: "Update stopped because safety backup metadata could not be saved"})
		return
	}
	if err := a.callWorker("/update", core.UpdateRequest{Site: site}, nil); err != nil {
		a.record("update", site, false, err.Error())
		a.render(w, r, view{LoggedIn: true, Error: "WordPress update failed. Safety backup retained: " + backupID + ". " + err.Error()})
		return
	}
	a.record("update", site, true, "safety backup "+backupID)
	a.redirectWithFlashTo(w, r, "/sites/"+site.ID+"/updates", "WordPress core, database, plugins, and themes updated. Safety backup retained: "+backupID, "")
}

func (a *app) restoreSite(w http.ResponseWriter, r *http.Request, site core.Site) {
	if site.Status != core.StatusRunning {
		http.Error(w, "site must be running", http.StatusConflict)
		return
	}
	if r.FormValue("confirm") != site.Domain {
		a.render(w, r, view{LoggedIn: true, Error: "Domain confirmation did not match"})
		return
	}
	backupID := r.FormValue("backup_id")
	found := false
	for _, backup := range site.Backups {
		if backup.ID == backupID {
			found = true
			break
		}
	}
	if !found || !core.ValidBackupID(backupID) {
		http.Error(w, "unknown backup", http.StatusBadRequest)
		return
	}
	safetyID, err := core.NewBackupID(time.Now())
	if err != nil {
		http.Error(w, "unable to generate safety backup ID", http.StatusInternalServerError)
		return
	}
	var safety core.Backup
	if err := a.callWorker("/backup", core.BackupRequest{Site: site, BackupID: safetyID}, &safety); err != nil {
		a.render(w, r, view{LoggedIn: true, Error: "Restore stopped because the safety backup failed: " + err.Error()})
		return
	}
	site.Backups = append(site.Backups, safety)
	if err := a.store.Put(site); err != nil {
		a.render(w, r, view{LoggedIn: true, Error: "Restore stopped because safety backup metadata could not be saved"})
		return
	}
	if err := a.callWorker("/restore", core.RestoreRequest{Site: site, BackupID: backupID, SafetyBackupID: safetyID}, nil); err != nil {
		a.record("restore", site, false, err.Error())
		a.render(w, r, view{LoggedIn: true, Error: "Restore failed. A safety backup was retained: " + safetyID + ". " + err.Error()})
		return
	}
	a.record("restore", site, true, backupID)
	a.redirectWithFlashTo(w, r, "/sites/"+site.ID+"/backups", "Backup restored. Safety backup retained: "+safetyID, "")
}

func siteIDFromFilesPath(requestPath string) (string, bool) {
	parts := strings.Split(strings.TrimPrefix(requestPath, "/sites/"), "/")
	if len(parts) != 2 || parts[1] != "files" || !core.ValidID(parts[0]) {
		return "", false
	}
	return parts[0], true
}

func (a *app) showFiles(w http.ResponseWriter, r *http.Request) {
	id, ok := siteIDFromFilesPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	site, found, err := a.store.Get(id)
	if err != nil || !found || site.Status != core.StatusRunning {
		http.Error(w, "running site not found", http.StatusNotFound)
		return
	}
	current := r.URL.Query().Get("path")
	if !core.ValidRelativePath(current, true) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	a.renderFiles(w, r, site, current, "", "")
}

func (a *app) renderFiles(w http.ResponseWriter, r *http.Request, site core.Site, current, message, viewError string) {
	var entries []core.FileEntry
	if err := a.callWorker("/files/list", core.FileRequest{SiteID: site.ID, Path: current}, &entries); err != nil {
		http.Error(w, "unable to list files: "+err.Error(), http.StatusBadGateway)
		return
	}
	items := make([]fileEntryView, 0, len(entries))
	for _, entry := range entries {
		candidate := entry.Name
		if current != "" {
			candidate = current + "/" + entry.Name
		}
		if !core.ValidRelativePath(candidate, false) {
			continue
		}
		items = append(items, fileEntryView{FileEntry: entry, Path: candidate})
	}
	parent, hasParent := "", current != ""
	if hasParent {
		parent = path.Dir(current)
		if parent == "." {
			parent = ""
		}
	}
	csrf := ""
	if cookie, err := r.Cookie("wph_session"); err == nil {
		csrf = a.csrf(cookie.Value)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	language := i18n.Resolve(r)
	fileView := filesView{Site: site, CSRF: csrf, CurrentPath: current, Parent: parent, HasParent: hasParent, Entries: items, Error: viewError, Message: message, Language: language, Direction: i18n.Direction(language), Languages: i18n.Languages(), PagePath: r.URL.RequestURI()}
	if err := filesPage(fileView).Render(r.Context(), w); err != nil {
		log.Printf("file page render failed: %v", err)
	}
}

func joinRelative(directory, name string) (string, error) {
	if !core.ValidRelativePath(directory, true) || path.Base(name) != name || !core.ValidRelativePath(name, false) {
		return "", errors.New("invalid file name")
	}
	if directory == "" {
		return name, nil
	}
	joined := directory + "/" + name
	if !core.ValidRelativePath(joined, false) {
		return "", errors.New("invalid file path")
	}
	return joined, nil
}

func (a *app) redirectFiles(w http.ResponseWriter, r *http.Request, siteID, directory string) {
	target := "/sites/" + siteID + "/files"
	if directory != "" {
		target += "?" + url.Values{"path": {directory}}.Encode()
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (a *app) fileAction(w http.ResponseWriter, r *http.Request, site core.Site, action string) {
	if site.Status != core.StatusRunning {
		http.Error(w, "site must be running", http.StatusConflict)
		return
	}
	current := r.FormValue("path")
	if action == "upload" {
		file, header, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "invalid upload", http.StatusBadRequest)
			return
		}
		defer file.Close()
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		target, err := joinRelative(current, header.Filename)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		content, err := io.ReadAll(io.LimitReader(file, (10<<20)+1))
		if err != nil || len(content) > 10<<20 {
			http.Error(w, "file exceeds 10 MB", http.StatusRequestEntityTooLarge)
			return
		}
		if err := a.callWorker("/files/write", core.FileRequest{SiteID: site.ID, Path: target, Content: content}, nil); err != nil {
			a.renderFiles(w, r, site, current, "", "Upload failed: "+err.Error())
			return
		}
		a.redirectFiles(w, r, site.ID, current)
		return
	}
	if action == "mkdir" {
		target, err := joinRelative(current, strings.TrimSpace(r.FormValue("name")))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := a.callWorker("/files/mkdir", core.FileRequest{SiteID: site.ID, Path: target}, nil); err != nil {
			a.renderFiles(w, r, site, current, "", "Create directory failed: "+err.Error())
			return
		}
		a.redirectFiles(w, r, site.ID, current)
		return
	}
	target := r.FormValue("path")
	if !core.ValidRelativePath(target, false) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	directory := path.Dir(target)
	if directory == "." {
		directory = ""
	}
	if action == "file-delete" {
		if err := a.callWorker("/files/delete", core.FileRequest{SiteID: site.ID, Path: target}, nil); err != nil {
			a.renderFiles(w, r, site, directory, "", "Delete failed: "+err.Error())
			return
		}
		a.redirectFiles(w, r, site.ID, directory)
		return
	}
	if action == "download" {
		var content core.FileContent
		if err := a.callWorker("/files/read", core.FileRequest{SiteID: site.ID, Path: target}, &content); err != nil {
			a.renderFiles(w, r, site, directory, "", "Download failed: "+err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": content.Name}))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(content.Content)
		return
	}
	http.NotFound(w, r)
}

func (a *app) record(action string, site core.Site, success bool, detail string) {
	if a.audit != nil {
		if err := a.audit.Append(audit.Entry{Action: action, SiteID: site.ID, Domain: site.Domain, Success: success, Detail: detail}); err != nil {
			log.Printf("write audit log: %v", err)
		}
	}
}

func (a *app) applySiteMail(site core.Site, enabled bool) error {
	mail, err := a.settings.Load()
	if err != nil {
		return err
	}
	if enabled && !mail.Enabled {
		return errors.New("configure and enable the global mail server first")
	}
	if site.Status != core.StatusRunning {
		return errors.New("site must be running")
	}
	mail.Enabled = enabled
	return a.callWorker("/mail", settings.ApplyRequest{SiteID: site.ID, Mail: mail}, nil)
}

func (a *app) saveMailSettings(w http.ResponseWriter, r *http.Request) {
	a.opsMu.Lock()
	defer a.opsMu.Unlock()
	existing, err := a.settings.Load()
	if err != nil {
		a.render(w, r, view{LoggedIn: true, Error: "Unable to load mail settings: " + err.Error()})
		return
	}
	port, err := strconv.Atoi(r.FormValue("smtp_port"))
	if err != nil && r.FormValue("enabled") == "on" {
		a.render(w, r, view{LoggedIn: true, Error: "SMTP port must be a number"})
		return
	}
	password := r.FormValue("smtp_password")
	if password == "" {
		password = existing.Password
	}
	mail := settings.Mail{Enabled: r.FormValue("enabled") == "on", Host: r.FormValue("smtp_host"), Port: port, Encryption: r.FormValue("smtp_encryption"), Username: r.FormValue("smtp_username"), Password: password, FromEmail: r.FormValue("smtp_from_email"), FromName: r.FormValue("smtp_from_name")}
	if err := a.settings.Save(mail); err != nil {
		a.render(w, r, view{LoggedIn: true, Error: "Invalid mail settings: " + err.Error()})
		return
	}
	sites, _ := a.store.List()
	failed := 0
	for _, site := range sites {
		if site.Status != core.StatusRunning || !site.MailEnabled {
			continue
		}
		if err := a.applySiteMail(site, mail.Enabled); err != nil {
			failed++
			a.record("mail-apply", site, false, err.Error())
		}
	}
	if a.audit != nil {
		_ = a.audit.Append(audit.Entry{Action: "mail-settings", Success: failed == 0, Detail: fmt.Sprintf("updated; %d site failures", failed)})
	}
	if failed > 0 {
		a.redirectWithFlashTo(w, r, "/settings", "Mail settings saved", fmt.Sprintf("Could not apply settings to %d running sites", failed))
		return
	}
	a.redirectWithFlashTo(w, r, "/settings", "Mail settings saved and applied", "")
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "hash-password" {
		password, err := io.ReadAll(io.LimitReader(os.Stdin, 1024))
		if err != nil || len(password) < 12 {
			log.Fatal("password must contain at least 12 bytes")
		}
		hash, err := bcrypt.GenerateFromPassword(bytes.TrimSpace(password), bcrypt.DefaultCost)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(string(hash))
		return
	}
	hash := os.Getenv("PANEL_ADMIN_HASH")
	key := os.Getenv("PANEL_SESSION_KEY")
	token := os.Getenv("WORKER_TOKEN")
	if hash == "" || len(key) < 32 || len(token) < 32 {
		log.Fatal("PANEL_ADMIN_HASH, PANEL_SESSION_KEY and WORKER_TOKEN are required")
	}
	panelDomain := os.Getenv("PANEL_DOMAIN")
	settingsStore, err := settings.New("/data/settings.json", []byte(key))
	if err != nil {
		log.Fatal(err)
	}
	app := &app{store: store.New("/data/sites.json"), settings: settingsStore, audit: audit.New("/data/audit.jsonl"), workerURL: "http://worker:8081", workerToken: token, adminHash: []byte(hash), sessionKey: []byte(key), secureCookie: !strings.HasPrefix(panelDomain, "http://localhost") && !strings.HasPrefix(panelDomain, "http://127.0.0.1"), client: &http.Client{Timeout: 20*time.Minute + 10*time.Second}, loginFails: make(map[string][]time.Time), flashes: make(map[string]flash)}
	sites, err := app.store.List()
	if err != nil {
		log.Fatal(err)
	}
	for _, site := range sites {
		if site.Status == core.StatusCreating || site.Status == core.StatusDeleting {
			site.Status, site.Error = core.StatusFailed, "Operation interrupted; inspect server and retry"
			if err := app.store.Put(site); err != nil {
				log.Fatal(err)
			}
		}
	}
	server := &http.Server{Addr: ":8080", Handler: app, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 21 * time.Minute, IdleTimeout: 60 * time.Second}
	log.Println("panel listening on :8080")
	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
