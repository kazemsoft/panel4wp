package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kazemsoft/panel4wp/internal/core"
)

type runtimeDocker struct {
	fakeDocker
	states string
	fail   bool
}

func (f *runtimeDocker) Output(_ context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	if f.fail {
		return nil, fmt.Errorf("Docker unavailable")
	}
	if args[0] == "ps" && (!strings.Contains(strings.Join(args, " "), "project=wph-") || args[1] != "-a") {
		return nil, nil
	}
	if args[0] == "ps" {
		return []byte("aabbccdd\neeff0011"), nil
	}
	return []byte(f.states), nil
}
func runtimeStates(wp, db, health string) string {
	return fmt.Sprintf(`{"Name":"/wph-wp-0123456789abcdef","Project":"wph-0123456789abcdef","Service":"wordpress","Status":%q}
{"Name":"/wph-db-0123456789abcdef","Project":"wph-0123456789abcdef","Service":"db","Status":%q,"Health":%q}`, wp, db, health)
}
func TestRuntimeUsesContainerStateAndProjectOwnership(t *testing.T) {
	for _, tc := range []struct {
		wp, db, health string
		want           core.Status
	}{
		{"running", "running", "healthy", core.StatusRunning},
		{"exited", "exited", "", core.StatusStopped},
		{"exited", "running", "healthy", core.StatusUnhealthy},
		{"running", "running", "unhealthy", core.StatusUnhealthy},
		{"running", "restarting", "", core.StatusUnhealthy},
	} {
		t.Run(tc.wp+tc.db+tc.health, func(t *testing.T) {
			f := &runtimeDocker{states: runtimeStates(tc.wp, tc.db, tc.health)}
			base := t.TempDir()
			w := &worker{root: filepath.Join(base, "sites"), routes: base, docker: f}
			os.WriteFile(filepath.Join(base, "0123456789abcdef.caddy"), []byte("route"), 0600)
			got, err := w.runtime(context.Background(), "0123456789abcdef")
			if err != nil || got.Status != tc.want {
				t.Fatalf("runtime: %#v %v", got, err)
			}
			f.states = strings.ReplaceAll(f.states, "wph-0123456789abcdef", "wph-ffffffffffffffff")
			got, err = w.runtime(context.Background(), "0123456789abcdef")
			if err != nil || got.Status != core.StatusUnhealthy {
				t.Fatal("another project's services were accepted")
			}
		})
	}
}
func TestWorkerRestartResolvesUnfinishedReceiptWithoutReplay(t *testing.T) {
	base := t.TempDir()
	id := "0123456789abcdef"
	w := &worker{root: base}
	op := core.Operation{ID: "1111111111111111", Kind: "restore", State: "pending", StartedAt: time.Now()}
	if err := w.saveOperation(id, op); err != nil {
		t.Fatal(err)
	}
	f := &runtimeDocker{states: runtimeStates("exited", "exited", "")}
	restarted := &worker{root: base, docker: f}
	got, err := restarted.runtime(context.Background(), id)
	if err != nil || got.Operation.State != "interrupted" {
		t.Fatalf("receipt not recovered: %#v %v", got, err)
	}
	for _, call := range f.calls {
		if call[0] != "ps" && call[0] != "inspect" {
			t.Fatalf("replayed mutation: %v", call)
		}
	}
	persisted, _ := w.loadOperation(id)
	if persisted.State != "interrupted" {
		t.Fatal("interruption was not durable")
	}
}
func TestWorkerCommitsReceiptBeforeReplyAndRejectsReplay(t *testing.T) {
	base := t.TempDir()
	id := "0123456789abcdef"
	w := &worker{root: base, token: strings.Repeat("t", 64), docker: &fakeDocker{}}
	os.MkdirAll(w.siteDir(id), 0700)
	os.WriteFile(filepath.Join(w.siteDir(id), "compose.yaml"), []byte(fmt.Sprintf(composeTemplate, id, 512, 1., id, 512, 1., id, "http://localhost")), 0600)
	run := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/action", strings.NewReader(`{"id":"0123456789abcdef","action":"start"}`))
		r.Header.Set("X-Worker-Token", w.token)
		r.Header.Set("X-Operation-ID", "1111111111111111")
		resp := httptest.NewRecorder()
		w.ServeHTTP(resp, r)
		return resp
	}
	if resp := run(); resp.Code != 204 {
		t.Fatalf("operation failed: %d %s", resp.Code, resp.Body.String())
	}
	op, err := w.loadOperation(id)
	if err != nil || op.State != "succeeded" {
		t.Fatalf("receipt unavailable: %#v %v", op, err)
	}
	if resp := run(); resp.Code != 409 {
		t.Fatal("duplicate operation executed")
	}
	w.executionMu.Lock()
	r := httptest.NewRequest("POST", "/runtime", strings.NewReader(`{"site_id":"0123456789abcdef"}`))
	r.Header.Set("X-Worker-Token", w.token)
	resp := httptest.NewRecorder()
	w.ServeHTTP(resp, r)
	w.executionMu.Unlock()
	if resp.Code != 409 {
		t.Fatal("runtime inspected during active mutation")
	}
}
func TestWorkerStartupClosesPreviousDatabaseSessions(t *testing.T) {
	f := &outputDocker{output: []byte(`{"name":"wph-pma-0123456789abcdef","project":"wph-0123456789abcdef"}
{"name":"unrelated-pma","project":"another-project"}`)}
	w := &worker{root: t.TempDir(), docker: f}
	if err := w.removeStaleDatabaseTools(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 2 || strings.Join(f.calls[1], " ") != "rm -f wph-pma-0123456789abcdef" {
		t.Fatalf("unexpected startup action: %v", f.calls)
	}
}
func TestIncompleteSiteDoesNotPreventWorkerStartup(t *testing.T) {
	base := t.TempDir()
	w := &worker{root: base, docker: &fakeDocker{}}
	os.MkdirAll(w.siteDir("0123456789abcdef"), 0700)
	if err := w.migrateLegacyNetworks(context.Background()); err != nil {
		t.Fatal("incomplete install prevented worker startup", err)
	}
}
func TestFileCommandsRunAsUnprivilegedUserAndConstrainFilesystem(t *testing.T) {
	base := t.TempDir()
	f := &fakeDocker{}
	w := &worker{root: base, docker: f}
	id := "0123456789abcdef"
	os.MkdirAll(w.siteDir(id), 0700)
	os.WriteFile(filepath.Join(w.siteDir(id), "compose.yaml"), []byte("compose"), 0600)
	if err := w.writeFile(context.Background(), core.FileRequest{SiteID: id, Path: "file.txt", Content: []byte("test")}); err != nil {
		t.Fatal(err)
	}
	args := strings.Join(f.calls[0], " ")
	if !strings.Contains(args, "--user 33:33") || !strings.Contains(args, "open_basedir=/var/www/html/wp-content") {
		t.Fatal("file write still runs with unrestricted root access")
	}
}

type failedRestoreDocker struct{ fakeDocker }

func (f *failedRestoreDocker) Run(ctx context.Context, args ...string) error {
	f.fakeDocker.Run(ctx, args...)
	if strings.Contains(strings.Join(args, " "), "tar -xzf") {
		return fmt.Errorf("injected restore failure")
	}
	return nil
}
func TestFailedRestoreKeepsWordPressStopped(t *testing.T) {
	base := t.TempDir()
	id := "0123456789abcdef"
	f := &failedRestoreDocker{}
	w := &worker{root: filepath.Join(base, "sites"), backupsRoot: filepath.Join(base, "backups"), docker: f}
	for _, backupID := range []string{"20261005T120000Z-12345678", "20261005T120001Z-12345678"} {
		dir := w.backupDir(id, backupID)
		os.MkdirAll(dir, 0700)
		os.WriteFile(filepath.Join(dir, "database.sql"), []byte("SQL"), 0600)
		os.WriteFile(filepath.Join(dir, "wordpress.tar.gz"), []byte("archive"), 0600)
		db, _ := fileSHA256(filepath.Join(dir, "database.sql"))
		wp, _ := fileSHA256(filepath.Join(dir, "wordpress.tar.gz"))
		manifest, _ := json.Marshal(backupManifest{Version: 1, SiteID: id, DatabaseSHA256: db, WordPressSHA256: wp})
		os.WriteFile(filepath.Join(dir, "manifest.json"), manifest, 0600)
	}
	site := core.Site{ID: id, Domain: "test.localhost", Title: "Test", AdminEmail: "test@example.com"}
	err := w.restore(context.Background(), core.RestoreRequest{Site: site, BackupID: "20261005T120000Z-12345678", SafetyBackupID: "20261005T120001Z-12345678"})
	if err == nil {
		t.Fatal("injected restore did not fail")
	}
	if len(f.calls) != 2 || !strings.Contains(strings.Join(f.calls[0], " "), "stop wordpress") {
		t.Fatalf("failed restore restarted public WordPress: %v", f.calls)
	}
}

type orphanJobDocker struct{ fakeDocker }

func (f *orphanJobDocker) Output(ctx context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	switch {
	case strings.Contains(strings.Join(args, " "), "panel4wp.role=job"):
		return []byte(`{"Name":"wph-job-0123456789abcdef","Site":"0123456789abcdef","Role":"job"}
{"Name":"other-job","Site":"0123456789abcdef","Role":"job"}`), nil
	case strings.Contains(strings.Join(args, " "), "service=cli"):
		return []byte(`{"Name":"wph-cli-0123456789abcdef","Project":"wph-0123456789abcdef","Service":"cli"}`), nil
	default:
		return []byte(`{"Name":"wph-db-0123456789abcdef","Project":"wph-0123456789abcdef","Service":"db"}
{"Name":"wph-wp-0123456789abcdef","Project":"wph-0123456789abcdef","Service":"wordpress"}`), nil
	}
}
func TestInterruptedRestoreFencesOwnedJobsAndDatabaseBeforeRecovery(t *testing.T) {
	id := "0123456789abcdef"
	f := &orphanJobDocker{}
	w := &worker{root: t.TempDir(), docker: f}
	if err := w.saveOperation(id, core.Operation{ID: "1111111111111111", Kind: "restore", State: "pending"}); err != nil {
		t.Fatal(err)
	}
	if err := w.recoverInterruptedJobs(context.Background()); err != nil {
		t.Fatal(err)
	}
	op, _ := w.loadOperation(id)
	if op.State != "interrupted" {
		t.Fatal("receipt not fenced")
	}
	stopped := []string{}
	for _, call := range f.calls {
		if call[0] == "rm" && call[len(call)-1] == "other-job" {
			t.Fatal("removed unrelated helper")
		}
		if call[0] == "stop" {
			stopped = append(stopped, call[len(call)-1])
		}
	}
	if strings.Join(stopped, ",") != "wph-wp-"+id+",wph-db-"+id {
		t.Fatalf("old import not fenced before recovery: %v", stopped)
	}
}
