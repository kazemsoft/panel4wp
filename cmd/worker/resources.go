package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kazemsoft/panel4wp/internal/core"
)

// Parse only the controlled Compose resource fields. Refuse unfamiliar or
// asymmetric configurations rather than guessing during capacity admission.
func composeResources(data []byte, replacement *core.Resources) (core.Resources, []byte, error) {
	lines := strings.Split(string(data), "\n")
	section := ""
	values := map[string]core.Resources{}
	counts := map[string]int{}
	for i, line := range lines {
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") && strings.HasSuffix(line, ":") {
			section = strings.TrimSpace(strings.TrimSuffix(line, ":"))
		}
		if line != "" && !strings.HasPrefix(line, " ") {
			section = ""
		}
		if section != "wordpress" && section != "db" {
			continue
		}
		r := values[section]
		field := strings.Fields(line)
		if len(field) != 2 {
			continue
		}
		switch field[0] {
		case "mem_limit:":
			m, err := strconv.Atoi(strings.TrimSuffix(field[1], "m"))
			if err != nil || !strings.HasSuffix(field[1], "m") {
				return r, nil, errors.New("unsupported Compose memory limit")
			}
			r.MemoryMB = m
			counts[section+"memory"]++
			if replacement != nil {
				lines[i] = fmt.Sprintf("    mem_limit: %dm", replacement.MemoryMB)
			}
		case "cpus:":
			c, err := strconv.ParseFloat(field[1], 64)
			if err != nil {
				return r, nil, err
			}
			r.CPUs = c
			counts[section+"cpu"]++
			if replacement != nil {
				lines[i] = fmt.Sprintf("    cpus: %.2f", replacement.CPUs)
			}
		}
		values[section] = r
	}
	r := values["wordpress"]
	for _, k := range []string{"wordpressmemory", "wordpresscpu", "dbmemory", "dbcpu"} {
		if counts[k] != 1 {
			return r, nil, errors.New("missing or duplicated Compose resource limits")
		}
	}
	if r != values["db"] {
		return r, nil, errors.New("WordPress and database resource limits differ; inspect Compose configuration")
	}
	if err := r.Validate(); err != nil {
		return r, nil, err
	}
	return r, []byte(strings.Join(lines, "\n")), nil
}

func (w *worker) configuredResources(id string) (core.Resources, error) {
	if !core.ValidID(id) {
		return core.Resources{}, errors.New("invalid site ID")
	}
	data, err := os.ReadFile(filepath.Join(w.siteDir(id), "compose.yaml"))
	if err != nil {
		return core.Resources{}, err
	}
	r, _, err := composeResources(data, nil)
	return r, err
}

func freeDisk(data []byte) (int64, error) {
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 2 {
		return 0, errors.New("disk capacity unavailable")
	}
	f := strings.Fields(lines[len(lines)-1])
	if len(f) < 4 {
		return 0, errors.New("invalid disk capacity")
	}
	n, err := strconv.ParseInt(f[3], 10, 64)
	if err != nil || n < 0 || n > 1<<50 {
		return 0, errors.New("invalid disk capacity")
	}
	return n * 1024, nil
}

func (w *worker) hostCapacity(ctx context.Context, excludeID string) (core.HostCapacity, error) {
	var h core.HostCapacity
	if excludeID != "" && !core.ValidID(excludeID) {
		return h, errors.New("invalid site ID")
	}
	info, err := w.docker.Output(ctx, "info", "--format", `{"cpus":{{.NCPU}},"memory_bytes":{{.MemTotal}}}`)
	if err != nil {
		return h, err
	}
	if err = json.Unmarshal(info, &h); err != nil {
		return h, err
	}
	if h.CPUs <= 0 || h.MemoryBytes <= 0 {
		return h, errors.New("Docker host capacity unavailable")
	}
	h.ReservedMemoryBytes = max(512*1024*1024, h.MemoryBytes/5)
	h.ReservedCPUs = .5
	allocated := map[string]core.Resources{}
	entries, err := os.ReadDir(w.root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return h, err
	}
	for _, entry := range entries {
		id := entry.Name()
		if !core.ValidID(id) || !entry.IsDir() || id == excludeID {
			continue
		}
		r, err := w.configuredResources(id)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return h, fmt.Errorf("capacity for site %s: %w", id, err)
		}
		allocated["wph-wp-"+id] = r
		allocated["wph-db-"+id] = r
	}
	// Include orphaned/stopped managed containers and larger live limits. Never
	// trust panel metadata to lower another site's reservation.
	out, err := w.docker.Output(ctx, "ps", "-a", "--format", `{"name":{{json .Names}},"project":{{json (.Label "com.docker.compose.project")}},"service":{{json (.Label "com.docker.compose.service")}},"state":{{json .State}}}`)
	if err != nil {
		return h, err
	}
	decoder := json.NewDecoder(bytes.NewReader(out))
	var names []string
	managed := map[string]bool{}
	other := map[string]bool{}
	for decoder.More() {
		var c struct{ Name, Project, Service, State string }
		if err := decoder.Decode(&c); err != nil {
			return h, err
		}
		id := strings.TrimPrefix(c.Project, "wph-")
		owned := core.ValidID(id) && (c.Service == "wordpress" && c.Name == "wph-wp-"+id || c.Service == "db" && c.Name == "wph-db-"+id)
		if owned {
			if id == excludeID {
				continue
			}
			names = append(names, c.Name)
			managed[c.Name] = true
		} else if c.State == "running" {
			names = append(names, c.Name)
			other[c.Name] = true
		}

	}
	if len(names) > 0 {
		args := append([]string{"inspect", "--format", `{"name":{{json .Name}},"memory":{{.HostConfig.Memory}},"cpus":{{.HostConfig.NanoCpus}}}`}, names...)
		out, err := w.docker.Output(ctx, args...)
		if err != nil {
			return h, err
		}
		decoder = json.NewDecoder(bytes.NewReader(out))
		var sampleNames []string
		samples := map[string]struct{ Memory, CPU bool }{}
		for decoder.More() {
			var c struct {
				Name         string
				Memory, CPUs int64
			}
			if err := decoder.Decode(&c); err != nil {
				return h, err
			}
			name := strings.TrimPrefix(c.Name, "/")
			if c.Memory < 0 || c.CPUs < 0 {
				return h, errors.New("invalid Docker resource limits")
			}
			if managed[name] {
				if c.Memory <= 0 || c.CPUs <= 0 {
					return h, errors.New("a managed container has no resource limit; inspect it before allocating capacity")
				}
				r := allocated[name]
				r.MemoryMB = max(r.MemoryMB, int((c.Memory+1024*1024-1)/(1024*1024)))
				r.CPUs = max(r.CPUs, float64(c.CPUs)/1e9)
				allocated[name] = r
			} else if other[name] {
				if c.Memory > 0 {
					h.OtherMemoryBytes += c.Memory
				}
				if c.CPUs > 0 {
					h.OtherCPUs += float64(c.CPUs) / 1e9
				}
				if c.Memory == 0 || c.CPUs == 0 {
					sampleNames = append(sampleNames, name)
					samples[name] = struct{ Memory, CPU bool }{c.Memory == 0, c.CPUs == 0}
				}
			} else {
				return h, errors.New("unexpected container in capacity inspection")
			}
		}
		if len(sampleNames) > 0 {
			args := append([]string{"stats", "--no-stream", "--format", `{"name":{{json .Name}},"memory":{{json .MemUsage}},"cpu":{{json .CPUPerc}}}`}, sampleNames...)
			out, err := w.docker.Output(ctx, args...)
			if err != nil {
				return h, err
			}
			decoder = json.NewDecoder(bytes.NewReader(out))
			seen := map[string]bool{}
			for decoder.More() {
				var c struct{ Name, Memory, CPU string }
				if err := decoder.Decode(&c); err != nil {
					return h, err
				}
				flags, ok := samples[c.Name]
				if !ok || seen[c.Name] {
					return h, errors.New("unexpected container statistics")
				}
				seen[c.Name] = true
				if flags.Memory {
					memory, err := memoryUsageBytes(c.Memory)
					if err != nil {
						return h, err
					}
					h.OtherMemoryBytes += memory
				}
				if flags.CPU {
					cpu, err := strconv.ParseFloat(strings.TrimSuffix(c.CPU, "%"), 64)
					if err != nil || math.IsNaN(cpu) || math.IsInf(cpu, 0) || cpu < 0 || cpu > 100000 {
						return h, errors.New("invalid container CPU usage")
					}
					h.OtherCPUs += cpu / 100
				}
			}
			if len(seen) != len(samples) {
				return h, errors.New("container usage changed during capacity inspection; refresh and retry")
			}
		}

	}
	for _, r := range allocated {
		h.AllocatedMemoryBytes += int64(r.MemoryMB) * 1024 * 1024
		h.AllocatedCPUs += r.CPUs
	}
	out, err = w.docker.Output(ctx, "run", "--rm", "--name", "wph-capacity-probe", "--network", "none", "--read-only", "--memory", "32m", "--cpus", "0.25", "alpine:3.22", "df", "-Pk", "/")
	if err != nil {
		return h, err
	}
	h.DockerDiskFreeBytes, err = freeDisk(out)
	if err != nil {
		return h, err
	}
	root := w.root
	for {
		if _, statErr := os.Stat(root); statErr == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			return h, errors.New("panel storage unavailable")
		}
		root = parent
	}
	out, err = exec.CommandContext(ctx, "df", "-Pk", root).Output()
	if err != nil {
		return h, err
	}
	h.DataDiskFreeBytes, err = freeDisk(out)
	return h, err
}

func (w *worker) checkCapacity(ctx context.Context, id string, r core.Resources) error {
	if err := r.Validate(); err != nil {
		return err
	}
	h, err := w.hostCapacity(ctx, id)
	if err != nil {
		return fmt.Errorf("unable to verify host capacity: %w", err)
	}
	return h.Check(r)
}

func atomicConfig(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".compose-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = f.Chmod(0600); err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

type admissionError struct{ error }

func (w *worker) changeResources(ctx context.Context, req core.ResourceRequest) (result core.Resources, operationErr error) {
	changed := false
	defer func() {
		if operationErr != nil && !changed {
			operationErr = &admissionError{operationErr}
		}
	}()
	r := req.Resources
	if err := core.ValidateSite(req.Site); err != nil {
		return r, err
	}
	if err := r.Validate(); err != nil {
		return r, err
	}
	state, err := w.runtimeSnapshot(ctx, req.Site.ID, false)
	if err != nil {
		return r, err
	}
	if state.Status != core.StatusRunning && state.Status != core.StatusStopped {
		return r, errors.New("repair the site's service status before changing resources")
	}
	if err := w.checkCapacity(ctx, req.Site.ID, r); err != nil {
		return r, err
	}
	path := filepath.Join(w.siteDir(req.Site.ID), "compose.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return r, err
	}
	_, updated, err := composeResources(data, &r)
	if err != nil {
		return r, err
	}
	if err := w.databaseAction(ctx, req.Site.ID, "stop"); err != nil {
		return r, err
	}
	// Store the desired configuration before touching containers. A crash is
	// reconciled from this file; Start reapplies it with the same volumes.
	changed = true
	if err := atomicConfig(path, updated); err != nil {
		return r, err
	}
	if err := w.docker.Run(ctx, w.composeArgs(req.Site.ID, "stop", "wordpress", "db")...); err != nil {
		return r, err
	}
	args := []string{"create", "--force-recreate", "wordpress", "db"}
	if state.Status == core.StatusRunning {
		args = []string{"up", "-d", "--wait", "wordpress", "db"}
	}
	if err := w.docker.Run(ctx, w.composeArgs(req.Site.ID, args...)...); err != nil {
		return r, err
	}
	return r, nil
}

func memoryUsageBytes(raw string) (int64, error) {
	value := strings.TrimSpace(strings.SplitN(raw, "/", 2)[0])
	n := 0
	for n < len(value) && ((value[n] >= '0' && value[n] <= '9') || value[n] == '.') {
		n++
	}
	if n == 0 {
		return 0, errors.New("invalid container memory usage")
	}
	amount, err := strconv.ParseFloat(value[:n], 64)
	if err != nil || amount < 0 || amount > 1<<40 {
		return 0, errors.New("invalid container memory usage")
	}
	multiplier := map[string]float64{"B": 1, "kB": 1000, "KB": 1000, "MB": 1e6, "GB": 1e9, "TB": 1e12, "KiB": 1024, "MiB": 1 << 20, "GiB": 1 << 30, "TiB": 1 << 40}[strings.TrimSpace(value[n:])]
	if multiplier == 0 || amount*multiplier > 1<<60 {
		return 0, errors.New("invalid container memory unit")
	}
	return int64(amount * multiplier), nil
}
