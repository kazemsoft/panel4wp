package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kazemsoft/panel4wp/internal/core"
	"github.com/kazemsoft/panel4wp/internal/securitypolicy"
)

// Reserved internal contract for the future allocator. There is intentionally
// no browser/API activation endpoint. Presence (even invalid) enables fencing.
type quotaGuard struct {
	Version        int    `json:"version"`
	Phase          string `json:"phase"`
	ProjectID      uint32 `json:"project_id"`
	HardBytes      uint64 `json:"hard_bytes"`
	FilesystemUUID string `json:"filesystem_uuid"`
}

type quotaIntent struct {
	Version int    `json:"version"`
	State   string `json:"state"`
}

var quotaUUID = regexp.MustCompile(`^[a-f0-9]{32}$`)

func readGuardJSON(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil {
		return err
	}
	if len(data) > 4096 {
		return errors.New("oversized quota state")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("extra quota state")
	}
	return nil
}

func (w *worker) loadQuotaGuard(id string) (*quotaGuard, error) {
	if !core.ValidID(id) {
		return nil, errors.New("invalid site ID")
	}
	var guard quotaGuard
	err := readGuardJSON(filepath.Join(w.siteDir(id), "quota.json"), &guard)
	if errors.Is(err, os.ErrNotExist) {
		marked, markErr := w.quotaMarked(id)
		if marked || markErr != nil {
			return nil, errors.New("quota marker is unreadable")
		}
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("invalid quota record: %w", err)
	}
	if guard.Version != 1 || guard.Phase != "ready" || guard.ProjectID == 0 || guard.HardBytes == 0 || guard.HardBytes%512 != 0 || !quotaUUID.MatchString(guard.FilesystemUUID) {
		return nil, errors.New("quota record is incomplete or not ready")
	}
	return &guard, nil
}

func (w *worker) quotaMarked(id string) (bool, error) {
	if !core.ValidID(id) {
		return false, errors.New("invalid site ID")
	}
	_, err := os.Lstat(filepath.Join(w.siteDir(id), "quota.json"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return true, err
}

func (w *worker) loadQuotaIntent(id string) (quotaIntent, error) {
	intent := quotaIntent{Version: 1, State: "stopped"}
	err := readGuardJSON(filepath.Join(w.siteDir(id), "quota-intent.json"), &intent)
	if errors.Is(err, os.ErrNotExist) {
		return quotaIntent{Version: 1, State: "stopped"}, nil
	}
	if err != nil {
		return intent, err
	}
	if intent.Version != 1 || !validQuotaIntent(intent.State) {
		return intent, errors.New("invalid quota startup intent")
	}
	return intent, nil
}
func (w *worker) saveQuotaIntent(id, state string) error {
	if !core.ValidID(id) || !validQuotaIntent(state) {
		return errors.New("invalid quota intent")
	}
	if current, err := w.loadQuotaIntent(id); err == nil && current.State == state {
		if _, err := os.Stat(filepath.Join(w.siteDir(id), "quota-intent.json")); err == nil {
			return nil
		}
	}
	data, _ := json.Marshal(quotaIntent{Version: 1, State: state})
	return atomicConfig(filepath.Join(w.siteDir(id), "quota-intent.json"), data)
}

func validQuotaIntent(state string) bool {
	return state == "running" || state == "stopped" || state == "blocked" || state == "maintenance"
}

func (w *worker) setQuotaIntent(id, state string) error {
	marked, err := w.quotaMarked(id)
	if err != nil || !marked {
		return err
	}
	return w.saveQuotaIntent(id, state)
}

func quotaRestartCompose(data []byte) ([]byte, error) {
	lines := strings.Split(string(data), "\n")
	section := ""
	seen := map[string]int{}
	for n, line := range lines {
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") && strings.HasSuffix(line, ":") {
			section = strings.TrimSuffix(strings.TrimSpace(line), ":")
		}
		if (section == "wordpress" || section == "db") && strings.HasPrefix(line, "    restart:") {
			if line != "    restart: unless-stopped" && line != `    restart: "no"` {
				return nil, errors.New("unfamiliar quota restart configuration")
			}
			seen[section]++
			lines[n] = `    restart: "no"`
		}
	}
	if seen["wordpress"] != 1 || seen["db"] != 1 {
		return nil, errors.New("missing quota restart policy")
	}
	return []byte(strings.Join(lines, "\n")), nil
}

func (w *worker) ensureQuotaRestartCompose(id string) error {
	if err := w.ensureWriterPolicy(id); err != nil {
		return err
	}
	path := filepath.Join(w.siteDir(id), "compose.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	updated, err := quotaRestartCompose(data)
	if err != nil {
		return err
	}
	if bytes.Equal(data, updated) {
		return nil
	}
	return atomicConfig(path, updated)
}

func (w *worker) quotaContainers(ctx context.Context, id string) ([]containerState, error) {
	out, err := w.docker.Output(ctx, "ps", "-a", "--filter", "label=com.docker.compose.project=wph-"+id, "--format", "{{.ID}}")
	if err != nil {
		return nil, err
	}
	ids := strings.Fields(string(out))
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > 20 {
		return nil, errors.New("unexpected number of quota containers")
	}
	format := `{"name":{{json .Name}},"project":{{json (index .Config.Labels "com.docker.compose.project")}},"service":{{json (index .Config.Labels "com.docker.compose.service")}},"status":{{json .State.Status}},"restart":{{json .HostConfig.RestartPolicy.Name}},"security_opts":{{json .HostConfig.SecurityOpt}}}`
	out, err = w.docker.Output(ctx, append([]string{"inspect", "--format", format}, ids...)...)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(out))
	var states []containerState
	for {
		var state containerState
		err := decoder.Decode(&state)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		expected := map[string]string{"wordpress": "/wph-wp-", "db": "/wph-db-", "phpmyadmin": "/wph-pma-"}[state.Service]
		if state.Project == "wph-"+id && expected != "" && state.Name == expected+id {
			states = append(states, state)
		}
	}
	return states, nil
}

// Save blocked/stopped intent before stopping writers, so a worker crash cannot
// reinterpret this as permission to resume. Never mutate another project's job.
func (w *worker) fenceQuotaSite(ctx context.Context, id, state string) error {
	// Cleanup must survive a cancelled request or an expired probe deadline.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	intentErr := w.saveQuotaIntent(id, state)
	jobsErr := w.removeOwnedJobs(ctx, id)
	containers, err := w.quotaContainers(ctx, id)
	if err != nil {
		return errors.Join(intentErr, jobsErr, err)
	}
	var failures []error
	for _, container := range containers {
		name := strings.TrimPrefix(container.Name, "/")
		if err := w.docker.Run(ctx, "update", "--restart", "no", name); err != nil {
			failures = append(failures, err)
		}
		if container.Status == "running" || container.Status == "restarting" || container.Status == "paused" {
			if err := w.docker.Run(ctx, "stop", "--time", "20", name); err != nil {
				failures = append(failures, err)
			}
		}
	}
	return errors.Join(append(failures, intentErr, jobsErr)...)
}

func (w *worker) verifyQuotaStorage(ctx context.Context, id string, guard *quotaGuard) error {
	if guard == nil {
		return errors.New("quota record disappeared during verification")
	}
	out, err := w.runStorageProbe(ctx, id, "--guard", strconv.FormatUint(uint64(guard.ProjectID), 10), strconv.FormatUint(guard.HardBytes, 10), guard.FilesystemUUID)
	if err != nil {
		return fmt.Errorf("quota verification failed: %w", err)
	}
	var result struct {
		Verified bool   `json:"verified"`
		Reason   string `json:"reason"`
	}
	decoder := json.NewDecoder(bytes.NewReader(out))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("extra quota verification output")
	}
	if !result.Verified || result.Reason != "verified" {
		return errors.New("quota enforcement, project attributes or hard limit could not be verified")
	}
	return nil
}

// Used before every path that can launch a writer. Non-quota sites are unchanged.
func (w *worker) quotaPreflight(ctx context.Context, id string) error {
	marked, err := w.quotaMarked(id)
	if !marked && err == nil {
		return nil
	}
	guard, loadErr := w.loadQuotaGuard(id)
	if err == nil {
		err = loadErr
		if guard == nil && err == nil {
			err = errors.New("quota marker disappeared during verification")
		}
	}
	if err == nil {
		err = w.ensureQuotaRestartCompose(id)
	}
	if err == nil {
		err = w.verifyQuotaStorage(ctx, id, guard)
	}
	if err == nil {
		var states []containerState
		states, err = w.quotaContainers(ctx, id)
		for _, state := range states {
			if state.Restart != "no" || !securitypolicy.Active(state.SecurityOpts) {
				err = errors.New("quota writers need restart=no and the current security profile")
				break
			}
		}
	}
	if err != nil {
		return errors.Join(err, w.fenceQuotaSite(ctx, id, "blocked"))
	}
	return nil
}

func (w *worker) finishQuotaWrite(ctx context.Context, id string, result error) error {
	marked, markErr := w.quotaMarked(id)
	if !marked && markErr == nil {
		return result
	}
	var rejected *admissionError
	if markErr == nil && errors.As(result, &rejected) && rejected.quotaSafe {
		if err := w.quotaPreflight(ctx, id); err != nil {
			// Drop the rejection type: the post-check failed and fencing occurred.
			return errors.Join(errors.New(result.Error()), err)
		}
		return result
	}
	if result != nil || markErr != nil {
		return errors.Join(result, markErr, w.fenceQuotaSite(ctx, id, "blocked"))
	}
	return w.quotaPreflight(ctx, id)
}

func (w *worker) prepareRestore(ctx context.Context, id string) (err error) {
	defer func() { err = w.finishQuotaWrite(ctx, id, err) }()
	if err = w.quotaPreflight(ctx, id); err != nil {
		return err
	}
	if err = w.ensureWriterPolicy(id); err != nil {
		return err
	}
	if err = w.setQuotaIntent(id, "maintenance"); err != nil {
		return err
	}
	if err = w.docker.Run(ctx, w.composeArgs(id, "stop", "wordpress")...); err != nil {
		return err
	}
	return w.docker.Run(ctx, w.composeArgs(id, "up", "-d", "--wait", "db")...)
}

func (w *worker) startQuotaSite(ctx context.Context, id string) error {
	if err := w.fenceQuotaSite(ctx, id, "blocked"); err != nil {
		return err
	}
	if err := w.quotaPreflight(ctx, id); err != nil {
		return err
	}
	resources, err := w.configuredResources(id)
	if err != nil {
		return err
	}
	if err = w.checkCapacity(ctx, id, resources); err != nil {
		return err
	}
	if err = w.docker.Run(ctx, w.composeArgs(id, "up", "-d", "--wait", "wordpress", "db")...); err != nil {
		return errors.Join(err, w.fenceQuotaSite(ctx, id, "blocked"))
	}
	// Inspect the created services; no labels alone are accepted as proof.
	if err = w.quotaPreflight(ctx, id); err != nil {
		return err
	}
	states, err := w.quotaContainers(ctx, id)
	count := 0
	for _, state := range states {
		if state.Service == "wordpress" || state.Service == "db" {
			if state.Status != "running" {
				err = errors.New("quota service did not start")
			}
			count++
		}
	}
	if count != 2 && err == nil {
		err = errors.New("quota service is missing after start")
	}
	if err == nil {
		err = w.saveQuotaIntent(id, "running")
	}
	if err != nil {
		return errors.Join(err, w.fenceQuotaSite(ctx, id, "blocked"))
	}
	return nil
}

func (w *worker) reconcileQuotaSites(ctx context.Context, startup bool) error {
	entries, err := os.ReadDir(w.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var failures []error
	for _, entry := range entries {
		id := entry.Name()
		if !entry.IsDir() || !core.ValidID(id) {
			continue
		}
		marked, markErr := w.quotaMarked(id)
		if !marked && markErr == nil {
			continue
		}
		guard, loadErr := w.loadQuotaGuard(id)
		intent, intentErr := w.loadQuotaIntent(id)
		op, opErr := w.loadOperation(id)
		resolved := op == nil || op.State == "succeeded" || (op.Kind == "resources" && op.State == "failed" && op.AdmissionRejected)
		if markErr != nil || guard == nil || loadErr != nil || intentErr != nil || opErr != nil || !resolved {
			if err := w.fenceQuotaSite(ctx, id, "blocked"); err != nil {
				failures = append(failures, err)
			}
			continue
		}
		if intent.State != "running" && (startup || intent.State != "maintenance") {
			if intent.State == "maintenance" {
				intent.State = "blocked"
			}
			if err := w.fenceQuotaSite(ctx, id, intent.State); err != nil {
				failures = append(failures, err)
			}
			continue
		}
		if err := w.ensureQuotaRestartCompose(id); err != nil {
			if fenceErr := w.fenceQuotaSite(ctx, id, "blocked"); fenceErr != nil {
				failures = append(failures, fenceErr)
			}
			continue
		}
		if err := w.verifyQuotaStorage(ctx, id, guard); err != nil {
			if fenceErr := w.fenceQuotaSite(ctx, id, "blocked"); fenceErr != nil {
				failures = append(failures, fenceErr)
			}
			continue
		}
		states, err := w.quotaContainers(ctx, id)
		safe := err == nil && len(states) >= 2
		running := safe
		count := 0
		for _, state := range states {
			if state.Restart != "no" || !securitypolicy.Active(state.SecurityOpts) {
				safe = false
			}
			if state.Service == "wordpress" || state.Service == "db" {
				count++
				if state.Status != "running" {
					running = false
				}
			}
		}
		if !safe {
			if fenceErr := w.fenceQuotaSite(ctx, id, "blocked"); fenceErr != nil {
				failures = append(failures, fenceErr)
			}
			continue
		}
		// A verified DB may stay up between prepare-restore and restore requests.
		// A worker restart always fences maintenance, and it never auto-resumes.
		if intent.State == "maintenance" {
			for _, state := range states {
				if state.Service == "wordpress" && state.Status != "exited" && state.Status != "created" && state.Status != "dead" {
					if err := w.fenceQuotaSite(ctx, id, "blocked"); err != nil {
						failures = append(failures, err)
					}
					break
				}
			}
			continue
		}
		if !running || count != 2 {
			if err := w.startQuotaSite(ctx, id); err != nil {
				log.Printf("quota site %s did not resume: %v", id, err)
			}
		}
	}
	return errors.Join(failures...)
}

func (w *worker) runQuotaSupervisor(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !w.executionMu.TryLock() {
				continue
			}
			checkCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			err := w.reconcileQuotaSites(checkCtx, false)
			cancel()
			w.executionMu.Unlock()
			if err != nil {
				log.Printf("quota restart supervisor: %v", err)
			}
		}
	}
}
