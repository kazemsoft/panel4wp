package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/example/wp-host-panel/internal/core"
	"github.com/example/wp-host-panel/internal/store"
)

var page = template.Must(template.New("page").Funcs(template.FuncMap{"hasSuffix": strings.HasSuffix, "formatTime": func(t time.Time) string { return t.Local().Format("2006-01-02 15:04") }}).Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>WP Host Panel</title><style>
body{font:16px system-ui,sans-serif;background:#f5f7fa;color:#17212b;max-width:1050px;margin:2rem auto;padding:0 1rem}header{display:flex;justify-content:space-between;align-items:center}h1{font-size:1.6rem}main{display:grid;grid-template-columns:320px 1fr;gap:1.5rem}section{background:#fff;border:1px solid #dbe2ea;border-radius:12px;padding:1.25rem;margin-bottom:1rem}label{display:block;font-weight:600;margin-top:1rem}input,select{box-sizing:border-box;width:100%;padding:.65rem;border:1px solid #aab7c4;border-radius:7px;margin-top:.3rem;background:#fff}button{border:0;background:#165dba;color:#fff;border-radius:7px;padding:.65rem 1rem;cursor:pointer}button.danger{background:#ad2635}button.secondary{background:#4b6077}form.inline{display:inline-block;margin:.15rem}form.inline button{font-size:.85rem}table{width:100%;border-collapse:collapse}td,th{padding:.7rem .3rem;border-bottom:1px solid #e5eaf0;text-align:left;vertical-align:top}small,.muted{color:#607386}.error{background:#ffe9e9;padding:.8rem;border-radius:7px}.success{background:#e6f6eb;padding:.8rem;border-radius:7px}.badge{background:#edf2f6;border-radius:99px;padding:.15rem .5rem}code{overflow-wrap:anywhere}@media(max-width:760px){main{display:block}}
</style></head><body><header><h1>WP Host Panel</h1>{{if .LoggedIn}}<form action="/logout" method="post"><input type="hidden" name="csrf" value="{{.CSRF}}"><button class="secondary">Log out</button></form>{{end}}</header>
{{if .Error}}<p class="error">{{.Error}}</p>{{end}}{{if .Message}}<p class="success">{{.Message}}</p>{{end}}
{{if not .LoggedIn}}<section style="max-width:380px"><h2>Administrator login</h2><form action="/login" method="post"><label>Password<input type="password" name="password" autocomplete="current-password" required></label><p><button>Log in</button></p></form></section>{{else}}
<main><section><h2>Create WordPress site</h2><form action="/sites" method="post"><input type="hidden" name="csrf" value="{{.CSRF}}"><label>Domain<input name="domain" placeholder="example.com; blank for local test"></label><label>Site title<input name="title" required maxlength="120"></label><label>WordPress admin email<input type="email" name="email" required></label><label>Resources per WordPress and database container<select name="plan"><option value="small">Small · 384 MB · 0.5 CPU</option><option value="standard" selected>Standard · 768 MB · 1 CPU</option><option value="large">Large · 1536 MB · 2 CPUs</option></select></label><p><button>Create site</button></p></form><small>Blank domain creates an HTTP-only *.localhost site for testing on this computer. Public domains require DNS to point to this server.</small></section>
<section><h2>Sites</h2>{{if not .Sites}}<p class="muted">No sites yet.</p>{{else}}<table><thead><tr><th>Site</th><th>Status</th><th>Actions</th></tr></thead><tbody>{{range .Sites}}{{$site := .}}<tr><td><strong>{{.Title}}</strong><br><a href="{{if hasSuffix .Domain ".localhost"}}http{{else}}https{{end}}://{{.Domain}}" target="_blank" rel="noopener">{{.Domain}}</a><br><small>{{.ID}} · {{.MemoryMB}} MB · {{.CPUs}} CPU</small>{{if .Error}}<p class="error">{{.Error}}</p>{{end}}</td><td><span class="badge">{{.Status}}</span></td><td>
{{if eq .Status "running"}}<form class="inline" action="/sites/{{.ID}}/stop" method="post"><input type="hidden" name="csrf" value="{{$.CSRF}}"><button class="secondary">Stop</button></form>{{end}}
{{if eq .Status "running"}}<form class="inline" action="/sites/{{.ID}}/backup" method="post"><input type="hidden" name="csrf" value="{{$.CSRF}}"><button>Back up now</button></form>{{end}}
{{if eq .Status "stopped"}}<form class="inline" action="/sites/{{.ID}}/start" method="post"><input type="hidden" name="csrf" value="{{$.CSRF}}"><button>Start</button></form>{{end}}
{{if eq .Status "failed"}}<form class="inline" action="/sites/{{.ID}}/retry" method="post"><input type="hidden" name="csrf" value="{{$.CSRF}}"><button>Retry</button></form>{{end}}
{{if .Backups}}<details><summary>Restore a backup</summary>{{range .Backups}}<form action="/sites/{{$site.ID}}/restore" method="post"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="backup_id" value="{{.ID}}"><small>{{formatTime .CreatedAt}} · {{.ID}}</small><label>Type {{$site.Domain}} to confirm<input name="confirm" required></label><p><button class="danger">Restore this backup</button></p></form>{{end}}</details>{{end}}
<details><summary>Delete permanently</summary><form action="/sites/{{.ID}}/delete" method="post"><input type="hidden" name="csrf" value="{{$.CSRF}}"><label>Type domain to confirm<input name="confirm" required placeholder="{{.Domain}}"></label><p><button class="danger">Delete site and data</button></p></form></details></td></tr>{{end}}</tbody></table>{{end}}</section></main>{{end}}</body></html>`))

type view struct {
	LoggedIn bool
	CSRF     string
	Error    string
	Message  string
	Sites    []core.Site
}

type app struct {
	store        *store.Store
	workerURL    string
	workerToken  string
	adminHash    []byte
	sessionKey   []byte
	secureCookie bool
	client       *http.Client
	opsMu        sync.Mutex
	loginMu      sync.Mutex
	loginFails   map[string][]time.Time
}

func (a *app) render(w http.ResponseWriter, r *http.Request, v view) {
	if v.LoggedIn {
		sites, err := a.store.List()
		if err != nil {
			http.Error(w, "unable to read sites", http.StatusInternalServerError)
			return
		}
		v.Sites = sites
		if c, err := r.Cookie("wph_session"); err == nil {
			v.CSRF = a.csrf(c.Value)
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	_ = page.Execute(w, v)
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
	if err != nil || r.ParseForm() != nil {
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
		return json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(result)
	}
	return nil
}

func (a *app) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/health" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/" {
		a.render(w, r, view{LoggedIn: a.authenticated(r)})
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
			a.render(w, r, view{Error: "Invalid password"})
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
		return
	}
	site.Status = core.StatusRunning
	if err := a.store.Put(site); err != nil {
		http.Error(w, "site created but status could not be saved", http.StatusInternalServerError)
		return
	}
	message := "Site created. WordPress username: admin. Password (save now): " + adminPassword
	a.render(w, r, view{LoggedIn: true, Message: message})
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
	if action == "delete" && r.FormValue("confirm") != site.Domain {
		a.render(w, r, view{LoggedIn: true, Error: "Domain confirmation did not match"})
		return
	}
	if action == "backup" {
		a.backupSite(w, r, site)
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
			a.render(w, r, view{LoggedIn: true, Message: "Site creation retried. WordPress username: admin. New password (save now): " + adminPassword})
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
			a.render(w, r, view{LoggedIn: true, Message: "Site action completed"})
			return
		}
	}
	site.Status, site.Error = core.StatusFailed, err.Error()
	_ = a.store.Put(site)
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
		a.render(w, r, view{LoggedIn: true, Error: "Backup failed: " + err.Error()})
		return
	}
	site.Backups = append(site.Backups, backup)
	if err := a.store.Put(site); err != nil {
		http.Error(w, "backup completed but metadata could not be saved", http.StatusInternalServerError)
		return
	}
	a.render(w, r, view{LoggedIn: true, Message: "Backup completed and verified"})
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
		a.render(w, r, view{LoggedIn: true, Error: "Restore failed. A safety backup was retained: " + safetyID + ". " + err.Error()})
		return
	}
	a.render(w, r, view{LoggedIn: true, Message: "Backup restored. Safety backup retained: " + safetyID})
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
	app := &app{store: store.New("/data/sites.json"), workerURL: "http://worker:8081", workerToken: token, adminHash: []byte(hash), sessionKey: []byte(key), secureCookie: !strings.HasPrefix(panelDomain, "http://localhost") && !strings.HasPrefix(panelDomain, "http://127.0.0.1"), client: &http.Client{Timeout: 20*time.Minute + 10*time.Second}, loginFails: make(map[string][]time.Time)}
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
