package main

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kazemsoft/panel4wp/internal/core"
	"github.com/kazemsoft/panel4wp/internal/securitypolicy"
)

func (w *worker) writeWriterProfile(id string) (string, error) {
	if !core.ValidID(id) {
		return "", errors.New("invalid site ID")
	}
	path := filepath.Join(w.siteDir(id), securitypolicy.Filename)
	want := securitypolicy.Bytes()
	current, err := os.ReadFile(path)
	if err == nil && bytes.Equal(current, want) {
		return path, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return path, atomicConfig(path, want)
}

func (w *worker) writerOptionLine(id string) string {
	return "    security_opt: [no-new-privileges:true, " + strconv.Quote("seccomp="+filepath.Join(w.siteDir(id), securitypolicy.Filename)) + "]\n"
}

// The panel owns this controlled Compose shape. Never overwrite a different
// administrator-provided security configuration or discard extra options.
func (w *worker) writerCompose(id string, data []byte) ([]byte, error) {
	if !core.ValidID(id) {
		return nil, errors.New("invalid site ID")
	}
	legacy := "    security_opt: [no-new-privileges:true]\n"
	guard := w.writerOptionLine(id)
	text := string(data)
	writers := 3 // WordPress, MariaDB and CLI; old sites may lack phpMyAdmin.
	if strings.Count(text, "  phpmyadmin:\n") == 1 {
		writers++
	}
	for _, service := range []string{"wordpress", "db", "cli"} {
		if strings.Count(text, "\n  "+service+":\n") != 1 {
			return nil, errors.New("unfamiliar site service configuration")
		}
	}
	if strings.Count(text, guard) == writers && strings.Count(text, "    security_opt:") == writers {
		return data, nil
	}
	if strings.Count(text, legacy) != writers-1 || strings.Count(text, "    security_opt:") != writers-1 || strings.Count(text, "  cli:\n") != 1 {
		return nil, errors.New("unfamiliar site security configuration; inspect Compose before applying the writer policy")
	}
	text = strings.ReplaceAll(text, legacy, guard)
	text = strings.Replace(text, "  cli:\n", "  cli:\n"+guard, 1)
	return []byte(text), nil
}

func (w *worker) ensureWriterPolicy(id string) error {
	path := filepath.Join(w.siteDir(id), "compose.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	updated, err := w.writerCompose(id, data)
	if err != nil {
		return err
	}
	if _, err := w.writeWriterProfile(id); err != nil {
		return err
	}
	if bytes.Equal(data, updated) {
		return nil
	}
	return atomicConfig(path, updated)
}

// Expand the saved configuration at startup, preserving all live containers
// and volumes. Runtime reconciliation identifies a missing actual filter;
// explicit Start applies it by recreating services.
func (w *worker) migrateWriterPolicies() error {
	entries, err := os.ReadDir(w.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !core.ValidID(entry.Name()) {
			continue
		}
		if err := w.ensureWriterPolicy(entry.Name()); err != nil {
			// Incomplete provisioning and unfamiliar hand-edited configurations
			// require inspection, rather than changing another site's availability.
			log.Printf("writer policy migration skipped site %s: %v", entry.Name(), err)
		}
	}
	return nil
}

func (w *worker) writerSecurityArgs(id string) ([]string, error) {
	path, err := w.writeWriterProfile(id)
	if err != nil {
		return nil, fmt.Errorf("writer profile unavailable: %w", err)
	}
	return []string{"--security-opt", "no-new-privileges:true", "--security-opt", "seccomp=" + path}, nil
}
