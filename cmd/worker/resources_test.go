package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/kazemsoft/panel4wp/internal/core"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type resourceDocker struct {
	fakeDocker
	stopped     bool
	low         bool
	failUpdate  bool
	pendingSeen bool
	worker      *worker
}

func (f *resourceDocker) Output(ctx context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	switch args[0] {
	case "info":
		mem := int64(16 << 30)
		if f.low {
			mem = 1 << 30
		}
		return []byte(fmt.Sprintf(`{"cpus":16,"memory_bytes":%d}`, mem)), nil
	case "run":
		return []byte("Filesystem 1024-blocks Used Available Capacity Mounted on\n/dev/test 10000000 1000 9000000 1% /"), nil
	case "ps":
		if strings.Contains(strings.Join(args, " "), "project=wph-") && args[1] == "-a" {
			return []byte("wp db"), nil
		}
		return nil, nil
	case "inspect":
		s := "running"
		if f.stopped {
			s = "exited"
		}
		states := runtimeStates(s, s, "")
		states = strings.ReplaceAll(states, `"Status":`, `"Memory":536870912,"CPUs":1000000000,"Status":`)
		return []byte(states), nil
	}
	return nil, errors.New("unexpected Docker query")
}
func (f *resourceDocker) Run(ctx context.Context, args ...string) error {
	f.calls = append(f.calls, args)
	if f.worker != nil {
		op, _ := f.worker.loadOperation("0123456789abcdef")
		if op != nil && op.State == "pending" {
			f.pendingSeen = true
		}
	}
	if f.failUpdate && strings.Contains(strings.Join(args, " "), "up -d --wait wordpress db") {
		return errors.New("recreate failed")
	}
	return nil
}
func resourceWorker(t *testing.T, f *resourceDocker) *worker {
	t.Helper()
	base := t.TempDir()
	w := &worker{root: filepath.Join(base, "sites"), routes: filepath.Join(base, "routes"), docker: f, token: strings.Repeat("t", 64)}
	f.worker = w
	id := "0123456789abcdef"
	os.MkdirAll(w.siteDir(id), 0700)
	os.MkdirAll(w.routes, 0700)
	os.WriteFile(filepath.Join(w.siteDir(id), "compose.yaml"), []byte(fmt.Sprintf(composeTemplate, id, 512, 1., id, 512, 1., id, "http://localhost")), 0600)
	os.WriteFile(filepath.Join(w.routes, id+".caddy"), []byte("route"), 0600)
	return w
}
func resourceRequest() core.ResourceRequest {
	return core.ResourceRequest{Site: core.Site{ID: "0123456789abcdef", Domain: "one.localhost", Title: "One", AdminEmail: "test@example.com"}, Resources: core.Resources{MemoryMB: 640, CPUs: .75}}
}

func TestResourceChangesPreserveStorageAndStoppedState(t *testing.T) {
	for _, stopped := range []bool{false, true} {
		t.Run(fmt.Sprint(stopped), func(t *testing.T) {
			f := &resourceDocker{stopped: stopped}
			w := resourceWorker(t, f)
			req := resourceRequest()
			dataFile := filepath.Join(w.siteDir(req.Site.ID), "preserved")
			os.WriteFile(dataFile, []byte("existing"), 0600)
			got, err := w.changeResources(context.Background(), req)
			if err != nil || got != req.Resources {
				t.Fatalf("change: %#v %v", got, err)
			}
			configured, err := w.configuredResources(req.Site.ID)
			if err != nil || configured != req.Resources {
				t.Fatal("configuration did not persist", err)
			}
			data, _ := os.ReadFile(dataFile)
			if string(data) != "existing" {
				t.Fatal("site data changed")
			}
			commands := fmt.Sprint(f.calls)
			if strings.Contains(commands, "--volumes") || strings.Contains(commands, "down") {
				t.Fatal("destructive recreation", commands)
			}
			if stopped && !strings.Contains(commands, "create --force-recreate wordpress db") {
				t.Fatal("stopped site was not kept stopped", commands)
			}
			if !stopped && !strings.Contains(commands, "up -d --wait wordpress db") {
				t.Fatal("running site was not restarted", commands)
			}
			compose, _ := os.ReadFile(filepath.Join(w.siteDir(req.Site.ID), "compose.yaml"))
			if !strings.Contains(string(compose), "mem_limit: 256m") {
				t.Fatal("database tool resources were altered")
			}
		})
	}
}

func TestCapacityCountsStoppedConfiguredSitesAndExcludesEditedSite(t *testing.T) {
	f := &resourceDocker{}
	w := resourceWorker(t, f)
	h, err := w.hostCapacity(context.Background(), "")
	if err != nil || h.AllocatedCPUs != 2 || h.AllocatedMemoryBytes != 1<<30 {
		t.Fatalf("capacity: %#v %v", h, err)
	}
	h, err = w.hostCapacity(context.Background(), "0123456789abcdef")
	if err != nil || h.AllocatedCPUs != 0 || h.AllocatedMemoryBytes != 0 {
		t.Fatalf("double counted edited site: %#v %v", h, err)
	}
}

func TestRejectedResourceChangeHasNoServiceSideEffects(t *testing.T) {
	f := &resourceDocker{low: true}
	w := resourceWorker(t, f)
	req := resourceRequest()
	raw, _ := json.Marshal(req)
	r := httptest.NewRequest("POST", "/resources", strings.NewReader(string(raw)))
	r.Header.Set("X-Worker-Token", w.token)
	r.Header.Set("X-Operation-ID", "1111111111111111")
	resp := httptest.NewRecorder()
	w.ServeHTTP(resp, r)
	if resp.Code != 500 {
		t.Fatal("capacity rejection missing", resp.Code)
	}
	limits, _ := w.configuredResources(req.Site.ID)
	if limits.MemoryMB != 512 {
		t.Fatal("rejected plan changed configuration")
	}
	for _, args := range f.calls {
		if args[0] != "ps" && args[0] != "inspect" && args[0] != "info" && args[0] != "run" {
			t.Fatal("rejected change touched service", args)
		}
	}
	op, _ := w.loadOperation(req.Site.ID)
	if op.State != "failed" {
		t.Fatal("rejected operation not resolved")
	}
}

func TestTrackedResourceChangeDoesNotInterruptItsOwnReceipt(t *testing.T) {
	f := &resourceDocker{}
	w := resourceWorker(t, f)
	raw, _ := json.Marshal(resourceRequest())
	r := httptest.NewRequest("POST", "/resources", strings.NewReader(string(raw)))
	r.Header.Set("X-Worker-Token", w.token)
	r.Header.Set("X-Operation-ID", "1111111111111111")
	resp := httptest.NewRecorder()
	w.ServeHTTP(resp, r)
	if resp.Code != 200 {
		t.Fatal(resp.Code, resp.Body.String())
	}
	op, _ := w.loadOperation("0123456789abcdef")
	if !f.pendingSeen || op.State != "succeeded" {
		t.Fatal("pending receipt was lost during execution", op)
	}
}

func TestUnknownComposeShapeIsNotRewritten(t *testing.T) {
	if _, _, err := composeResources([]byte("services:\n  wordpress:\n    cpus: 1\n"), &core.Resources{MemoryMB: 512, CPUs: 1}); err == nil {
		t.Fatal("accepted incomplete limits")
	}
}

type sharedHostDocker struct{ resourceDocker }

func (f *sharedHostDocker) Output(ctx context.Context, args ...string) ([]byte, error) {
	if args[0] == "ps" && !strings.Contains(strings.Join(args, " "), "project=wph-") {
		return []byte("{\"name\":\"other-app\",\"state\":\"running\"}\n{\"name\":\"unlimited-app\",\"state\":\"running\"}"), nil
	}
	if args[0] == "inspect" && strings.Contains(strings.Join(args, " "), "other-app") {
		return []byte("{\"name\":\"/other-app\",\"memory\":2147483648,\"cpus\":1000000000}\n{\"name\":\"/unlimited-app\",\"memory\":0,\"cpus\":0}"), nil
	}
	if args[0] == "stats" {
		return []byte(`{"name":"unlimited-app","memory":"256MiB / 16GiB","cpu":"50.00%"}`), nil
	}
	return f.resourceDocker.Output(ctx, args...)
}

func TestCapacityIncludesOtherDockerLimitsAndUnboundedUsage(t *testing.T) {
	f := &sharedHostDocker{}
	w := resourceWorker(t, &f.resourceDocker)
	w.docker = f
	h, err := w.hostCapacity(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if h.OtherMemoryBytes != 2304*1024*1024 || h.OtherCPUs != 1.5 {
		t.Fatal("other workloads not budgeted", h)
	}
	if err := h.Check(core.Resources{MemoryMB: 5120, CPUs: .5}); err == nil {
		t.Fatal("admitted resources reserved by other applications")
	}
}

func TestDockerMemoryUsageUnits(t *testing.T) {
	for raw, want := range map[string]int64{"256MiB / 16GiB": 256 << 20, "1.5GiB / 4GiB": 1536 << 20, "12MB / 4GB": 12000000, "0B / 4GiB": 0} {
		got, err := memoryUsageBytes(raw)
		if err != nil || got != want {
			t.Fatal(raw, got, err)
		}
	}
	for _, raw := range []string{"NaNB", "invalid", "-1MiB", "1Unknown"} {
		if _, err := memoryUsageBytes(raw); err == nil {
			t.Fatal("accepted malformed usage", raw)
		}
	}
}
