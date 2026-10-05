package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Execute the production PHP scripts against a temporary filesystem. These
// tests cover the dangling-link exploit, not merely the Go argument builder.
func TestFileScriptsRejectSymlinksAndPreserveTargets(t *testing.T) {
	php, err := exec.LookPath("php")
	if err != nil {
		t.Skip("PHP CLI unavailable; run this test on the Linux CI runner")
	}
	base := t.TempDir()
	root := filepath.Join(base, "content")
	outside := filepath.Join(base, "outside")
	os.MkdirAll(root, 0755)
	os.MkdirAll(outside, 0755)
	run := func(script, input string, args ...string) error {
		script = strings.ReplaceAll(script, "/var/www/html/wp-content", root)
		cmd := exec.Command(php, append([]string{"-d", "display_errors=stderr", "-d", "open_basedir=" + root, "-r", script}, args...)...)
		cmd.Stdin = strings.NewReader(input)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Logf("PHP rejected operation: %s", bytes.TrimSpace(output))
		}
		return err
	}
	if err := run(writeFilePHP, "safe", "normal.txt"); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "normal.txt")); err != nil || string(data) != "safe" {
		t.Fatal("regular upload failed")
	}
	target := filepath.Join(outside, "absent.txt")
	os.Symlink(target, filepath.Join(root, "dangling.txt"))
	if run(writeFilePHP, "unsafe", "dangling.txt") == nil {
		t.Fatal("dangling symlink upload accepted")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("upload escaped file manager")
	}
	os.Symlink(filepath.Join(root, "normal.txt"), filepath.Join(root, "link.txt"))
	for _, script := range []string{readFilePHP, deleteFilePHP} {
		if run(script, "", "link.txt") == nil {
			t.Fatal("symlink target operation accepted")
		}
	}
	if run(moveFilePHP, "", "link.txt", "moved.txt") == nil {
		t.Fatal("symlink source move accepted")
	}
	if run(moveFilePHP, "", "normal.txt", "dangling.txt") == nil {
		t.Fatal("dangling symlink destination accepted")
	}
	if data, _ := os.ReadFile(filepath.Join(root, "normal.txt")); string(data) != "safe" {
		t.Fatal("symlink deletion affected real file")
	}
	os.Symlink(outside, filepath.Join(root, "parent"))
	if run(writeFilePHP, "unsafe", "parent/new.txt") == nil {
		t.Fatal("symlink parent accepted")
	}
	script := strings.ReplaceAll(listFilesPHP, "/var/www/html/wp-content", root)
	out, listErr := exec.Command(php, "-d", "display_errors=stderr", "-d", "open_basedir="+root, "-r", script, "").CombinedOutput()
	var entries []map[string]any
	if listErr != nil || json.Unmarshal(out, &entries) != nil {
		t.Fatalf("links corrupted directory listing: %s %v", out, listErr)
	}
	oldRoot := root + "-old"
	os.Rename(root, oldRoot)
	os.Symlink(outside, root)
	if run(writeFilePHP, "unsafe", "root-escape.txt") == nil {
		t.Fatal("symlink root accepted")
	}
	if _, err := os.Stat(filepath.Join(outside, "root-escape.txt")); !os.IsNotExist(err) {
		t.Fatal("root link escaped restriction")
	}
}
