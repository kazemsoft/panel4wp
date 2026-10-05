package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInstallerWaitsForCompleteWordPressConfig(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "wp-includes"), 0700)
	os.MkdirAll(filepath.Join(root, "bin"), 0700)
	os.MkdirAll(filepath.Join(root, "secrets"), 0700)
	os.WriteFile(filepath.Join(root, "wp-includes/version.php"), []byte("version"), 0600)
	os.WriteFile(filepath.Join(root, "wp-config.php"), []byte("<?php // still being written\n"), 0600)
	for _, name := range []string{"admin_password", "admin_email"} {
		os.WriteFile(filepath.Join(root, "secrets", name), []byte("test-value"), 0600)
	}
	stub := `#!/bin/sh
if ! grep -q 'require_once.*wp-settings.php' wp-config.php; then touch premature; exit 8; fi
echo "$1 $2" >> calls
exit 0
`
	os.WriteFile(filepath.Join(root, "bin/wp"), []byte(stub), 0700)
	done := make(chan error, 1)
	go func() {
		time.Sleep(100 * time.Millisecond)
		done <- os.WriteFile(filepath.Join(root, "wp-config.php"), []byte("<?php\nrequire_once ABSPATH . 'wp-settings.php';\n"), 0600)
	}()
	cmd := exec.Command("sh", "-c", strings.ReplaceAll(wpInstallScript, "/run/secrets", filepath.Join(root, "secrets")))
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PATH="+filepath.Join(root, "bin")+":"+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "premature")); err == nil {
		t.Fatal("WP-CLI ran before configuration was complete")
	}
	calls, _ := os.ReadFile(filepath.Join(root, "calls"))
	if !strings.Contains(string(calls), "user update") {
		t.Fatal("installation did not continue after readiness")
	}
}
