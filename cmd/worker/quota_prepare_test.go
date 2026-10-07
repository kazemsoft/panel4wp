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
	reject  bool
	foreign bool
	helpers int
}

func (f *preparationDocker) Output(ctx context.Context, args ...string) ([]byte, error) {
	joined := strings.Join(args, " ")
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
		return []byte(`{"verified":true,"reason":"prepared"}`), nil
	}
	if args[0] == "run" && args[len(args)-1] == "--inventory" {
		return []byte(`{"verified":true,"reason":"verified","filesystem_uuid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","scanned_inodes":20,"wordpress_inodes":2,"database_inodes":1,"project_ids":[]}`), nil
	}
	return f.quotaDocker.Output(ctx, args...)
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
