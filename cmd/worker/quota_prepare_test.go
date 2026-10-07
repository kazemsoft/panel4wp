package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type preparationDocker struct {
	quotaDocker
	reject    bool
	foreign   bool
	helpers   int
	tags      int
	orphan    bool
	cancelTag bool
}

func (f *preparationDocker) Output(ctx context.Context, args ...string) ([]byte, error) {
	joined := strings.Join(args, " ")
	if args[0] == "ps" && strings.Contains(joined, "label=panel4wp.role=job") && f.orphan {
		return []byte(`{"name":"wph-job-` + quotaTestID + `","site":"` + quotaTestID + `","role":"job"}`), nil
	}
	if args[0] == "info" {
		return []byte("fixture-daemon-identity"), nil
	}
	if args[0] == "ps" && strings.Contains(joined, "--filter volume=") {
		if f.foreign {
			return []byte("foreign-writer"), nil
		}
		return nil, nil
	}
	if args[0] == "run" && strings.Contains(joined, "storage-authority") {
		f.helpers++
		if !f.stopped || f.restart != "no" {
			panic("helper launched before fencing")
		}
		if f.reject {
			return []byte(`{"verified":false,"reason":"authority-owner"}`), nil
		}
		if strings.Contains(joined, " --tag ") {
			if strings.Contains(joined, "dst=/quota-wordpress,readonly") || !strings.Contains(joined, "--cap-add FOWNER") || !strings.Contains(joined, "--name wph-job-"+quotaTestID) || !strings.Contains(joined, "--label panel4wp.role=job") {
				panic("incorrect mutating helper access/ownership")
			}
			f.tags++
			if f.cancelTag {
				f.orphan = true
				return nil, context.Canceled
			}
			return []byte(`{"verified":true,"reason":"tagged"}`), nil
		}
		return []byte(`{"verified":true,"reason":"prepared"}`), nil
	}
	if args[0] == "run" && args[len(args)-1] == "--inventory" {
		return []byte(`{"verified":true,"reason":"verified","filesystem_uuid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","scanned_inodes":20,"wordpress_inodes":2,"database_inodes":1,"project_ids":[]}`), nil
	}
	return f.quotaDocker.Output(ctx, args...)
}

func (f *preparationDocker) Run(ctx context.Context, args ...string) error {
	if args[0] == "rm" && args[len(args)-1] == "wph-job-"+quotaTestID {
		f.orphan = false
	}
	return f.quotaDocker.Run(ctx, args...)
}
func preparationWorker(t *testing.T) (*worker, *preparationDocker) {
	t.Helper()
	f := &preparationDocker{quotaDocker: quotaDocker{restart: "unless-stopped"}}
	w := quotaWorker(t, &f.quotaDocker)
	w.docker = f
	w.quotaRoot = "/dedicated-xfs"
	if err := os.Remove(filepath.Join(w.siteDir(quotaTestID), "quota.json")); err != nil {
		t.Fatal(err)
	}
	return w, f
}
func TestQuotaPreparationFencesAndPersistsRetryStableJournal(t *testing.T) {
	w, f := preparationWorker(t)
	if err := w.prepareQuotaTagging(context.Background(), quotaTestID); err != nil {
		t.Fatal(err)
	}
	plan, err := w.readQuotaPlan(quotaTestID)
	if err != nil || plan == nil || plan.Phase != "prepared" {
		t.Fatal(plan, err)
	}
	if !f.stopped || f.restart != "no" || f.helpers != 1 {
		t.Fatal("writers were not fenced", f)
	}
	if guard, err := w.loadQuotaGuard(quotaTestID); err == nil || guard != nil {
		t.Fatal("preparation enabled quotas", guard, err)
	}
	original, _ := os.ReadFile(filepath.Join(w.siteDir(quotaTestID), "quota-plan.json"))
	if err := w.prepareQuotaTagging(context.Background(), quotaTestID); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(filepath.Join(w.siteDir(quotaTestID), "quota-plan.json"))
	if string(original) != string(again) {
		t.Fatal("retry changed journal")
	}
}
func TestQuotaPreparationFailuresAndLostMarkersStayFenced(t *testing.T) {
	for _, failure := range []string{"native-reject", "foreign-writer", "missing-authority", "missing-quota-marker", "malformed-plan", "nil-plan", "missing-root"} {
		t.Run(failure, func(t *testing.T) {
			w, f := preparationWorker(t)
			if err := w.prepareQuotaTagging(context.Background(), quotaTestID); err != nil {
				t.Fatal(err)
			}
			plan, _ := w.readQuotaPlan(quotaTestID)
			switch failure {
			case "native-reject":
				f.reject = true
			case "foreign-writer":
				f.foreign = true
			case "missing-authority":
				os.Remove(filepath.Join(w.root, ".quota", "authority-"+plan.UUID+".json"))
			case "missing-quota-marker":
				os.Remove(filepath.Join(w.siteDir(quotaTestID), "quota.json"))
			case "malformed-plan":
				os.WriteFile(filepath.Join(w.siteDir(quotaTestID), "quota-plan.json"), []byte(`{"version":1}`), 0600)
			case "nil-plan":
				os.WriteFile(filepath.Join(w.siteDir(quotaTestID), "quota-plan.json"), []byte(`null`), 0600)
			case "missing-root":
				w.quotaRoot = ""
			}
			if failure != "missing-quota-marker" {
				if err := w.prepareQuotaTagging(context.Background(), quotaTestID); err == nil {
					t.Fatal("unsafe preparation accepted")
				}
			}
			if err := w.quotaPreflight(context.Background(), quotaTestID); err == nil {
				t.Fatal("pending writer allowed")
			}
			if err := w.reconcileQuotaSites(context.Background(), true); err != nil {
				t.Fatal(err)
			}
			if !f.stopped || hasQuotaStart(&f.quotaDocker) {
				t.Fatal("pending site resumed")
			}
		})
	}
}
func TestQuotaPreparationRejectsCopiedOrAlteredAuthority(t *testing.T) {
	w, _ := preparationWorker(t)
	if err := w.prepareQuotaTagging(context.Background(), quotaTestID); err != nil {
		t.Fatal(err)
	}
	plan, _ := w.readQuotaPlan(quotaTestID)
	path := filepath.Join(w.root, ".quota", "authority-"+plan.UUID+".json")
	authority := plan.quotaAuthority
	authority.State = quotaHash("another-state-root")
	body, _ := json.Marshal(authority)
	os.WriteFile(path, body, 0600)
	if err := w.prepareQuotaTagging(context.Background(), quotaTestID); err == nil {
		t.Fatal("copied authority accepted")
	}
}

func TestQuotaPreparationFirstMarkerFailureFencesWriters(t *testing.T) {
	w, f := preparationWorker(t)
	if err := os.Mkdir(filepath.Join(w.siteDir(quotaTestID), "quota.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := w.prepareQuotaTagging(context.Background(), quotaTestID); err == nil {
		t.Fatal("marker persistence failure accepted")
	}
	if !f.stopped || f.restart != "no" || f.helpers != 0 {
		t.Fatal("marker failure did not stop writers before helper", f)
	}
}

func TestQuotaTaggingPersistsPendingFenceAndRetryState(t *testing.T) {
	w, f := preparationWorker(t)
	if err := w.prepareQuotaTagging(context.Background(), quotaTestID); err != nil {
		t.Fatal(err)
	}
	f.cancelTag = true
	if err := w.tagQuotaData(context.Background(), quotaTestID); err == nil {
		t.Fatal("cancelled tagging accepted")
	}
	plan, err := w.readQuotaPlan(quotaTestID)
	if err != nil || plan.Phase != "tagging" || f.orphan || !f.stopped {
		t.Fatal("failed tagging not fenced", plan, err, f)
	}
	f.cancelTag = false
	if err := w.tagQuotaData(context.Background(), quotaTestID); err != nil {
		t.Fatal(err)
	}
	if err := w.tagQuotaData(context.Background(), quotaTestID); err != nil {
		t.Fatal("tag retry", err)
	}
	plan, err = w.readQuotaPlan(quotaTestID)
	if err != nil || plan.Phase != "tagged" || f.tags != 3 {
		t.Fatal(plan, err, f)
	}
	if guard, err := w.loadQuotaGuard(quotaTestID); err == nil || guard != nil {
		t.Fatal("tagging enabled quota", guard, err)
	}
	if err := w.reconcileQuotaSites(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if hasQuotaStart(&f.quotaDocker) {
		t.Fatal("tagged site resumed")
	}
}

func TestQuotaTaggingRejectsMissingPreparationAndAlteredGuard(t *testing.T) {
	for _, failure := range []string{"no-plan", "guard-missing", "guard-limit", "malformed-plan", "root-missing", "native-reject"} {
		t.Run(failure, func(t *testing.T) {
			w, f := preparationWorker(t)
			if failure != "no-plan" {
				if err := w.prepareQuotaTagging(context.Background(), quotaTestID); err != nil {
					t.Fatal(err)
				}
			}
			switch failure {
			case "guard-missing":
				os.Remove(filepath.Join(w.siteDir(quotaTestID), "quota.json"))
			case "guard-limit":
				data, _ := os.ReadFile(filepath.Join(w.siteDir(quotaTestID), "quota.json"))
				var guard quotaGuard
				json.Unmarshal(data, &guard)
				guard.HardBytes = 512
				data, _ = json.Marshal(guard)
				os.WriteFile(filepath.Join(w.siteDir(quotaTestID), "quota.json"), data, 0600)
			case "malformed-plan":
				os.WriteFile(filepath.Join(w.siteDir(quotaTestID), "quota-plan.json"), []byte(`null`), 0600)
			case "root-missing":
				w.quotaRoot = ""
			case "native-reject":
				f.reject = true
			}
			f.orphan = true
			if err := w.tagQuotaData(context.Background(), quotaTestID); err == nil {
				t.Fatal("unsafe tagging accepted")
			}
			if f.orphan || !f.stopped || hasQuotaStart(&f.quotaDocker) {
				t.Fatal("rejected tagging left writers active", f)
			}
		})
	}
}
