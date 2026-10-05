package main

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/kazemsoft/panel4wp/internal/core"
	"github.com/kazemsoft/panel4wp/internal/i18n"
)

func formResources(r *http.Request) (core.Resources, error) {
	var limits core.Resources
	switch r.FormValue("plan") {
	case "small":
		limits = core.Resources{MemoryMB: 384, CPUs: .5}
	case "", "standard":
		limits = core.Resources{MemoryMB: 768, CPUs: 1}
	case "large":
		limits = core.Resources{MemoryMB: 1536, CPUs: 2}
	case "custom":
		m, err := strconv.Atoi(r.FormValue("memory_mb"))
		if err != nil {
			return limits, errors.New("invalid memory limit")
		}
		c, err := strconv.ParseFloat(r.FormValue("cpus"), 64)
		if err != nil {
			return limits, errors.New("invalid CPU limit")
		}
		limits = core.Resources{MemoryMB: m, CPUs: c}
	default:
		return limits, errors.New("invalid resource plan")
	}
	return limits, limits.Validate()
}

func (a *app) saveResources(w http.ResponseWriter, r *http.Request, site core.Site) {
	target := "/sites/" + site.ID + "/resources"
	if site.HealthError != "" || site.Status != core.StatusRunning && site.Status != core.StatusStopped {
		http.Error(w, "verify or repair the site's service status before changing resources", http.StatusConflict)
		return
	}
	limits, err := a.formResources(r)
	if err != nil {
		a.redirectWithFlashTo(w, r, target, "", err.Error())
		return
	}
	var applied core.Resources
	if err := a.callTracked(&site, "resources", "/resources", core.ResourceRequest{Site: site, Resources: limits}, &applied); err != nil {
		a.record("resources", site, false, err.Error())
		a.redirectWithFlashTo(w, r, target, "", err.Error())
		return
	}
	if applied != limits {
		http.Error(w, "resource change result was inconsistent; refresh service status", http.StatusBadGateway)
		return
	}
	site.MemoryMB, site.CPUs = applied.MemoryMB, applied.CPUs
	site.Error, site.HealthError = "", ""
	if err := a.store.Put(site); err != nil {
		http.Error(w, "resources applied but metadata could not be saved; refresh service status", http.StatusInternalServerError)
		return
	}
	a.record("resources", site, true, "")
	a.redirectWithFlashTo(w, r, target, i18n.T(i18n.Resolve(r), "resources_saved"), "")
}

func resourcePlan(r core.Resources) string {
	switch r {
	case (core.Resources{MemoryMB: 384, CPUs: .5}):
		return "small"
	case (core.Resources{MemoryMB: 768, CPUs: 1}):
		return "standard"
	case (core.Resources{MemoryMB: 1536, CPUs: 2}):
		return "large"
	default:
		return "custom"
	}
}

func (a *app) formResources(r *http.Request) (core.Resources, error) {
	value := r.FormValue("plan")
	if !strings.HasPrefix(value, "saved-") {
		return formResources(r)
	}
	id := strings.TrimPrefix(value, "saved-")
	if !core.ValidID(id) {
		return core.Resources{}, errors.New("invalid resource plan")
	}
	plans, err := a.store.ListResourcePlans()
	if err != nil {
		return core.Resources{}, err
	}
	for _, plan := range plans {
		if plan.ID == id {
			return plan.Resources, plan.Resources.Validate()
		}
	}
	return core.Resources{}, errors.New("resource plan no longer exists; reload the page")
}

func (a *app) resourcePlansAction(w http.ResponseWriter, r *http.Request) {
	var err error
	if r.URL.Path == "/settings/resource-plans/delete" {
		err = a.store.DeleteResourcePlan(r.FormValue("id"))
	} else {
		var limits core.Resources
		limits, err = formResources(r)
		if err == nil {
			var id string
			id, err = core.NewID()
			if err == nil {
				err = a.store.PutResourcePlan(core.ResourcePlan{ID: id, Name: strings.TrimSpace(r.FormValue("name")), Resources: limits})
			}
		}
	}
	if err != nil {
		a.redirectWithFlashTo(w, r, "/settings", "", err.Error())
		return
	}
	a.record("resource-plans", core.Site{}, true, "")
	a.redirectWithFlashTo(w, r, "/settings", i18n.T(i18n.Resolve(r), "plans_saved"), "")
}
