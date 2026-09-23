package settings

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestEncryptedRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	store, err := New(path, []byte(strings.Repeat("k", 64)))
	if err != nil {
		t.Fatal(err)
	}
	want := Mail{Enabled: true, Host: "smtp.example.com", Port: 587, Encryption: "starttls", Username: "mailer", Password: "very-secret", FromEmail: "hello@example.com", FromName: "Example"}
	if err := store.Save(want); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(want.Password)) || bytes.Contains(raw, []byte(want.Host)) {
		t.Fatal("settings were stored in plaintext")
	}
	got, err := store.Load()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip failed: %#v, %v", got, err)
	}
}

func TestValidationAndWrongKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	store, _ := New(path, []byte(strings.Repeat("a", 64)))
	if err := store.Save(Mail{Enabled: true, Host: "bad host", Port: 587, Encryption: "starttls", FromEmail: "a@example.com", FromName: "A"}); err == nil {
		t.Fatal("invalid host accepted")
	}
	valid := Mail{Enabled: true, Host: "smtp.example.com", Port: 465, Encryption: "tls", FromEmail: "a@example.com", FromName: "A"}
	if err := store.Save(valid); err != nil {
		t.Fatal(err)
	}
	wrong, _ := New(path, []byte(strings.Repeat("b", 64)))
	if _, err := wrong.Load(); err == nil {
		t.Fatal("wrong encryption key accepted")
	}
}
