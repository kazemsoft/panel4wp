package audit

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendAndListNewestFirst(t *testing.T) {
	log := New(filepath.Join(t.TempDir(), "audit.jsonl"))
	for _, action := range []string{"create", "backup", "delete"} {
		if err := log.Append(Entry{Action: action, SiteID: "0123456789abcdef", Success: action != "delete", Detail: "line one\nline two"}); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := log.List(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Action != "delete" || entries[1].Action != "backup" {
		t.Fatalf("unexpected entries: %#v", entries)
	}
	if strings.Contains(entries[0].Detail, "\n") {
		t.Fatal("audit detail retained a newline")
	}
}

func TestRejectsInvalidInput(t *testing.T) {
	log := New(filepath.Join(t.TempDir(), "audit.jsonl"))
	if err := log.Append(Entry{}); err == nil {
		t.Fatal("empty action accepted")
	}
	if _, err := log.List(0); err == nil {
		t.Fatal("invalid limit accepted")
	}
}
