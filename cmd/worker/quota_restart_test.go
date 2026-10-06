package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kazemsoft/panel4wp/internal/core"
	"github.com/kazemsoft/panel4wp/internal/securitypolicy"
)

const quotaTestID = "0123456789abcdef"

type quotaDocker struct {
	resourceDocker
	restart     string
	denyProbe   bool
	failInspect bool
	failStart   bool
	wpStopped   bool
}

func (f *quotaDocker) Output(ctx context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if args[0] == "volume" {
		return []byte(fmt.Sprintf(`{"name":"wph-%s_wordpress_data","driver":"local","project":"wph-%s","volume":"wordpress_data","options":false}
{"name":"wph-%s_database_data","driver":"local","project":"wph-%s","volume":"database_data","options":false}`, quotaTestID, quotaTestID, quotaTestID, quotaTestID)), nil
	}
	if args[0] == "run" && strings.Contains(strings.Join(args, " "), "storage-probe") {
		if f.denyProbe {
			return []byte(`{"verified":false,"reason":"enforcement-disabled"}`), nil
		}
		return []byte(`{"verified":true,"reason":"verified"}`), nil
	}
	if args[0] == "inspect" && strings.Contains(strings.Join(args, " "), "security_opts") {
		if f.failInspect {
			return nil, errors.New("inspect unavailable")
		}
		status := "running"
		if f.stopped {
			status = "exited"
		}
		wpStatus := status
		if f.wpStopped {
			wpStatus = "exited"
		}
		opts := jsonOptions([]string{"seccomp=" + string(securitypolicy.Bytes()), "no-new-privileges:true"})
		return []byte(fmt.Sprintf(`{"name":"/wph-wp-%s","project":"wph-%s","service":"wordpress","status":%q,"restart":%q,"security_opts":%s,"memory":536870912,"cpus":1000000000}
{"name":"/wph-db-%s","project":"wph-%s","service":"db","status":%q,"restart":%q,"security_opts":%s,"memory":536870912,"cpus":1000000000}
{"name":"/wph-wp-ffffffffffffffff","project":"foreign","service":"wordpress","status":"running"}`, quotaTestID, quotaTestID, wpStatus, f.restart, opts, quotaTestID, quotaTestID, status, f.restart, opts)), nil
	}
	return f.resourceDocker.Output(ctx, args...)
}
func (f *quotaDocker) Run(ctx context.Context, args ...string) error {
	f.calls = append(f.calls, args)
	if err := ctx.Err(); err != nil {
		return err
	}
	if args[0] == "update" {
		f.restart = "no"
	}
	if args[0] == "stop" || strings.Contains(strings.Join(args, " "), " stop ") {
		f.stopped = true
	}
	if strings.Contains(strings.Join(args, " "), "up -d --wait wordpress db") {
		if f.failStart {
			return errors.New("injected compose failure")
		}
		f.stopped = false
		f.restart = "no"
	}
	return nil
}
func quotaWorker(t *testing.T, f *quotaDocker) *worker {
	t.Helper()
	w := resourceWorker(t, &f.resourceDocker)
	w.docker = f
	w.storageImage = "sha256:" + strings.Repeat("a", 64)
	guard := quotaGuard{Version: 1, Phase: "ready", ProjectID: 123, HardBytes: 8 << 20, FilesystemUUID: strings.Repeat("b", 32)}
	body, _ := json.Marshal(guard)
	if err := os.WriteFile(filepath.Join(w.siteDir(quotaTestID), "quota.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	return w
}
func hasQuotaStart(f *quotaDocker) bool {
	for _, call := range f.calls {
		if strings.Contains(strings.Join(call, " "), "up -d --wait") {
			return true
		}
	}
	return false
}
func TestQuotaExplicitStartFencesThenVerifiesAndPersistsIntent(t *testing.T) {
	f := &quotaDocker{restart: "unless-stopped"}
	w := quotaWorker(t, f)
	// Explicit Start is allowed to recover despite its own pending receipt.
	if err := w.saveOperation(quotaTestID, core.Operation{ID: "1111111111111111", Kind: "start", State: "pending"}); err != nil {
		t.Fatal(err)
	}
	if err := w.action(context.Background(), quotaTestID, "start"); err != nil {
		t.Fatal(err)
	}
	intent, err := w.loadQuotaIntent(quotaTestID)
	if err != nil || intent.State != "running" {
		t.Fatal(intent, err)
	}
	config, _ := os.ReadFile(filepath.Join(w.siteDir(quotaTestID), "compose.yaml"))
	if strings.Contains(string(config), "unless-stopped") {
		t.Fatal("daemon can bypass gate")
	}
	probe, up := -1, -1
	for index, args := range f.calls {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "ffffffffffffffff") && args[0] != "inspect" {
			t.Fatal("foreign container mutated")
		}
		if strings.Contains(joined, "--guard") && probe < 0 {
			probe = index
		}
		if strings.Contains(joined, "up -d --wait wordpress db") {
			up = index
		}
	}
	if probe < 0 || up <= probe {
		t.Fatal("writer launched before preflight", probe, up)
	}
	// Ordinary Stop persists independently of observed runtime status.
	if err := w.action(context.Background(), quotaTestID, "stop"); err != nil {
		t.Fatal(err)
	}
	intent, _ = w.loadQuotaIntent(quotaTestID)
	if intent.State != "stopped" {
		t.Fatal("Stop not durable")
	}
}
func TestQuotaFailuresNeverStartAndBlockAllWriters(t *testing.T) {
	for _, failure := range []string{"probe", "inspect", "start", "malformed", "pending", "dangling"} {
		t.Run(failure, func(t *testing.T) {
			f := &quotaDocker{restart: "no"}
			w := quotaWorker(t, f)
			path := filepath.Join(w.siteDir(quotaTestID), "quota.json")
			switch failure {
			case "probe":
				f.denyProbe = true
			case "inspect":
				f.failInspect = true
			case "start":
				f.failStart = true
			case "malformed":
				os.WriteFile(path, []byte("{"), 0600)
			case "pending":
				os.WriteFile(path, []byte(`{"version":1,"phase":"pending"}`), 0600)
			case "dangling":
				os.Remove(path)
				os.Symlink(filepath.Join(t.TempDir(), "missing"), path)
			}
			if err := w.action(context.Background(), quotaTestID, "start"); err == nil {
				t.Fatal("unsafe start allowed")
			}
			if failure != "start" && hasQuotaStart(f) {
				t.Fatal("writer launched despite failed verification")
			}
			intent, _ := w.loadQuotaIntent(quotaTestID)
			if intent.State != "blocked" {
				t.Fatal("failure may auto resume")
			}
			if failure != "inspect" && !f.stopped {
				t.Fatal("failure left writer running")
			}
		})
	}
}
func TestQuotaSupervisorHonorsIntentAndReceipts(t *testing.T) {
	for _, tc := range []struct {
		intent, receipt string
		resume          bool
	}{
		{"running", "succeeded", true}, {"running", "", true},
		{"stopped", "succeeded", false}, {"blocked", "succeeded", false},
		{"running", "pending", false}, {"running", "failed", false}, {"running", "interrupted", false},
	} {
		t.Run(tc.intent+tc.receipt, func(t *testing.T) {
			f := &quotaDocker{restart: "no", resourceDocker: resourceDocker{stopped: true}}
			w := quotaWorker(t, f)
			w.saveQuotaIntent(quotaTestID, tc.intent)
			if tc.receipt != "" {
				w.saveOperation(quotaTestID, core.Operation{ID: "1111111111111111", Kind: "restore", State: tc.receipt})
			}
			if err := w.reconcileQuotaSites(context.Background(), true); err != nil {
				t.Fatal(err)
			}
			if hasQuotaStart(f) != tc.resume {
				t.Fatal("unexpected resume decision", f.calls)
			}
			// Repeat with a new worker to prove no transient flag supplies intent.
			restarted := &worker{root: w.root, docker: w.docker, storageImage: w.storageImage}
			f.calls = nil
			if err := restarted.reconcileQuotaSites(context.Background(), true); err != nil {
				t.Fatal(err)
			}
			if hasQuotaStart(f) {
				t.Fatal("idempotent supervisor restarted a healthy or blocked site")
			}
		})
	}
}
func TestQuotaSupervisorFencesLostEnforcement(t *testing.T) {
	f := &quotaDocker{restart: "no", denyProbe: true}
	w := quotaWorker(t, f)
	w.saveQuotaIntent(quotaTestID, "running")
	if err := w.reconcileQuotaSites(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if !f.stopped || hasQuotaStart(f) {
		t.Fatal("lost enforcement was ignored")
	}
	intent, _ := w.loadQuotaIntent(quotaTestID)
	if intent.State != "blocked" {
		t.Fatal("may restart later without consent")
	}
}
func TestQuotaGateCoversIndirectWriterLaunches(t *testing.T) {
	for _, kind := range []string{"pma", "prepare-restore", "update", "restore", "resources", "create"} {
		t.Run(kind, func(t *testing.T) {
			f := &quotaDocker{restart: "no", denyProbe: true}
			w := quotaWorker(t, f)
			site := resourceRequest().Site
			var err error
			switch kind {
			case "pma":
				err = w.databaseAction(context.Background(), quotaTestID, "start")
			case "prepare-restore":
				err = w.action(context.Background(), quotaTestID, "prepare-restore")
			case "update":
				err = w.updateSite(context.Background(), core.UpdateRequest{Site: site})
			case "restore":
				err = w.restore(context.Background(), core.RestoreRequest{Site: site})
			case "resources":
				_, err = w.changeResources(context.Background(), resourceRequest())
			case "create":
				err = w.create(context.Background(), core.CreateRequest{Site: site})
			}
			if err == nil || hasQuotaStart(f) {
				t.Fatal("indirect launch bypassed quota gate")
			}
		})
	}
}
func TestQuotaRestartRewritePreservesEverythingElse(t *testing.T) {
	original := []byte(fmt.Sprintf(composeTemplate, quotaTestID, 512, 1., quotaTestID, 512, 1., quotaTestID, "http://localhost"))
	updated, err := quotaRestartCompose(original)
	if err != nil {
		t.Fatal(err)
	}
	restored := strings.Replace(string(updated), `    restart: "no"`, "    restart: unless-stopped", 2)
	if restored != string(original) {
		t.Fatal("unrelated data configuration changed")
	}
	if _, err := quotaRestartCompose([]byte("services: {}")); err == nil {
		t.Fatal("unknown compose rewritten")
	}
}

func TestQuotaCancellationAndIntentWriteFailureStillFence(t *testing.T) {
	for _, failure := range []string{"cancelled", "intent-disk-error"} {
		t.Run(failure, func(t *testing.T) {
			f := &quotaDocker{restart: "unless-stopped"}
			w := quotaWorker(t, f)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if failure == "cancelled" {
				cancel()
			} else if err := os.Mkdir(filepath.Join(w.siteDir(quotaTestID), "quota-intent.json"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := w.action(ctx, quotaTestID, "start"); err == nil {
				t.Fatal("failure ignored")
			}
			if !f.stopped || f.restart != "no" || hasQuotaStart(f) {
				t.Fatal("cleanup depended on caller deadline or persistence", f.calls)
			}
		})
	}
}

func TestQuotaMaintenanceNeverResumesAfterWorkerRestart(t *testing.T) {
	for _, tc := range []struct{ startup, deny, stops bool }{{false, false, false}, {true, false, true}, {false, true, true}} {
		t.Run(fmt.Sprint(tc), func(t *testing.T) {
			f := &quotaDocker{restart: "no", denyProbe: tc.deny, wpStopped: true}
			w := quotaWorker(t, f)
			if err := w.saveQuotaIntent(quotaTestID, "maintenance"); err != nil {
				t.Fatal(err)
			}
			if err := w.reconcileQuotaSites(context.Background(), tc.startup); err != nil {
				t.Fatal(err)
			}
			if f.stopped != tc.stops || hasQuotaStart(f) {
				t.Fatal("maintenance restart/periodic fence incorrect", f.calls)
			}
			intent, _ := w.loadQuotaIntent(quotaTestID)
			if tc.stops && intent.State != "blocked" {
				t.Fatal("unsafe maintenance may resume")
			}
		})
	}
}

func TestQuotaRejectedCapacityPreservesRunningIntentAcrossRestart(t *testing.T) {
	f := &quotaDocker{restart: "no", resourceDocker: resourceDocker{low: true}}
	w := quotaWorker(t, f)
	if err := w.ensureQuotaRestartCompose(quotaTestID); err != nil {
		t.Fatal(err)
	}
	if err := w.saveQuotaIntent(quotaTestID, "running"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(w.siteDir(quotaTestID), "compose.yaml")
	before, _ := os.ReadFile(path)
	raw, _ := json.Marshal(resourceRequest())
	r := httptest.NewRequest("POST", "/resources", strings.NewReader(string(raw)))
	r.Header.Set("X-Worker-Token", w.token)
	r.Header.Set("X-Operation-ID", "1111111111111111")
	resp := httptest.NewRecorder()
	w.ServeHTTP(resp, r)
	if resp.Code != 500 || !strings.Contains(resp.Body.String(), "insufficient RAM") {
		t.Fatal("missing capacity rejection", resp.Code, resp.Body.String())
	}
	op, _ := w.loadOperation(quotaTestID)
	if op == nil || op.State != "failed" || !op.AdmissionRejected {
		t.Fatal("rejection not certified", op)
	}
	for _, startup := range []bool{false, true} {
		if err := w.reconcileQuotaSites(context.Background(), startup); err != nil {
			t.Fatal(err)
		}
		intent, _ := w.loadQuotaIntent(quotaTestID)
		if f.stopped || hasQuotaStart(f) || intent.State != "running" {
			t.Fatal("admission rejection stopped healthy site", f.calls)
		}
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("rejected plan modified configuration")
	}
}

func TestQuotaGuardFailureCannotBecomeSafeAdmissionRejection(t *testing.T) {
	f := &quotaDocker{restart: "no", denyProbe: true, resourceDocker: resourceDocker{low: true}}
	w := quotaWorker(t, f)
	if err := w.saveQuotaIntent(quotaTestID, "running"); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(resourceRequest())
	r := httptest.NewRequest("POST", "/resources", strings.NewReader(string(raw)))
	r.Header.Set("X-Worker-Token", w.token)
	r.Header.Set("X-Operation-ID", "1111111111111111")
	resp := httptest.NewRecorder()
	w.ServeHTTP(resp, r)
	op, _ := w.loadOperation(quotaTestID)
	intent, _ := w.loadQuotaIntent(quotaTestID)
	if resp.Code != 500 || op == nil || op.State != "failed" || op.AdmissionRejected || !f.stopped || intent.State != "blocked" {
		t.Fatal("guard failure incorrectly certified as safe", op, intent, f.calls)
	}
}

func TestQuotaMissingRecordVerifierDoesNotPanic(t *testing.T) {
	w := &worker{}
	if err := w.verifyQuotaStorage(context.Background(), quotaTestID, nil); err == nil {
		t.Fatal("missing record accepted")
	}
}
