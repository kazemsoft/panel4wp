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
	"github.com/kazemsoft/panel4wp/internal/settings"
	"github.com/kazemsoft/panel4wp/internal/store"
)

var page = template.Must(template.New("page").Funcs(template.FuncMap{"hasSuffix": strings.HasSuffix, "formatTime": func(t time.Time) string { return t.Local().Format("2006-01-02 15:04") }, "formatBytes": func(n int64) string {
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
}}).Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>panel4wp · WordPress workspace</title><style>
:root{--bg:#f6f8fb;--surface:#fff;--ink:#192639;--muted:#718096;--line:#e5eaf1;--primary:#4355d6;--soft:#eef0ff;--danger:#be354b}*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--ink);font:14px/1.6 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}a{color:var(--primary);text-decoration:none}a:hover{text-decoration:underline}button,input,select{font:inherit}button,.button{display:inline-flex;align-items:center;justify-content:center;gap:8px;border:1px solid transparent;background:var(--primary);color:#fff;border-radius:9px;padding:9px 15px;font-weight:600;cursor:pointer;transition:background .15s,box-shadow .15s}button:hover,.button:hover{background:#3343b6;text-decoration:none}button:focus-visible,a:focus-visible,summary:focus-visible{outline:3px solid #a8b2ff;outline-offset:3px}button.secondary,.button.secondary{background:#fff;color:#52617a;border-color:var(--line)}button.secondary:hover,.button.secondary:hover{background:#f0f3f8}button.danger{background:#fff3f4;color:var(--danger);border-color:#f2d4da}button:disabled{opacity:.65;cursor:wait}input,select{display:block;width:100%;padding:10px 12px;margin-top:6px;border:1px solid #d8dfea;border-radius:9px;background:#fff;color:var(--ink)}input:focus,select:focus{outline:3px solid #eef0ff;border-color:#8e9bea}input[type=hidden]{display:none}input[type=file]{padding:8px}label{display:block;font-size:12px;font-weight:650;color:#536178;margin:16px 0}h1,h2,h3,p{margin-top:0}h1{font-size:28px;line-height:1.25;letter-spacing:-.8px;margin-bottom:8px}h2{font-size:18px;letter-spacing:-.35px;margin-bottom:16px}h3{font-size:16px;margin-bottom:5px}small,.muted{color:var(--muted)}small{font-size:12px}.eyebrow{font-size:10px;letter-spacing:1.7px;text-transform:uppercase;font-weight:700;color:#8590a3}.shell{display:grid;grid-template-columns:226px minmax(0,1fr);min-height:100vh}.sidebar{background:#fff;border-right:1px solid var(--line);padding:32px 22px;display:flex;flex-direction:column;position:sticky;top:0;height:100vh}.brand{display:flex;align-items:center;gap:10px;color:var(--ink);font-size:20px;font-weight:750;letter-spacing:-.5px;margin-bottom:40px}.brand:hover{text-decoration:none}.mark{display:grid;place-items:center;width:35px;height:35px;background:var(--primary);color:#fff;border-radius:11px;font-size:18px}.nav-link{display:block;padding:11px 14px;border-radius:9px;margin:5px 0;font-weight:600;color:#647089}.nav-link.active{background:var(--soft);color:var(--primary)}.sidebar .eyebrow{margin:20px 14px 8px}.sidebar-footer{margin-top:auto;padding-top:30px}.sidebar-footer p{font-size:12px;color:var(--muted)}.workspace{padding:35px 40px;max-width:1600px;width:100%;margin:auto}.topbar{display:flex;align-items:center;justify-content:space-between;gap:16px;padding-bottom:30px}.topbar p{margin:0;color:var(--muted)}.admin-chip{background:white;border:1px solid var(--line);border-radius:30px;padding:7px 13px;font-size:12px;color:#69758b}.dot{display:inline-block;width:7px;height:7px;background:#5aab85;border-radius:50%;margin-right:7px}.overview{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:16px;margin-bottom:28px}.metric{padding:20px 24px;background:#fff;border:1px solid var(--line);border-radius:13px}.metric strong{display:block;font-size:23px;font-weight:650;margin-top:5px}.metric span{font-size:12px;color:var(--muted)}main.dashboard{display:grid;grid-template-columns:minmax(0,1fr) 310px;gap:24px;align-items:start}section,.card{background:var(--surface);border:1px solid var(--line);border-radius:14px;padding:24px;margin-bottom:20px}.create-card{grid-column:2;grid-row:1;position:sticky;top:24px}.sites-section{grid-column:1;grid-row:1;background:none;border:0;padding:0}.section-head{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:16px}.section-head h2{margin:0}.section-head a{font-size:12px;font-weight:600}.site-card{padding:0;overflow:hidden}.site-head{display:flex;align-items:flex-start;gap:13px;padding:22px 24px}.site-icon{width:42px;height:42px;flex-shrink:0;background:#f1f3fd;border:1px solid #e5e9fb;border-radius:12px;display:grid;place-items:center;color:#6874c6;font-weight:700;font-size:21px}.site-meta{min-width:0;flex:1}.site-meta a,.site-meta small{overflow-wrap:anywhere}.site-meta small{display:block;margin-top:4px;font-size:11px}.badge{display:inline-block;border-radius:30px;padding:3px 9px;font-size:10px;font-weight:700;background:#edf0f5;color:#718096;text-transform:uppercase;letter-spacing:.6px}.badge.running{color:#278260;background:#eaf7f0}.badge.failed{color:#b03e50;background:#fff0f2}.badge.stopped{color:#8b6c30;background:#fff7e7}.site-actions{padding:16px 24px;border-top:1px solid var(--line);background:#fcfdff;display:flex;align-items:center;flex-wrap:wrap;gap:8px}.inline{display:inline-block;margin:0}.inline button{font-size:12px;padding:7px 11px}.site-actions>small{width:100%;font-size:11px}.site-details{padding:0 24px 16px}.site-details:empty{display:none}details{border-top:1px solid var(--line);padding:12px 0}summary{cursor:pointer;font-weight:600;color:#69758b;font-size:12px}details form{padding:12px 0}details form+form{border-top:1px solid var(--line)}details p{margin:10px 0}details label{margin:10px 0}.danger-zone summary{color:#aa5261}.error,.success{padding:14px 18px;border-radius:10px;overflow-wrap:anywhere;white-space:pre-line;font-size:13px;margin-bottom:20px}.error{background:#fff0f2;color:#a43448;border:1px solid #f4dce0}.success{background:#eaf7f0;color:#277653;border:1px solid #d6eedf}.empty{text-align:center;padding:45px 20px;background:#fff;border:1px dashed #d5ddea;border-radius:14px;color:var(--muted)}.empty h3{color:var(--ink)}.roadmap{margin-top:12px}.roadmap-head{display:flex;align-items:center;gap:12px;margin-bottom:15px}.roadmap-head h2{margin:0}.roadmap-grid{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:12px}.future{padding:14px;border:1px dashed #dce2ee;border-radius:10px;color:#7b879a;font-size:12px;background:#fafbfe}.future span{display:block;font-size:10px;color:#9aa5b5;margin-top:3px}.login-layout{min-height:100vh;display:grid;place-items:center;padding:25px}.login-card{width:100%;max-width:420px;padding:38px;box-shadow:0 12px 50px #24355408}.login-card .brand{margin-bottom:30px}.login-card button,.create-card button{width:100%}.login-card h1{font-size:24px}.login-card .error,.login-card .success{margin-top:15px}code{font-size:12px;overflow-wrap:anywhere}.file-topbar{margin-bottom:20px}.file-topbar h1{margin-top:15px}.table-wrap{overflow-x:auto}table{width:100%;border-collapse:collapse;min-width:550px}th{font-size:10px;letter-spacing:.8px;text-transform:uppercase;color:#8a95a7;font-weight:600}td,th{padding:13px 10px;border-bottom:1px solid var(--line);text-align:left;vertical-align:middle}td{font-size:12px}td:first-child{overflow-wrap:anywhere;max-width:380px}.file-tools{display:grid;grid-template-columns:1fr 1fr;gap:20px}.file-tools form{display:flex;align-items:end;gap:10px}.file-tools label{flex:1;margin:0}.file-tools input{min-width:0}.file-tools button{white-space:nowrap}.path{background:#f7f9fc;padding:11px 14px;border-radius:8px;font-size:12px}.hint{font-size:12px;color:var(--muted)}@media(min-width:1600px){.workspace{padding:40px 55px}}@media(max-width:1150px){.workspace{padding:28px 25px}.shell{grid-template-columns:185px minmax(0,1fr)}.sidebar{padding:28px 15px}main.dashboard{grid-template-columns:minmax(0,1fr) 270px;gap:18px}.site-head{padding:18px}.site-actions{padding:14px 18px}.site-details{padding:0 18px 14px}.roadmap-grid{grid-template-columns:repeat(3,1fr)}.file-tools{grid-template-columns:1fr}}@media(max-width:900px){main.dashboard{display:flex;flex-direction:column}.create-card{position:static;width:100%;order:2}.sites-section{width:100%;order:1}.overview{gap:10px}.metric{padding:16px}.metric strong{font-size:20px}}@media(max-width:650px){.shell{display:block}.sidebar{position:static;height:auto;padding:14px 20px;display:flex;flex-direction:row;flex-wrap:wrap;align-items:center;gap:10px;border-right:0;border-bottom:1px solid var(--line)}.brand{font-size:18px;margin:0}.mark{width:30px;height:30px}.sidebar nav{display:flex;margin-left:auto}.sidebar .eyebrow,.sidebar-footer p,.sidebar .roadmap-nav{display:none}.sidebar-footer{margin:0;padding:0}.sidebar-footer button{font-size:11px;padding:7px 10px}.nav-link{font-size:11px;padding:7px 10px;margin:0}.workspace{padding:24px 16px}.topbar{padding-bottom:22px}.topbar h1{font-size:24px}.admin-chip{display:none}.overview{grid-template-columns:1fr 1fr}.overview .metric:last-child{display:none}.metric strong{font-size:18px}.site-head{gap:10px}.site-icon{display:none}.badge{font-size:9px;padding:3px 7px}.roadmap-grid{grid-template-columns:1fr 1fr}.file-tools form{display:block}.file-tools button{margin-top:12px}.section-head{align-items:flex-start}section{padding:20px}.login-card{padding:28px}.site-actions .button{font-size:12px;padding:7px 11px}}
</style></head><body>{{if not .LoggedIn}}<div class="login-layout"><section class="login-card"><a class="brand" href="/"><span class="mark">p</span>panel4wp</a><div class="eyebrow">Your WordPress workspace</div><h1>Welcome back.</h1><p class="muted">Sign in to manage your sites.</p>{{if .Error}}<p class="error" role="alert">{{.Error}}</p>{{end}}{{if .Message}}<p class="success" role="status">{{.Message}}</p>{{end}}<form action="/login" method="post"><label>Administrator password<input type="password" name="password" autocomplete="current-password" required autofocus></label><button>Log in →</button></form><p class="hint" style="margin:22px 0 0">Private control. Open-source foundation.</p></section></div>{{else}}<div class="shell"><aside class="sidebar"><a class="brand" href="/"><span class="mark">p</span>panel4wp</a><nav><div class="eyebrow">Workspace</div><a class="nav-link active" href="/">▦ &nbsp; My sites</a><a class="nav-link" href="/#settings">⚙ &nbsp; Settings</a><a class="nav-link roadmap-nav" href="/#roadmap">◷ &nbsp; What's next</a></nav><div class="sidebar-footer"><p>Open source.<br>Your server. Your sites.</p><form action="/logout" method="post"><input type="hidden" name="csrf" value="{{.CSRF}}"><button class="secondary">Log out</button></form></div></aside><div class="workspace"><header class="topbar"><div><div class="eyebrow">Overview</div><h1>Your WordPress workspace</h1><p>A little clarity for everything you host.</p></div><span class="admin-chip"><span class="dot"></span>Administrator</span></header>{{if .Error}}<p class="error" role="alert">{{.Error}}</p>{{end}}{{if .Message}}<p class="success" role="status">{{.Message}}</p>{{end}}<div class="overview"><div class="metric"><span>Managed sites</span><strong>{{len .Sites}}</strong></div><div class="metric"><span>Hosting environment</span><strong>Your server</strong></div><div class="metric"><span>Workspace access</span><strong>Single administrator</strong></div></div><main class="dashboard"><section class="create-card" id="create-site"><div class="eyebrow">New workspace</div><h2>Create a site</h2><p class="hint">A fresh WordPress installation, ready in a few moments.</p><form action="/sites" method="post"><input type="hidden" name="csrf" value="{{.CSRF}}"><label>Domain<input name="domain" placeholder="example.com"></label><label>Site title<input name="title" required maxlength="120"></label><label>WordPress admin email<input type="email" name="email" required></label><label>Resource plan<select name="plan"><option value="small">Small · 384 MB · 0.5 CPU</option><option value="standard" selected>Standard · 768 MB · 1 CPU</option><option value="large">Large · 1536 MB · 2 CPUs</option></select></label><p><button>Create site</button></p></form><small>Blank domain creates an HTTP-only *.localhost site for testing on this computer. Public domains require DNS to point to this server.</small></section>
<section class="sites-section" id="sites"><div class="section-head"><div><h2>Your sites <span class="badge">{{len .Sites}}</span></h2></div><a href="/?stats=1">↻ Refresh usage</a></div>{{if not .Sites}}<div class="empty"><h3>Your first site starts here</h3><p>Create a WordPress site to manage its files, database and backups in one place.</p><a href="#create-site">Create your first site →</a></div>{{else}}{{if .StatsError}}<p class="error">{{.StatsError}}</p>{{end}}{{range .Sites}}{{$site := .}}{{$stats := index $.Stats .ID}}{{$storage := index $.Storage .ID}}<article class="card site-card"><div class="site-head"><div class="site-icon">W</div><div class="site-meta"><h3>{{.Title}}</h3><a href="{{if hasSuffix .Domain ".localhost"}}http{{else}}https{{end}}://{{.Domain}}" target="_blank" rel="noopener">{{.Domain}}</a><small>{{.ID}} · {{.MemoryMB}} MB · {{.CPUs}} CPU</small></div><span class="badge {{.Status}}">{{.Status}}</span></div><div class="site-details">{{if $stats}}<details><summary>Live resource usage</summary>{{if $stats.WordPress}}<p><small><strong>WordPress</strong> · CPU {{$stats.WordPress.CPU}} · Memory {{$stats.WordPress.Memory}} ({{$stats.WordPress.MemoryPC}}) · Network {{$stats.WordPress.NetIO}} · Disk I/O {{$stats.WordPress.BlockIO}} · PIDs {{$stats.WordPress.PIDs}}</small></p>{{end}}{{if $stats.Database}}<p><small><strong>Database</strong> · CPU {{$stats.Database.CPU}} · Memory {{$stats.Database.Memory}} ({{$stats.Database.MemoryPC}}) · Network {{$stats.Database.NetIO}} · Disk I/O {{$stats.Database.BlockIO}} · PIDs {{$stats.Database.PIDs}}</small></p>{{end}}{{if $storage}}<p><small><strong>Storage</strong> · WordPress {{formatBytes $storage.WordPressBytes}} · Database {{formatBytes $storage.DatabaseBytes}} · Backups {{formatBytes $storage.BackupBytes}}</small></p>{{end}}</details>{{end}}{{if .Error}}<p class="error">{{.Error}}</p>{{end}}</div><div class="site-actions">
{{if eq .Status "running"}}<form class="inline" action="/sites/{{.ID}}/stop" method="post"><input type="hidden" name="csrf" value="{{$.CSRF}}"><button class="secondary">Stop</button></form>{{end}}
{{if eq .Status "running"}}<form class="inline" action="/sites/{{.ID}}/backup" method="post"><input type="hidden" name="csrf" value="{{$.CSRF}}"><button>Back up now</button></form>{{end}}
{{if eq .Status "running"}}<form class="inline" action="/sites/{{.ID}}/update" method="post"><input type="hidden" name="csrf" value="{{$.CSRF}}"><button class="secondary">Update WordPress</button></form>{{end}}
{{if eq .Status "running"}}<a class="button secondary" href="/sites/{{.ID}}/files">File manager</a>{{end}}
{{if eq .Status "running"}}<form class="inline" action="/sites/{{.ID}}/database-start" method="post"><input type="hidden" name="csrf" value="{{$.CSRF}}"><button class="secondary">Open database manager</button></form><small>Database access closes after 15 minutes. WordPress updates include a safety backup.</small>{{end}}
{{if eq .Status "stopped"}}<form class="inline" action="/sites/{{.ID}}/start" method="post"><input type="hidden" name="csrf" value="{{$.CSRF}}"><button>Start</button></form>{{end}}
{{if eq .Status "failed"}}<form class="inline" action="/sites/{{.ID}}/retry" method="post"><input type="hidden" name="csrf" value="{{$.CSRF}}"><button>Retry</button></form>{{end}}
</div><div class="site-details">{{if .Backups}}<details><summary>Backups & recovery · {{len .Backups}} available</summary><p class="hint">Restore replaces the current files and database. A safety backup is created first.</p>{{range .Backups}}<form action="/sites/{{$site.ID}}/restore" method="post"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="backup_id" value="{{.ID}}"><small>{{formatTime .CreatedAt}} · {{.ID}}</small><label>Type {{$site.Domain}} to confirm<input name="confirm" required></label><p><button class="danger">Restore this backup</button></p></form>{{end}}</details>{{end}}
<details><summary>Outgoing email {{if .MailEnabled}}<span class="badge running">Enabled</span>{{end}}</summary><p class="hint">Use the encrypted global SMTP configuration for this site.</p>{{if eq .Status "running"}}<form action="/sites/{{.ID}}/{{if .MailEnabled}}mail-disable{{else}}mail-enable{{end}}" method="post"><input type="hidden" name="csrf" value="{{$.CSRF}}"><button class="secondary">{{if .MailEnabled}}Disable SMTP{{else}}Enable global SMTP{{end}}</button></form>{{else}}<p class="hint">Start the site before changing mail delivery.</p>{{end}}</details><details class="danger-zone"><summary>Delete permanently</summary><p class="hint">This removes the site, database and all backups. This cannot be undone.</p><form action="/sites/{{.ID}}/delete" method="post"><input type="hidden" name="csrf" value="{{$.CSRF}}"><label>Type domain to confirm<input name="confirm" required placeholder="{{.Domain}}"></label><p><button class="danger">Delete site and data</button></p></form></details></div></article>{{end}}{{end}}</section></main><section id="settings"><div class="roadmap-head"><h2>Mail server settings</h2>{{if .Mail.Enabled}}<span class="badge running">Enabled</span>{{else}}<span class="badge">Disabled</span>{{end}}</div><p class="hint">One encrypted SMTP connection can be enabled independently for each WordPress site.</p><form action="/settings/mail" method="post"><input type="hidden" name="csrf" value="{{.CSRF}}"><label><input type="checkbox" name="enabled" {{if .Mail.Enabled}}checked{{end}} style="display:inline;width:auto;margin-right:8px">Enable global SMTP</label><div class="file-tools"><div><label>SMTP host<input name="smtp_host" value="{{.Mail.Host}}" placeholder="smtp.example.com" autocomplete="off"></label><label>Encryption<select name="smtp_encryption"><option value="starttls" {{if eq .Mail.Encryption "starttls"}}selected{{end}}>STARTTLS</option><option value="tls" {{if eq .Mail.Encryption "tls"}}selected{{end}}>TLS</option><option value="none" {{if eq .Mail.Encryption "none"}}selected{{end}}>None</option></select></label><label>Username<input name="smtp_username" value="{{.Mail.Username}}" autocomplete="off"></label><label>From email<input type="email" name="smtp_from_email" value="{{.Mail.FromEmail}}" placeholder="hello@example.com"></label></div><div><label>Port<input type="number" name="smtp_port" value="{{if .Mail.Port}}{{.Mail.Port}}{{end}}" placeholder="587" min="1" max="65535"></label><label>Sender name<input name="smtp_from_name" value="{{.Mail.FromName}}" placeholder="Your company"></label><label>Password<input type="password" name="smtp_password" autocomplete="new-password" placeholder="{{if .MailPasswordSet}}Leave blank to keep existing password{{else}}SMTP password{{end}}"></label><p class="hint">The password is encrypted on disk and never sent back to the browser.</p></div></div><button>Save mail settings</button></form></section><section id="activity"><div class="roadmap-head"><h2>Recent activity</h2><span class="badge">{{len .Activity}} events</span></div>{{if .Activity}}<div class="table-wrap"><table><thead><tr><th>Time</th><th>Action</th><th>Site</th><th>Result</th></tr></thead><tbody>{{range .Activity}}<tr><td>{{formatTime .Time}}</td><td>{{.Action}}</td><td>{{if .Domain}}{{.Domain}}{{else}}—{{end}}</td><td>{{if .Success}}Success{{else}}Failed{{end}}</td></tr>{{end}}</tbody></table></div>{{else}}<p class="hint">Administrator actions will appear here.</p>{{end}}</section><section class="roadmap" id="roadmap"><div class="roadmap-head"><h2>On the horizon</h2><span class="badge">Coming soon</span></div><p class="hint">Planned capabilities. These features are not available yet.</p><div class="roadmap-grid"><div class="future">SFTP access<span>Secure file transfer</span></div><div class="future">Automated alerts<span>Keep an eye on server health</span></div><div class="future">Image upgrades<span>Manage container versions</span></div><div class="future">Storage quotas<span>Usage reporting is live; hard limits are next</span></div><div class="future">Scheduled remote backups<span>Protect data off this server</span></div><div class="future">Customer accounts<span>Separate customer workspaces</span></div></div></section><p class="hint">panel4wp · Built for independent WordPress hosting.</p></div></div>{{end}}</body></html>`))

var filesPage = template.Must(template.New("files").Funcs(template.FuncMap{"formatTime": func(ts int64) string { return time.Unix(ts, 0).Local().Format("2006-01-02 15:04") }}).Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>panel4wp · WordPress workspace</title><style>
:root{--bg:#f6f8fb;--surface:#fff;--ink:#192639;--muted:#718096;--line:#e5eaf1;--primary:#4355d6;--soft:#eef0ff;--danger:#be354b}*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--ink);font:14px/1.6 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}a{color:var(--primary);text-decoration:none}a:hover{text-decoration:underline}button,input,select{font:inherit}button,.button{display:inline-flex;align-items:center;justify-content:center;gap:8px;border:1px solid transparent;background:var(--primary);color:#fff;border-radius:9px;padding:9px 15px;font-weight:600;cursor:pointer;transition:background .15s,box-shadow .15s}button:hover,.button:hover{background:#3343b6;text-decoration:none}button:focus-visible,a:focus-visible,summary:focus-visible{outline:3px solid #a8b2ff;outline-offset:3px}button.secondary,.button.secondary{background:#fff;color:#52617a;border-color:var(--line)}button.secondary:hover,.button.secondary:hover{background:#f0f3f8}button.danger{background:#fff3f4;color:var(--danger);border-color:#f2d4da}button:disabled{opacity:.65;cursor:wait}input,select{display:block;width:100%;padding:10px 12px;margin-top:6px;border:1px solid #d8dfea;border-radius:9px;background:#fff;color:var(--ink)}input:focus,select:focus{outline:3px solid #eef0ff;border-color:#8e9bea}input[type=hidden]{display:none}input[type=file]{padding:8px}label{display:block;font-size:12px;font-weight:650;color:#536178;margin:16px 0}h1,h2,h3,p{margin-top:0}h1{font-size:28px;line-height:1.25;letter-spacing:-.8px;margin-bottom:8px}h2{font-size:18px;letter-spacing:-.35px;margin-bottom:16px}h3{font-size:16px;margin-bottom:5px}small,.muted{color:var(--muted)}small{font-size:12px}.eyebrow{font-size:10px;letter-spacing:1.7px;text-transform:uppercase;font-weight:700;color:#8590a3}.shell{display:grid;grid-template-columns:226px minmax(0,1fr);min-height:100vh}.sidebar{background:#fff;border-right:1px solid var(--line);padding:32px 22px;display:flex;flex-direction:column;position:sticky;top:0;height:100vh}.brand{display:flex;align-items:center;gap:10px;color:var(--ink);font-size:20px;font-weight:750;letter-spacing:-.5px;margin-bottom:40px}.brand:hover{text-decoration:none}.mark{display:grid;place-items:center;width:35px;height:35px;background:var(--primary);color:#fff;border-radius:11px;font-size:18px}.nav-link{display:block;padding:11px 14px;border-radius:9px;margin:5px 0;font-weight:600;color:#647089}.nav-link.active{background:var(--soft);color:var(--primary)}.sidebar .eyebrow{margin:20px 14px 8px}.sidebar-footer{margin-top:auto;padding-top:30px}.sidebar-footer p{font-size:12px;color:var(--muted)}.workspace{padding:35px 40px;max-width:1600px;width:100%;margin:auto}.topbar{display:flex;align-items:center;justify-content:space-between;gap:16px;padding-bottom:30px}.topbar p{margin:0;color:var(--muted)}.admin-chip{background:white;border:1px solid var(--line);border-radius:30px;padding:7px 13px;font-size:12px;color:#69758b}.dot{display:inline-block;width:7px;height:7px;background:#5aab85;border-radius:50%;margin-right:7px}.overview{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:16px;margin-bottom:28px}.metric{padding:20px 24px;background:#fff;border:1px solid var(--line);border-radius:13px}.metric strong{display:block;font-size:23px;font-weight:650;margin-top:5px}.metric span{font-size:12px;color:var(--muted)}main.dashboard{display:grid;grid-template-columns:minmax(0,1fr) 310px;gap:24px;align-items:start}section,.card{background:var(--surface);border:1px solid var(--line);border-radius:14px;padding:24px;margin-bottom:20px}.create-card{grid-column:2;grid-row:1;position:sticky;top:24px}.sites-section{grid-column:1;grid-row:1;background:none;border:0;padding:0}.section-head{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:16px}.section-head h2{margin:0}.section-head a{font-size:12px;font-weight:600}.site-card{padding:0;overflow:hidden}.site-head{display:flex;align-items:flex-start;gap:13px;padding:22px 24px}.site-icon{width:42px;height:42px;flex-shrink:0;background:#f1f3fd;border:1px solid #e5e9fb;border-radius:12px;display:grid;place-items:center;color:#6874c6;font-weight:700;font-size:21px}.site-meta{min-width:0;flex:1}.site-meta a,.site-meta small{overflow-wrap:anywhere}.site-meta small{display:block;margin-top:4px;font-size:11px}.badge{display:inline-block;border-radius:30px;padding:3px 9px;font-size:10px;font-weight:700;background:#edf0f5;color:#718096;text-transform:uppercase;letter-spacing:.6px}.badge.running{color:#278260;background:#eaf7f0}.badge.failed{color:#b03e50;background:#fff0f2}.badge.stopped{color:#8b6c30;background:#fff7e7}.site-actions{padding:16px 24px;border-top:1px solid var(--line);background:#fcfdff;display:flex;align-items:center;flex-wrap:wrap;gap:8px}.inline{display:inline-block;margin:0}.inline button{font-size:12px;padding:7px 11px}.site-actions>small{width:100%;font-size:11px}.site-details{padding:0 24px 16px}.site-details:empty{display:none}details{border-top:1px solid var(--line);padding:12px 0}summary{cursor:pointer;font-weight:600;color:#69758b;font-size:12px}details form{padding:12px 0}details form+form{border-top:1px solid var(--line)}details p{margin:10px 0}details label{margin:10px 0}.danger-zone summary{color:#aa5261}.error,.success{padding:14px 18px;border-radius:10px;overflow-wrap:anywhere;white-space:pre-line;font-size:13px;margin-bottom:20px}.error{background:#fff0f2;color:#a43448;border:1px solid #f4dce0}.success{background:#eaf7f0;color:#277653;border:1px solid #d6eedf}.empty{text-align:center;padding:45px 20px;background:#fff;border:1px dashed #d5ddea;border-radius:14px;color:var(--muted)}.empty h3{color:var(--ink)}.roadmap{margin-top:12px}.roadmap-head{display:flex;align-items:center;gap:12px;margin-bottom:15px}.roadmap-head h2{margin:0}.roadmap-grid{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:12px}.future{padding:14px;border:1px dashed #dce2ee;border-radius:10px;color:#7b879a;font-size:12px;background:#fafbfe}.future span{display:block;font-size:10px;color:#9aa5b5;margin-top:3px}.login-layout{min-height:100vh;display:grid;place-items:center;padding:25px}.login-card{width:100%;max-width:420px;padding:38px;box-shadow:0 12px 50px #24355408}.login-card .brand{margin-bottom:30px}.login-card button,.create-card button{width:100%}.login-card h1{font-size:24px}.login-card .error,.login-card .success{margin-top:15px}code{font-size:12px;overflow-wrap:anywhere}.file-topbar{margin-bottom:20px}.file-topbar h1{margin-top:15px}.table-wrap{overflow-x:auto}table{width:100%;border-collapse:collapse;min-width:550px}th{font-size:10px;letter-spacing:.8px;text-transform:uppercase;color:#8a95a7;font-weight:600}td,th{padding:13px 10px;border-bottom:1px solid var(--line);text-align:left;vertical-align:middle}td{font-size:12px}td:first-child{overflow-wrap:anywhere;max-width:380px}.file-tools{display:grid;grid-template-columns:1fr 1fr;gap:20px}.file-tools form{display:flex;align-items:end;gap:10px}.file-tools label{flex:1;margin:0}.file-tools input{min-width:0}.file-tools button{white-space:nowrap}.path{background:#f7f9fc;padding:11px 14px;border-radius:8px;font-size:12px}.hint{font-size:12px;color:var(--muted)}@media(min-width:1600px){.workspace{padding:40px 55px}}@media(max-width:1150px){.workspace{padding:28px 25px}.shell{grid-template-columns:185px minmax(0,1fr)}.sidebar{padding:28px 15px}main.dashboard{grid-template-columns:minmax(0,1fr) 270px;gap:18px}.site-head{padding:18px}.site-actions{padding:14px 18px}.site-details{padding:0 18px 14px}.roadmap-grid{grid-template-columns:repeat(3,1fr)}.file-tools{grid-template-columns:1fr}}@media(max-width:900px){main.dashboard{display:flex;flex-direction:column}.create-card{position:static;width:100%;order:2}.sites-section{width:100%;order:1}.overview{gap:10px}.metric{padding:16px}.metric strong{font-size:20px}}@media(max-width:650px){.shell{display:block}.sidebar{position:static;height:auto;padding:14px 20px;display:flex;flex-direction:row;flex-wrap:wrap;align-items:center;gap:10px;border-right:0;border-bottom:1px solid var(--line)}.brand{font-size:18px;margin:0}.mark{width:30px;height:30px}.sidebar nav{display:flex;margin-left:auto}.sidebar .eyebrow,.sidebar-footer p,.sidebar .roadmap-nav{display:none}.sidebar-footer{margin:0;padding:0}.sidebar-footer button{font-size:11px;padding:7px 10px}.nav-link{font-size:11px;padding:7px 10px;margin:0}.workspace{padding:24px 16px}.topbar{padding-bottom:22px}.topbar h1{font-size:24px}.admin-chip{display:none}.overview{grid-template-columns:1fr 1fr}.overview .metric:last-child{display:none}.metric strong{font-size:18px}.site-head{gap:10px}.site-icon{display:none}.badge{font-size:9px;padding:3px 7px}.roadmap-grid{grid-template-columns:1fr 1fr}.file-tools form{display:block}.file-tools button{margin-top:12px}.section-head{align-items:flex-start}section{padding:20px}.login-card{padding:28px}.site-actions .button{font-size:12px;padding:7px 11px}}
</style></head><body><div class="shell"><aside class="sidebar"><a class="brand" href="/"><span class="mark">p</span>panel4wp</a><nav><div class="eyebrow">Workspace</div><a class="nav-link active" href="/">▦ &nbsp; My sites</a><a class="nav-link" href="/#settings">⚙ &nbsp; Settings</a><a class="nav-link roadmap-nav" href="/#roadmap">◷ &nbsp; What's next</a></nav><div class="sidebar-footer"><p>Open source.<br>Your server. Your sites.</p><form action="/logout" method="post"><input type="hidden" name="csrf" value="{{.CSRF}}"><button class="secondary">Log out</button></form></div></aside><div class="workspace"><header class="file-topbar"><a href="/">← Back to your sites</a><div class="eyebrow" style="margin-top:24px">Site workspace</div><h1>File manager</h1><p class="muted">{{.Site.Domain}} · wp-content</p></header>
{{if .Error}}<p class="error">{{.Error}}</p>{{end}}{{if .Message}}<p class="success">{{.Message}}</p>{{end}}
<section><p class="path">Path: <code>/wp-content{{if .CurrentPath}}/{{.CurrentPath}}{{end}}</code></p>{{if .HasParent}}<p><a href="/sites/{{.Site.ID}}/files?path={{urlquery .Parent}}">↑ Parent directory</a></p>{{end}}
<div class="table-wrap"><table><thead><tr><th>Name</th><th>Size</th><th>Modified</th><th>Actions</th></tr></thead><tbody>{{range .Entries}}<tr><td>{{if eq .Type "directory"}}<a href="/sites/{{$.Site.ID}}/files?path={{urlquery .Path}}">📁 {{.Name}}</a>{{else}}{{if eq .Type "file"}}📄 {{.Name}}{{else}}🔗 {{.Name}}{{end}}{{end}}</td><td>{{if eq .Type "file"}}{{.Size}} bytes{{end}}</td><td>{{formatTime .Modified}}</td><td>{{if eq .Type "file"}}<form class="inline" action="/sites/{{$.Site.ID}}/download" method="post"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="path" value="{{.Path}}"><button>Download</button></form>{{end}}{{if ne .Type "link"}} <details class="danger-zone"><summary>Delete</summary><p class="hint">Permanently delete {{.Name}}?</p><form class="inline" action="/sites/{{$.Site.ID}}/file-delete" method="post"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="path" value="{{.Path}}"><button class="danger">Confirm delete</button></form></details>{{end}}</td></tr>{{else}}<tr><td colspan="4">This directory is empty.</td></tr>{{end}}</tbody></table></div></section>
<div class="file-tools"><section><h2>Upload a file</h2><form action="/sites/{{.Site.ID}}/upload" method="post" enctype="multipart/form-data"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="path" value="{{.CurrentPath}}"><label>Select a file<input type="file" name="file" required></label><button>Upload · max 10 MB</button></form></section>
<section><h2>Create directory</h2><form action="/sites/{{.Site.ID}}/mkdir" method="post"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="path" value="{{.CurrentPath}}"><label>Directory name<input name="name" required maxlength="120" placeholder="directory-name"></label><button>Create</button></form></section>
</div><p class="hint">Access is restricted to this site's <code>wp-content</code>. Symbolic links cannot be downloaded, modified, or deleted.</p></div></div></body></html>`))

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
}

type view struct {
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
	if err := a.setFlash(w, message, flashError); err != nil {
		http.Error(w, "unable to create result message", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *app) render(w http.ResponseWriter, r *http.Request, v view) {
	if v.LoggedIn {
		sites, err := a.store.List()
		if err != nil {
			http.Error(w, "unable to read sites", http.StatusInternalServerError)
			return
		}
		v.Sites = sites
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
		if cookie.Name != "wph_session" && cookie.Name != "wph_flash" {
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

func (a *app) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/health" {
		w.WriteHeader(http.StatusNoContent)
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
	if strings.Contains(r.URL.Path, "/database/") && strings.HasPrefix(r.URL.Path, "/sites/") {
		a.proxyDatabase(w, r)
		return
	}
	if r.Method == http.MethodGet {
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
			a.redirectWithFlash(w, r, "Database manager stopped", "")
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
		a.redirectWithFlash(w, r, "Outgoing mail settings updated for "+site.Domain, "")
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
			a.redirectWithFlash(w, r, "Site creation retried. WordPress username: admin. New password (save now): "+adminPassword, "")
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
			a.redirectWithFlash(w, r, "Site action completed", "")
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
	a.redirectWithFlash(w, r, "Backup completed and verified", "")
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
	a.redirectWithFlash(w, r, "WordPress core, database, plugins, and themes updated. Safety backup retained: "+backupID, "")
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
	a.redirectWithFlash(w, r, "Backup restored. Safety backup retained: "+safetyID, "")
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
	if err := filesPage.Execute(w, filesView{Site: site, CSRF: csrf, CurrentPath: current, Parent: parent, HasParent: hasParent, Entries: items, Error: viewError, Message: message}); err != nil {
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
		a.redirectWithFlash(w, r, "Mail settings saved", fmt.Sprintf("Could not apply settings to %d running sites", failed))
		return
	}
	a.redirectWithFlash(w, r, "Mail settings saved and applied", "")
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
