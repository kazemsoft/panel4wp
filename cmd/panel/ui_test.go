package main

import (
	"bytes"
	"github.com/kazemsoft/panel4wp/internal/core"
	"strings"
	"testing"
)

func TestDashboardPreservesSiteActionsAndEscapesContent(t *testing.T) {
	var output bytes.Buffer
	site := core.Site{ID: "abc123", Domain: "demo.localhost", Title: `<script>alert(1)</script>`, Status: core.StatusRunning, Backups: []core.Backup{{ID: "backup123"}}}
	if err := page.Execute(&output, view{LoggedIn: true, CSRF: "csrf-test", Sites: []core.Site{site}}); err != nil {
		t.Fatal(err)
	}
	html := output.String()
	for _, target := range []string{"stop", "backup", "update", "database-start", "restore", "delete"} {
		if !strings.Contains(html, `action="/sites/abc123/`+target+`"`) {
			t.Errorf("missing action %s", target)
		}
	}
	for _, required := range []string{`href="/sites/abc123/files"`, `name="confirm"`, `value="csrf-test"`, `name="backup_id"`, `Coming soon`, `id="settings"`} {
		if !strings.Contains(html, required) {
			t.Errorf("missing UI element %s", required)
		}
	}
	if strings.Contains(html, site.Title) {
		t.Fatal("unescaped site title")
	}
}

func TestFileManagerConfirmsDeletionAndPreservesOperations(t *testing.T) {
	var output bytes.Buffer
	v := filesView{Site: core.Site{ID: "abc123", Domain: "demo.localhost"}, CSRF: "csrf-test", Entries: []fileEntryView{{FileEntry: core.FileEntry{Name: "test.txt", Type: "file"}, Path: "test.txt"}}}
	if err := filesPage.Execute(&output, v); err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{`action="/sites/abc123/download"`, `action="/sites/abc123/file-delete"`, `action="/sites/abc123/upload"`, `action="/sites/abc123/mkdir"`, `Confirm delete`, `value="csrf-test"`} {
		if !strings.Contains(output.String(), required) {
			t.Errorf("missing UI element %s", required)
		}
	}
}
