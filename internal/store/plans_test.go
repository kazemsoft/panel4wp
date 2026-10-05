package store

import (
	"github.com/kazemsoft/panel4wp/internal/core"
	"path/filepath"
	"testing"
)

func TestNamedPlansPersistAndDeletionPreservesSiteAllocations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sites.json")
	s := New(path)
	plan := core.ResourcePlan{ID: "0123456789abcdef", Name: "Custom hosting", Resources: core.Resources{MemoryMB: 640, CPUs: .75}}
	if err := s.PutResourcePlan(plan); err != nil {
		t.Fatal(err)
	}
	s.Put(core.Site{ID: "1111111111111111", MemoryMB: 640, CPUs: .75})
	restarted := New(path)
	plans, err := restarted.ListResourcePlans()
	if err != nil || len(plans) != 1 || plans[0] != plan {
		t.Fatal(plans, err)
	}
	duplicate := plan
	duplicate.ID = "2222222222222222"
	if restarted.PutResourcePlan(duplicate) == nil {
		t.Fatal("duplicate plan accepted")
	}
	if err := restarted.DeleteResourcePlan(plan.ID); err != nil {
		t.Fatal(err)
	}
	site, ok, err := restarted.Get("1111111111111111")
	if err != nil || !ok || site.MemoryMB != 640 || site.CPUs != .75 {
		t.Fatal("deleting template modified the site", site, err)
	}
}
