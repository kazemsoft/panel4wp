package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kazemsoft/panel4wp/internal/core"
)

var dockerContainerID = regexp.MustCompile(`^(?:[a-f0-9]{12}|[a-f0-9]{64})$`)
var dockerImageID = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

// Resolve the immutable image of this worker, rather than running a mutable tag
// supplied by a browser or pulling an extra image during a read-only request.
func (w *worker) storageProbeImage(ctx context.Context) (string, error) {
	if w.storageImage != "" {
		if !dockerImageID.MatchString(w.storageImage) {
			return "", errors.New("invalid cached worker image identity")
		}
		return w.storageImage, nil
	}
	hostname, err := os.Hostname()
	if err != nil || !dockerContainerID.MatchString(hostname) {
		return "", errors.New("storage probe requires a Docker worker with its default container hostname")
	}
	out, err := w.docker.Output(ctx, "inspect", "--format", "{{.Image}}", hostname)
	if err != nil {
		return "", err
	}
	image := strings.TrimSpace(string(out))
	if !dockerImageID.MatchString(image) {
		return "", errors.New("invalid worker image identity")
	}
	w.storageImage = image
	return image, nil
}

func (w *worker) storageCapability(ctx context.Context, id string) (core.StorageCapability, error) {
	if !core.ValidID(id) {
		return core.StorageCapability{}, errors.New("invalid site ID")
	}
	if _, err := os.Stat(filepath.Join(w.siteDir(id), "compose.yaml")); err != nil {
		return core.StorageCapability{}, err
	}
	out, err := w.runStorageProbe(ctx, id)
	if err != nil {
		var unsupported *volumeProbeError
		if errors.As(err, &unsupported) {
			return core.StorageCapability{Reason: unsupported.reason}, nil
		}
		return core.StorageCapability{}, err
	}
	return decodeStorageCapability(out)
}

type volumeProbeError struct{ reason string }

func (e *volumeProbeError) Error() string { return e.reason }

// Only existing owned local volumes and this worker's immutable image are used.
func (w *worker) runStorageProbe(ctx context.Context, id string, arguments ...string) ([]byte, error) {
	if !core.ValidID(id) {
		return nil, errors.New("invalid site ID")
	}
	volumes := []string{"wph-" + id + "_wordpress_data", "wph-" + id + "_database_data"}
	format := `{"name":{{json .Name}},"driver":{{json .Driver}},"project":{{json (index .Labels "com.docker.compose.project")}},"volume":{{json (index .Labels "com.docker.compose.volume")}},"options":{{if .Options}}true{{else}}false{{end}}}`
	out, err := w.docker.Output(ctx, append([]string{"volume", "inspect", "--format", format}, volumes...)...)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(out))
	for index, name := range volumes {
		var volume struct {
			Name, Driver, Project, Volume string
			Options                       bool
		}
		if err := decoder.Decode(&volume); err != nil {
			return nil, err
		}
		if volume.Name != name || volume.Project != "wph-"+id || volume.Volume != []string{"wordpress_data", "database_data"}[index] {
			return nil, &volumeProbeError{"volume-ownership"}
		}
		if volume.Driver != "local" || volume.Options {
			return nil, &volumeProbeError{"volume-driver"}
		}
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, errors.New("unexpected volume identity response")
	}
	image, err := w.storageProbeImage(ctx)
	if err != nil {
		return nil, err
	}
	args := []string{"run", "--rm", "--pull", "never", "--network", "none", "--read-only", "--cap-drop", "ALL", "--cap-add", "SYS_ADMIN", "--cap-add", "DAC_READ_SEARCH", "--security-opt", "no-new-privileges:true", "--pids-limit", "16", "--memory", "32m", "--cpus", "0.25", "--entrypoint", "/usr/local/bin/storage-probe", "--mount", "type=volume,src=" + volumes[0] + ",dst=/quota-wordpress,readonly", "--mount", "type=volume,src=" + volumes[1] + ",dst=/quota-database,readonly", image}
	return w.docker.Output(ctx, append(args, arguments...)...)
}

func decodeStorageCapability(out []byte) (core.StorageCapability, error) {
	var result core.StorageCapability
	decoder := json.NewDecoder(bytes.NewReader(out))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return result, errors.New("unexpected storage capability response")
	}
	switch result.Reason {
	case "supported":
		if !result.SupportsProjectQuotas() {
			return result, errors.New("inconsistent storage capability response")
		}
	case "filesystem", "different-filesystems", "kernel", "quota-query", "enforcement-disabled":
	default:
		return result, errors.New("invalid storage capability response")
	}
	for _, fs := range []string{result.WordPressFilesystem, result.DatabaseFilesystem} {
		switch fs {
		case "xfs", "ext4", "btrfs", "overlay", "tmpfs", "other":
		default:
			return result, errors.New("invalid filesystem type")
		}
	}
	return result, nil
}
