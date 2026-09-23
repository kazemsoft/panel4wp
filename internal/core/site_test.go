package core

import (
	"strings"
	"testing"
	"time"
)

func TestNormalizeDomain(t *testing.T) {
	valid := map[string]string{
		" Example.COM ":   "example.com",
		"a-b.example.org": "a-b.example.org",
		"abc.localhost":   "abc.localhost",
	}
	for input, want := range valid {
		got, err := NormalizeDomain(input)
		if err != nil || got != want {
			t.Fatalf("NormalizeDomain(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	for _, input := range []string{"", "localhost", "https://example.com", "a..com", "-a.com", "a-.com", "a.b.localhost", "a.com\nother"} {
		if _, err := NormalizeDomain(input); err == nil {
			t.Errorf("accepted invalid domain %q", input)
		}
	}
}

func TestRandomIDAndPassword(t *testing.T) {
	a, err := NewID()
	if err != nil || !ValidID(a) {
		t.Fatalf("invalid generated ID: %q, %v", a, err)
	}
	b, _ := NewID()
	if a == b {
		t.Fatal("IDs collided")
	}
	p, err := RandomPassword()
	if err != nil || len(p) != 48 {
		t.Fatalf("invalid password: length %d, %v", len(p), err)
	}
}

func TestBackupID(t *testing.T) {
	id, err := NewBackupID(time.Date(2026, 9, 23, 1, 2, 3, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if !ValidBackupID(id) || !strings.HasPrefix(id, "20260923T010203Z-") {
		t.Fatalf("unexpected backup ID: %q", id)
	}
	if ValidBackupID("../backup") {
		t.Fatal("path traversal accepted as a backup ID")
	}
}

func TestResourceLimits(t *testing.T) {
	memory, cpus := ResourceLimits(Site{})
	if memory != 512 || cpus != 1 {
		t.Fatalf("unexpected defaults: %d MB, %g CPUs", memory, cpus)
	}
	site := Site{ID: "0123456789abcdef", Domain: "one.localhost", Title: "one", AdminEmail: "a@example.com", MemoryMB: 128, CPUs: 1}
	if err := ValidateSite(site); err == nil {
		t.Fatal("accepted unsafe memory limit")
	}
}
