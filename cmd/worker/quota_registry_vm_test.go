package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Opt-in fixture owned by tests/quota-vm; never run against the host daemon.
func TestQuotaRegistryNativeVM(t *testing.T) {
	phase := os.Getenv("PANEL4WP_QUOTA_VM_PHASE")
	if phase == "" {
		t.Skip("requires the isolated quota VM")
	}
	if _, err := os.Stat("/quota/guard-uuid"); err != nil {
		t.Fatal("missing VM fixture")
	}
	ctx := context.Background()
	docker := dockerRunner{}
	image, err := docker.Output(ctx, "image", "inspect", "--format", "{{.Id}}", "inventory-probe:lab")
	if err != nil {
		t.Fatal(err)
	}
	w := &worker{root: "/quota/registry-state", docker: docker, storageImage: strings.TrimSpace(string(image)), quotaRoot: "/quota"}
	first, second := "4444444444444444", "5555555555555555"
	if phase == "setup" {
		if err := os.Mkdir(w.root, 0700); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{first, second} {
			registrySite(t, w, id)
			for _, volume := range []string{"wordpress_data", "database_data"} {
				if err := docker.Run(ctx, "volume", "create", "--label", "com.docker.compose.project=wph-"+id, "--label", "com.docker.compose.volume="+volume, "wph-"+id+"_"+volume); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := docker.Run(ctx, "run", "--rm", "--network", "none", "--volume", "wph-"+first+"_wordpress_data:/data", "guard-canary:lab", "/bin/sh", "-c", "echo registry-preserved > /data/seed"); err != nil {
			t.Fatal(err)
		}
		a, err := w.reserveQuotaProject(ctx, first)
		if err != nil {
			out, probeErr := w.runStorageProbe(ctx, first, "--inventory")
			t.Fatalf("reservation: %v; native output: %s; probe: %v", err, out, probeErr)
		}
		b, err := w.reserveQuotaProject(ctx, second)
		if err != nil || a.ProjectID == b.ProjectID {
			t.Fatal(a, b, err)
		}
		restarted := &worker{root: w.root, docker: docker, storageImage: w.storageImage}
		retry, err := restarted.reserveQuotaProject(ctx, first)
		if err != nil || retry != a {
			t.Fatal("native retry changed identifier", retry, err)
		}
		if marked, err := w.quotaMarked(first); marked || err != nil {
			t.Fatal("reservation enabled quota", err)
		}
		t.Log("NATIVE_QUOTA_REGISTRY_RESERVATION_PASS")
		compose := "services:\n  wordpress:\n    restart: unless-stopped\n    security_opt: [no-new-privileges:true]\n  db:\n    restart: unless-stopped\n    security_opt: [no-new-privileges:true]\n  cli:\n"
		if err := os.WriteFile(filepath.Join(w.siteDir(first), "compose.yaml"), []byte(compose), 0600); err != nil {
			t.Fatal(err)
		}
		for _, service := range []string{"wordpress", "db"} {
			name, volume := "wph-wp-"+first, "wordpress_data"
			if service == "db" {
				name, volume = "wph-db-"+first, "database_data"
			}
			if err := docker.Run(ctx, "run", "-d", "--name", name, "--restart", "unless-stopped", "--network", "none", "--label", "com.docker.compose.project=wph-"+first, "--label", "com.docker.compose.service="+service, "--volume", "wph-"+first+"_"+volume+":/data", "guard-canary:lab", "/bin/sh", "-c", "while :; do /bin/busybox sleep 1; done"); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.prepareQuotaTagging(ctx, first); err != nil {
			t.Fatal(err)
		}
		if err := w.prepareQuotaTagging(ctx, first); err != nil {
			t.Fatal("preparation retry", err)
		}
		plan, err := w.readQuotaPlan(first)
		if err != nil || plan == nil || plan.Phase != "prepared" {
			t.Fatal(plan, err)
		}
		if guard, err := w.loadQuotaGuard(first); err == nil || guard != nil {
			t.Fatal("preparation authorized starts", guard, err)
		}
		t.Log("NATIVE_WORKER_PREPARATION_AND_WRITER_FENCING_PASS")
		if err := w.tagQuotaData(ctx, first); err != nil {
			t.Fatal("native tagging", err)
		}
		if err := w.tagQuotaData(ctx, first); err != nil {
			t.Fatal("native tagging retry", err)
		}
		plan, err = w.readQuotaPlan(first)
		if err != nil || plan == nil || plan.Phase != "tagged" {
			t.Fatal(plan, err)
		}
		if guard, err := w.loadQuotaGuard(first); err == nil || guard != nil {
			t.Fatal("tagging authorized writers", guard, err)
		}
		if err := docker.Run(ctx, "run", "-d", "--name", "wph-job-"+first, "--network", "none", "--label", "panel4wp.role=job", "--label", "panel4wp.site="+first, "--volume", "wph-"+first+"_wordpress_data:/data:ro", "guard-canary:lab", "/bin/sh", "-c", "while :; do /bin/busybox sleep 1; done"); err != nil {
			t.Fatal(err)
		}
		if err := w.reconcileQuotaSites(ctx, true); err != nil {
			t.Fatal("orphan cleanup", err)
		}
		if _, err := docker.Output(ctx, "inspect", "wph-job-"+first); err == nil {
			t.Fatal("orphan native job survived reconciliation")
		}
		t.Log("NATIVE_WORKER_TAGGING_RETRY_AND_ORPHAN_CLEANUP_PASS")
	} else if phase == "reboot" {
		path := filepath.Join(w.root, ".quota", "projects.json")
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		ledger, err := w.loadQuotaLedger(false)
		if err != nil || len(ledger.Reservations) != 2 {
			t.Fatal("reboot lost ledger", ledger, err)
		}
		if entry, err := w.reserveQuotaProject(ctx, second); err == nil || entry.ProjectID != 0 {
			t.Fatal("unenforced storage accepted", entry, err)
		}
		after, _ := os.ReadFile(path)
		if string(before) != string(after) {
			t.Fatal("rejection rewrote reservations")
		}
		out, err := docker.Output(ctx, "run", "--rm", "--network", "none", "--volume", "wph-"+first+"_wordpress_data:/data:ro", "guard-canary:lab", "/bin/busybox", "cat", "/data/seed")
		if err != nil || strings.TrimSpace(string(out)) != "registry-preserved" {
			t.Fatal("reservation lost volume data", err)
		}
		planPath := filepath.Join(w.siteDir(first), "quota-plan.json")
		planBefore, err := os.ReadFile(planPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := w.prepareQuotaTagging(ctx, first); err == nil {
			t.Fatal("unenforced preparation accepted")
		}
		planAfter, err := os.ReadFile(planPath)
		if err != nil || string(planBefore) != string(planAfter) {
			t.Fatal("failed preparation rewrote journal", err)
		}
		if err := w.tagQuotaData(ctx, first); err == nil {
			t.Fatal("unenforced tagged-tree recovery accepted")
		}
		w.quotaRoot = "" // Startup fencing must not depend on the authority binding.
		if err := w.reconcileQuotaSites(ctx, true); err != nil {
			t.Fatal(err)
		}
		states, err := w.quotaContainers(ctx, first)
		if err != nil || len(states) != 2 {
			t.Fatal(states, err)
		}
		for _, state := range states {
			if state.Restart != "no" || state.Status != "exited" {
				t.Fatal("prepared writer resumed", state)
			}
		}
		t.Log("NATIVE_PREPARATION_REBOOT_AND_MISSING_ROOT_FENCING_PASS")
		t.Log("NATIVE_QUOTA_REGISTRY_REBOOT_AND_REJECTION_PASS")
	} else {
		t.Fatal("unknown VM phase")
	}
}
