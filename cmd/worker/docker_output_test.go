package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDockerOutputSeparatesWarningsAndRetainsFailures(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
echo 'WARNING: fixture has no swap accounting' >&2
if test "$1" = fail; then
  echo 'fixture operation failed' >&2
  exit 1
fi
echo '{"verified":true}'
`
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := (dockerRunner{}).Output(context.Background(), "inspect")
	if err != nil || string(out) != "{\"verified\":true}\n" {
		t.Fatal("stderr contaminated protocol output", string(out), err)
	}
	out, err = (dockerRunner{}).Output(context.Background(), "fail")
	if err == nil || out != nil || !strings.Contains(err.Error(), "fixture operation failed") || !strings.Contains(err.Error(), "exit status 1") {
		t.Fatal("failure diagnostics were lost", out, err)
	}
}
