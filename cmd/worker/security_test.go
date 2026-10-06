package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kazemsoft/panel4wp/internal/core"
	"github.com/kazemsoft/panel4wp/internal/securitypolicy"
)

func TestWriterMigrationPreservesConfigurationAndNeverRestartsSites(t *testing.T) {
	id := "0123456789abcdef"
	for _, withTool := range []bool{true, false} {
		t.Run(fmt.Sprint(withTool), func(t *testing.T) {
			f := &fakeDocker{}
			w := &worker{root: t.TempDir(), docker: f}
			os.MkdirAll(w.siteDir(id), 0700)
			data := []byte(fmt.Sprintf(composeTemplate, id, 512, 1., id, 512, 1., id, "http://localhost"))
			if !withTool {
				start := bytes.Index(data, []byte("  phpmyadmin:\n"))
				end := bytes.Index(data, []byte("  cli:\n"))
				data = append(data[:start], data[end:]...)
			}
			path := filepath.Join(w.siteDir(id), "compose.yaml")
			os.WriteFile(path, data, 0600)
			if err := w.migrateWriterPolicies(); err != nil {
				t.Fatal(err)
			}
			first, _ := os.ReadFile(path)
			expectedCount := 3
			if withTool {
				expectedCount++
			}
			if strings.Count(string(first), "seccomp=") != expectedCount {
				t.Fatal("writer service omitted")
			}
			// Replacing just security options must restore every original byte.
			guard := "    security_opt: [no-new-privileges:true, " + fmt.Sprintf("%q", "seccomp="+filepath.Join(w.siteDir(id), securitypolicy.Filename)) + "]\n"
			restored := strings.Replace(string(first), "  cli:\n"+guard, "  cli:\n", 1)
			restored = strings.ReplaceAll(restored, guard, "    security_opt: [no-new-privileges:true]\n")
			if restored != string(data) {
				t.Fatal("migration changed volumes, secrets, limits or other configuration")
			}
			if err := w.migrateWriterPolicies(); err != nil {
				t.Fatal(err)
			}
			second, _ := os.ReadFile(path)
			if !bytes.Equal(first, second) || len(f.calls) != 0 {
				t.Fatal("migration not idempotent or restarted a service")
			}
			profile, _ := os.ReadFile(filepath.Join(w.siteDir(id), securitypolicy.Filename))
			if !bytes.Equal(profile, securitypolicy.Bytes()) {
				t.Fatal("profile not persisted")
			}
			info, _ := os.Stat(filepath.Join(w.siteDir(id), securitypolicy.Filename))
			if info.Mode().Perm() != 0600 {
				t.Fatal("profile permissions")
			}
		})
	}
}

func TestWriterMigrationRefusesCustomSecurityAndSkipsIncompleteSites(t *testing.T) {
	id := "0123456789abcdef"
	f := &fakeDocker{}
	w := &worker{root: t.TempDir(), docker: f}
	os.MkdirAll(w.siteDir(id), 0700)
	path := filepath.Join(w.siteDir(id), "compose.yaml")
	original := []byte(strings.ReplaceAll(fmt.Sprintf(composeTemplate, id, 512, 1., id, 512, 1., id, "http://localhost"), "[no-new-privileges:true]", "[no-new-privileges:true, apparmor:custom]"))
	os.WriteFile(path, original, 0600)
	if err := w.ensureWriterPolicy(id); err == nil {
		t.Fatal("custom security configuration was discarded")
	}
	os.MkdirAll(w.siteDir("fedcba9876543210"), 0700)
	if err := w.migrateWriterPolicies(); err != nil {
		t.Fatal("partial site prevented startup", err)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, original) || len(f.calls) > 0 {
		t.Fatal("unsafe migration")
	}
}

func TestRuntimeDetectsMissingActualWriterFilter(t *testing.T) {
	id := "0123456789abcdef"
	opts := []string{"no-new-privileges:true", "seccomp=" + string(securitypolicy.Bytes())}
	for _, tc := range []struct {
		wp, db []string
		want   core.Status
	}{
		{opts, opts, core.StatusRunning},
		{nil, opts, core.StatusUnhealthy},
		{opts, []string{"seccomp=unconfined", "no-new-privileges:true"}, core.StatusUnhealthy},
	} {
		f := &runtimeDocker{states: runtimeStates("running", "running", "healthy")}
		base := t.TempDir()
		w := &worker{root: base, routes: base, docker: f}
		os.WriteFile(filepath.Join(base, id+".caddy"), []byte("route"), 0600)
		// Keep expected names and project; change only actual inspect security options.
		f.states = fmt.Sprintf(`{"name":"/wph-wp-%s","project":"wph-%s","service":"wordpress","status":"running","security_opts":%s}
{"name":"/wph-db-%s","project":"wph-%s","service":"db","status":"running","security_opts":%s}`, id, id, jsonOptions(tc.wp), id, id, jsonOptions(tc.db))
		got, err := w.runtime(context.Background(), id)
		if err != nil || got.Status != tc.want {
			t.Fatalf("runtime %v %v", got, err)
		}
		if got.Status == core.StatusUnhealthy && !strings.Contains(got.Detail, "security policy") {
			t.Fatal("missing repair guidance")
		}
	}
}

func jsonOptions(options []string) string { data, _ := json.Marshal(options); return string(data) }
