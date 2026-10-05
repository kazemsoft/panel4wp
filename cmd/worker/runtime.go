package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kazemsoft/panel4wp/internal/core"
)

func (w *worker) operationPath(id string) string {
	return filepath.Join(w.root, ".operations", id+".json")
}

func (w *worker) saveOperation(id string, op core.Operation) error {
	dir := filepath.Dir(w.operationPath(id))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.Marshal(op)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".receipt-*")
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
	if err = os.Rename(f.Name(), w.operationPath(id)); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func (w *worker) loadOperation(id string) (*core.Operation, error) {
	data, err := os.ReadFile(w.operationPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var op core.Operation
	if err := json.Unmarshal(data, &op); err != nil {
		return nil, err
	}
	return &op, nil
}

// Only long site operations receive receipts. Store no request body or credentials.
func (w *worker) trackOperation(req *http.Request, operationID string) (func(error, any) error, error) {
	if !core.ValidID(operationID) {
		return nil, errors.New("invalid operation ID")
	}
	switch req.URL.Path {
	case "/create", "/action", "/backup", "/restore", "/update", "/resources":
	default:
		return nil, errors.New("operation receipt not supported")
	}
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	req.Body = io.NopCloser(bytes.NewReader(raw))
	var body struct {
		Site   core.Site
		ID     string
		Action string
	}
	if json.Unmarshal(raw, &body) != nil {
		return nil, errors.New("invalid request")
	}
	id := body.Site.ID
	kind := strings.TrimPrefix(req.URL.Path, "/")
	if req.URL.Path == "/action" {
		id, kind = body.ID, body.Action
	}
	if !core.ValidID(id) {
		return nil, errors.New("invalid site ID")
	}
	previous, err := w.loadOperation(id)
	if err != nil {
		return nil, err
	}
	if previous != nil && (previous.ID == operationID || previous.State == "pending") {
		return nil, errors.New("operation already submitted or unresolved; inspect its receipt")
	}
	op := core.Operation{ID: operationID, Kind: kind, State: "pending", StartedAt: time.Now().UTC()}
	if err := w.saveOperation(id, op); err != nil {
		return nil, err
	}
	return func(operationErr error, result any) error {
		op.State, op.FinishedAt = "succeeded", time.Now().UTC()
		var rejected *admissionError
		if operationErr != nil && !errors.As(operationErr, &rejected) {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
			cleanupErr := w.quiesceSite(cleanupCtx, id, kind)
			cancel()
			if cleanupErr != nil {
				return fmt.Errorf("failed operation could not be fenced: %w", cleanupErr)
			}
			op.State, op.Error = "failed", "Worker operation failed; inspect the activity log."
		}
		if operationErr != nil {
			op.State, op.Error = "failed", "Worker operation failed; inspect the activity log."
		}
		if backup, ok := result.(core.Backup); ok {
			op.Backup = &backup
		}
		return w.saveOperation(id, op)
	}, nil
}

// Ask Docker for names and states only; never return inspect's environment/secrets.
func (w *worker) runtime(ctx context.Context, id string) (core.SiteRuntime, error) {
	return w.runtimeSnapshot(ctx, id, true)
}

func (w *worker) runtimeSnapshot(ctx context.Context, id string, recoverPending bool) (core.SiteRuntime, error) {
	if !core.ValidID(id) {
		return core.SiteRuntime{}, errors.New("invalid site ID")
	}
	snapshot := core.SiteRuntime{}
	if configured, configErr := w.configuredResources(id); configErr == nil {
		snapshot.Resources = &configured
	} else if !errors.Is(configErr, os.ErrNotExist) {
		return snapshot, configErr
	}
	op, err := w.loadOperation(id)
	if err != nil {
		return snapshot, err
	}
	if recoverPending && op != nil && op.State == "pending" {
		// CLI cancellation does not stop Docker daemon jobs or database execs.
		// Fence those before making the site available for any recovery action.
		if err := w.quiesceSite(ctx, id, op.Kind); err != nil {
			return snapshot, err
		}
		op.State, op.Error, op.FinishedAt = "interrupted", "Worker restarted before the operation finished.", time.Now().UTC()
		if err := w.saveOperation(id, *op); err != nil {
			return snapshot, err
		}
	}
	snapshot.Operation = op
	out, err := w.docker.Output(ctx, "ps", "-a", "--filter", "label=com.docker.compose.project=wph-"+id, "--format", "{{.ID}}")
	if err != nil {
		return snapshot, err
	}
	ids := strings.Fields(string(out))
	if len(ids) == 0 {
		snapshot.Status, snapshot.Detail = core.StatusUnhealthy, "Site containers are missing. Start the site to recreate services with existing volumes."
		return snapshot, nil
	}
	if len(ids) > 20 {
		return snapshot, errors.New("unexpected number of site containers")
	}
	format := `{"name":{{json .Name}},"project":{{json (index .Config.Labels "com.docker.compose.project")}},"service":{{json (index .Config.Labels "com.docker.compose.service")}},"status":{{json .State.Status}},"memory":{{.HostConfig.Memory}},"cpus":{{.HostConfig.NanoCpus}},"health":{{with (index .State "Health")}}{{json .Status}}{{else}}""{{end}}}`
	args := append([]string{"inspect", "--format", format}, ids...)
	out, err = w.docker.Output(ctx, args...)
	if err != nil {
		return snapshot, err
	}
	var wp, db *containerState
	decoder := json.NewDecoder(bytes.NewReader(out))
	for {
		var state containerState
		if err := decoder.Decode(&state); err == io.EOF {
			break
		} else if err != nil {
			return snapshot, fmt.Errorf("invalid Docker runtime response: %w", err)
		}
		if state.Project != "wph-"+id {
			continue
		}
		if state.Service == "wordpress" && state.Name == "/wph-wp-"+id {
			wp = &state
		}
		if state.Service == "db" && state.Name == "/wph-db-"+id {
			db = &state
		}
	}
	if wp == nil || db == nil {
		snapshot.Status, snapshot.Detail = core.StatusUnhealthy, "A site service is missing. Start the site to recreate it with existing volumes."
		return snapshot, nil
	}
	if wp.Status == "running" && db.Status == "running" && (wp.Health == "" || wp.Health == "healthy") && (db.Health == "" || db.Health == "healthy") {
		snapshot.Status = core.StatusRunning
	} else if (wp.Status == "exited" || wp.Status == "created") && (db.Status == "exited" || db.Status == "created") {
		snapshot.Status = core.StatusStopped
	} else {
		snapshot.Status, snapshot.Detail = core.StatusUnhealthy, fmt.Sprintf("WordPress: %s %s; database: %s %s. Start the site and inspect its logs if the problem persists.", wp.Status, wp.Health, db.Status, db.Health)
	}
	if snapshot.Resources != nil && (snapshot.Status == core.StatusRunning || snapshot.Status == core.StatusStopped) {
		r := *snapshot.Resources
		if wp.Memory != int64(r.MemoryMB)*1024*1024 || db.Memory != int64(r.MemoryMB)*1024*1024 || wp.CPUs != int64(r.CPUs*1e9) || db.CPUs != int64(r.CPUs*1e9) {
			snapshot.Status, snapshot.Detail = core.StatusUnhealthy, "Configured resources differ from Docker limits. Start the site to apply its saved configuration."
		}
	}
	if snapshot.Status == core.StatusRunning {
		if _, err := os.Stat(filepath.Join(w.routes, id+".caddy")); errors.Is(err, os.ErrNotExist) {
			snapshot.Status, snapshot.Detail = core.StatusUnhealthy, "Domain route is missing. Inspect the server route configuration."
		} else if err != nil {
			return snapshot, err
		}
	}
	return snapshot, nil
}

type containerState struct {
	Name, Project, Service, Status, Health string
	Memory, CPUs                           int64
}

// A worker restart invalidates in-memory tool sessions, so close them before
// accepting traffic. Site services and data are not changed.
func (w *worker) removeStaleDatabaseTools(ctx context.Context) error {
	format := `{ "name":{{json .Names}},"project":{{json (.Label "com.docker.compose.project")}} }`
	out, err := w.docker.Output(ctx, "ps", "-a", "--filter", "label=com.docker.compose.service=phpmyadmin", "--format", format)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(out))
	for {
		var tool struct{ Name, Project string }
		if err := decoder.Decode(&tool); err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		id := strings.TrimPrefix(tool.Name, "wph-pma-")
		if !core.ValidID(id) || tool.Name != "wph-pma-"+id || tool.Project != "wph-"+id {
			continue
		}
		if err := w.docker.Run(ctx, "rm", "-f", tool.Name); err != nil {
			return fmt.Errorf("close previous database session: %w", err)
		}
	}
	return nil
}

// Discover only helpers with explicit ownership. CLI containers carry Compose
// project/service labels; volume helpers carry panel4wp role/site labels.
func (w *worker) removeOwnedJobs(ctx context.Context, siteID string) error {
	format := `{"name":{{json .Names}},"project":{{json (.Label "com.docker.compose.project")}},"service":{{json (.Label "com.docker.compose.service")}},"site":{{json (.Label "panel4wp.site")}},"role":{{json (.Label "panel4wp.role")}}}`
	for _, filter := range []string{"label=panel4wp.role=job", "label=com.docker.compose.service=cli"} {
		out, err := w.docker.Output(ctx, "ps", "-a", "--filter", filter, "--format", format)
		if err != nil {
			return err
		}
		decoder := json.NewDecoder(bytes.NewReader(out))
		for {
			var job struct{ Name, Project, Service, Site, Role string }
			if err := decoder.Decode(&job); err == io.EOF {
				break
			} else if err != nil {
				return err
			}
			id := job.Site
			owned := job.Role == "job" && core.ValidID(id) && job.Name == "wph-job-"+id
			if job.Service == "cli" {
				id = strings.TrimPrefix(job.Project, "wph-")
				owned = core.ValidID(id) && job.Project == "wph-"+id
			}
			if !owned || (siteID != "" && id != siteID) {
				continue
			}
			if err := w.docker.Run(ctx, "rm", "-f", job.Name); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *worker) quiesceSite(ctx context.Context, id, kind string) error {
	if err := w.removeOwnedJobs(ctx, id); err != nil {
		return err
	}
	switch kind {
	case "restore", "update", "backup", "prepare-restore", "resources":
	default:
		return nil
	}
	// Restarting the DB later fences any old root import/dump process. Keep
	// WordPress stopped until the administrator explicitly recovers this site.
	format := `{"name":{{json .Names}},"project":{{json (.Label "com.docker.compose.project")}},"service":{{json (.Label "com.docker.compose.service")}}}`
	out, err := w.docker.Output(ctx, "ps", "--filter", "label=com.docker.compose.project=wph-"+id, "--format", format)
	if err != nil {
		return err
	}
	services := map[string]string{}
	decoder := json.NewDecoder(bytes.NewReader(out))
	for {
		var service struct{ Name, Project, Service string }
		if err := decoder.Decode(&service); err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		if service.Project != "wph-"+id {
			continue
		}
		if service.Service == "wordpress" && service.Name == "wph-wp-"+id || service.Service == "db" && service.Name == "wph-db-"+id {
			services[service.Service] = service.Name
		}
	}
	for _, name := range []string{services["wordpress"], services["db"]} {
		if name != "" {
			if err := w.docker.Run(ctx, "stop", "--time", "20", name); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *worker) recoverInterruptedJobs(ctx context.Context) error {
	if err := w.removeOwnedJobs(ctx, ""); err != nil {
		return err
	}
	entries, err := os.ReadDir(filepath.Join(w.root, ".operations"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !core.ValidID(id) || entry.Name() != id+".json" {
			continue
		}
		op, err := w.loadOperation(id)
		if err != nil {
			return err
		}
		if op == nil || op.State != "pending" {
			continue
		}
		if err := w.quiesceSite(ctx, id, op.Kind); err != nil {
			return err
		}
		op.State, op.Error, op.FinishedAt = "interrupted", "Worker restarted before the operation finished; remaining jobs were stopped.", time.Now().UTC()
		if err := w.saveOperation(id, *op); err != nil {
			return err
		}
	}
	return nil
}
