package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kazemsoft/panel4wp/internal/core"
)

var quotaDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)

type quotaAuthority struct {
	Version int    `json:"version"`
	Owner   string `json:"owner"`
	Daemon  string `json:"daemon"`
	State   string `json:"state"`
	UUID    string `json:"filesystem_uuid"`
	Phase   string `json:"phase"`
}

type quotaTagPlan struct {
	quotaAuthority
	SiteID    string `json:"site_id"`
	ProjectID uint32 `json:"project_id"`
}

func quotaHash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func validQuotaAuthority(a quotaAuthority) bool {
	return a.Version == 1 && quotaUUID.MatchString(a.Owner) && quotaUUID.MatchString(a.UUID) &&
		quotaDigest.MatchString(a.Daemon) && quotaDigest.MatchString(a.State) && (a.Phase == "pending" || a.Phase == "prepared")
}

func (w *worker) readQuotaPlan(id string) (*quotaTagPlan, error) {
	if !core.ValidID(id) {
		return nil, errors.New("invalid quota site")
	}
	data, err := readQuotaMetadata(filepath.Join(w.siteDir(id), "quota-plan.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var plan quotaTagPlan
	if err := strictQuotaJSON(data, &plan); err != nil {
		return nil, err
	}
	if !validQuotaAuthority(plan.quotaAuthority) || plan.SiteID != id || plan.ProjectID == 0 {
		return nil, errors.New("invalid quota preparation journal")
	}
	return &plan, nil
}

func (w *worker) quotaDaemon(ctx context.Context) (string, error) {
	out, err := w.docker.Output(ctx, "info", "--format", "{{.ID}}")
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(out))
	if len(id) < 8 || len(id) > 128 || strings.ContainsAny(id, "\r\n\x00 \t") {
		return "", errors.New("invalid Docker daemon identity")
	}
	return quotaHash(id), nil
}

// Consistent lock order: execution -> local ledger -> native filesystem lock.
// The native helper cannot access the local ledger or acquire locks in reverse.
// Internal preparation only: no API/UI caller, tagging, limits or ready marker.
func (w *worker) prepareQuotaTagging(ctx context.Context, id string) (err error) {
	if !core.ValidID(id) || !filepath.IsAbs(w.quotaRoot) || filepath.Clean(w.quotaRoot) != w.quotaRoot || w.quotaRoot == "/" || strings.ContainsAny(w.quotaRoot, ",\r\n\x00") {
		return errors.New("quota preparation requires an explicit dedicated filesystem root")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	w.executionMu.Lock()
	defer w.executionMu.Unlock()
	daemon, err := w.quotaDaemon(ctx)
	if err != nil {
		return err
	}
	state := quotaHash(filepath.Clean(w.root))
	plan, err := w.readQuotaPlan(id)
	if err != nil {
		return err
	}
	// A failed preparation remains fenced even if storage/env is later absent.
	defer func() {
		if err != nil {
			err = errors.Join(err, w.fenceQuotaSite(ctx, id, "blocked"))
		}
	}()
	var reservation quotaReservation
	if plan == nil {
		reservation, err = w.reserveQuotaProject(ctx, id)
		if err != nil {
			return err
		}
	}
	lock, fresh, err := w.lockQuotaLedger(ctx)
	if err != nil {
		return err
	}
	defer lock.Close()
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	ledger, err := w.loadQuotaLedger(fresh)
	if err != nil {
		return err
	}
	if plan != nil {
		for _, candidate := range ledger.Reservations {
			if candidate.SiteID == id {
				reservation = candidate
				break
			}
		}
		if reservation.ProjectID == 0 || plan.UUID != reservation.FilesystemUUID || plan.ProjectID != reservation.ProjectID || plan.Daemon != daemon || plan.State != state {
			return errors.New("quota preparation identity or reservation changed")
		}
	}
	path := filepath.Join(w.root, ".quota", "authority-"+reservation.FilesystemUUID+".json")
	var authority quotaAuthority
	data, readErr := readQuotaMetadata(path)
	if errors.Is(readErr, os.ErrNotExist) && plan == nil {
		token := make([]byte, 16)
		if _, err = rand.Read(token); err != nil {
			return err
		}
		authority = quotaAuthority{Version: 1, Owner: hex.EncodeToString(token), Daemon: daemon, State: state, UUID: reservation.FilesystemUUID, Phase: "pending"}
		body, _ := json.Marshal(authority)
		if err = atomicConfig(path, body); err != nil {
			return err
		}
	} else {
		if readErr != nil {
			return errors.New("quota authority state missing; recover complete metadata")
		}
		if err = strictQuotaJSON(data, &authority); err != nil {
			return err
		}
		if !validQuotaAuthority(authority) || authority.Daemon != daemon || authority.State != state || authority.UUID != reservation.FilesystemUUID {
			return errors.New("quota authority belongs to another installation or daemon")
		}
	}
	if plan != nil && plan.Owner != authority.Owner {
		return errors.New("quota owner changed")
	}
	if plan == nil {
		// The first durable launch fence precedes every cross-filesystem operation.
		guard := quotaGuard{Version: 1, Phase: "pending", ProjectID: reservation.ProjectID, FilesystemUUID: reservation.FilesystemUUID}
		body, _ := json.Marshal(guard)
		if err = atomicConfig(filepath.Join(w.siteDir(id), "quota.json"), body); err != nil {
			return err
		}
		plan = &quotaTagPlan{quotaAuthority: authority, SiteID: id, ProjectID: reservation.ProjectID}
		plan.Phase = "pending"
		body, _ = json.Marshal(plan)
		if err = atomicConfig(filepath.Join(w.siteDir(id), "quota-plan.json"), body); err != nil {
			return err
		}
	}
	if err = w.fenceQuotaSite(ctx, id, "blocked"); err != nil {
		return err
	}
	if err = w.ensureQuotaRestartCompose(id); err != nil {
		return err
	}
	states, err := w.quotaContainers(ctx, id)
	if err != nil {
		return err
	}
	for _, container := range states {
		if container.Restart != "no" || (container.Status != "exited" && container.Status != "created" && container.Status != "dead") {
			return errors.New("quota writer fencing could not be verified")
		}
	}
	// Catch other Docker workloads using the named volumes, irrespective of labels.
	for _, volume := range []string{"wordpress_data", "database_data"} {
		out, queryErr := w.docker.Output(ctx, "ps", "--filter", "volume=wph-"+id+"_"+volume, "--format", "{{.ID}}")
		if queryErr != nil {
			return queryErr
		}
		if strings.TrimSpace(string(out)) != "" {
			return errors.New("another active container uses quota site data")
		}
	}
	mode := "claim"
	// Existing per-site journals must not be recreated after metadata loss.
	if plan.Phase == "prepared" {
		mode = "existing"
	}
	out, err := w.runStorageHelper(ctx, id, "storage-authority", []string{"type=bind,src=" + w.quotaRoot + ",dst=/quota-filesystem"},
		"--prepare", authority.Owner, daemon, state, reservation.FilesystemUUID, id, strconv.FormatUint(uint64(reservation.ProjectID), 10), mode)
	if err != nil {
		return err
	}
	var result struct {
		Verified bool   `json:"verified"`
		Reason   string `json:"reason"`
	}
	if err = strictQuotaJSON(out, &result); err != nil {
		return err
	}
	if !result.Verified || result.Reason != "prepared" {
		return fmt.Errorf("quota ownership/journal preparation refused: %s", result.Reason)
	}
	authority.Phase = "prepared"
	body, _ := json.Marshal(authority)
	if err = atomicConfig(path, body); err != nil {
		return err
	}
	plan.Phase = "prepared"
	body, _ = json.Marshal(plan)
	return atomicConfig(filepath.Join(w.siteDir(id), "quota-plan.json"), body)
}
