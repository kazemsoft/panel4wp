package core

import "testing"

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
