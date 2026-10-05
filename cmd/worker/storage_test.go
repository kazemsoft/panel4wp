package main

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStorageCapabilityRequiresBothAccountingAndEnforcement(t *testing.T) {
	valid := `{"wordpress_filesystem":"xfs","database_filesystem":"xfs","same_filesystem":true,"project_accounting":true,"project_enforcement":true,"reason":"supported"}`
	got, err := decodeStorageCapability([]byte(valid))
	if err != nil || !got.SupportsProjectQuotas() {
		t.Fatal(got, err)
	}
	for _, invalid := range []string{
		strings.Replace(valid, `"project_enforcement":true`, `"project_enforcement":false`, 1),
		strings.Replace(valid, `"project_accounting":true`, `"project_accounting":false`, 1),
		strings.Replace(valid, `"same_filesystem":true`, `"same_filesystem":false`, 1),
		strings.Replace(valid, `"xfs"`, `"ext4"`, 1),
		strings.Replace(valid, `"reason":"supported"`, `"reason":"unknown"`, 1),
		valid + `{}`,
	} {
		if _, err := decodeStorageCapability([]byte(invalid)); err == nil {
			t.Fatal("unverified support accepted", invalid)
		}
	}
	got, err = decodeStorageCapability([]byte(strings.Replace(valid, `"reason":"supported"`, `"reason":"enforcement-disabled"`, 1)))
	if err != nil || got.SupportsProjectQuotas() {
		t.Fatal("unsupported result became active support", got, err)
	}
}

func TestStorageCapabilityDoesNotMountUnownedOrAliasedVolumes(t *testing.T) {
	id := "0123456789abcdef"
	for _, scenario := range []struct{ project, driver, options, reason string }{
		{"foreign", "local", "false", "volume-ownership"},
		{"wph-" + id, "nfs", "false", "volume-driver"},
		{"wph-" + id, "local", "true", "volume-driver"},
	} {
		t.Run(scenario.reason+scenario.driver+scenario.options, func(t *testing.T) {
			f := &sizeDocker{outputs: [][]byte{[]byte(fmt.Sprintf(`{"name":"wph-%s_wordpress_data","driver":"%s","project":"%s","volume":"wordpress_data","options":%s}`, id, scenario.driver, scenario.project, scenario.options))}}
			w := &worker{root: t.TempDir(), docker: f}
			if err := os.MkdirAll(w.siteDir(id), 0700); err != nil {
				t.Fatal(err)
			}
			os.WriteFile(filepath.Join(w.siteDir(id), "compose.yaml"), []byte("existing"), 0600)
			got, err := w.storageCapability(context.Background(), id)
			if err != nil || got.Reason != scenario.reason || len(f.calls) != 1 || f.calls[0][0] != "volume" {
				t.Fatal("unsafe volume reached helper", got, err, f.calls)
			}
		})
	}
}

func TestStorageCapabilityRequiresWorkerAuthenticationAndValidSite(t *testing.T) {
	f := &fakeDocker{}
	w := &worker{docker: f, token: strings.Repeat("t", 64)}
	for _, authorized := range []bool{false, true} {
		r := httptest.NewRequest("POST", "/storage/capability", strings.NewReader(`{"site_id":"../../foreign"}`))
		if authorized {
			r.Header.Set("X-Worker-Token", w.token)
		}
		resp := httptest.NewRecorder()
		w.ServeHTTP(resp, r)
		expected := 403
		if authorized {
			expected = 400
		}
		if resp.Code != expected || len(f.calls) != 0 {
			t.Fatal("invalid capability request reached Docker", resp.Code, f.calls)
		}
	}
}
