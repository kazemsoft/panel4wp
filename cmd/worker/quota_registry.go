package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/kazemsoft/panel4wp/internal/core"
)

const quotaLedgerLimit = 1 << 20
const quotaLedgerSentinel = "panel4wp-quota-ledger-v1\n"

// Provisional reservations only. One shared allocator/state root per filesystem
// is required; a live census cannot serialize another installation or host root.
// No reservation permits changing attributes, setting a limit or writing ready.
type quotaReservation struct {
	SiteID         string `json:"site_id"`
	FilesystemUUID string `json:"filesystem_uuid"`
	ProjectID      uint32 `json:"project_id"`
}

type quotaLedger struct {
	Version      int                `json:"version"`
	Reservations []quotaReservation `json:"reservations"`
}

type quotaInventory struct {
	Verified        bool     `json:"verified"`
	Reason          string   `json:"reason"`
	FilesystemUUID  string   `json:"filesystem_uuid"`
	ScannedInodes   uint64   `json:"scanned_inodes"`
	WordPressInodes uint64   `json:"wordpress_inodes"`
	DatabaseInodes  uint64   `json:"database_inodes"`
	ProjectIDs      []uint32 `json:"project_ids"`
}

func strictQuotaJSON(data []byte, target any) error {
	if len(data) > quotaLedgerLimit {
		return errors.New("quota metadata exceeds size limit")
	}
	keys := json.NewDecoder(bytes.NewReader(data))
	keys.UseNumber()
	if err := uniqueQuotaKeys(keys, 0); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("extra quota metadata")
	}
	return nil
}

func uniqueQuotaKeys(d *json.Decoder, depth int) error {
	if depth > 32 {
		return errors.New("quota metadata nesting limit")
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := token.(json.Delim); ok {
		if delimiter != '{' && delimiter != '[' {
			return errors.New("invalid quota metadata")
		}
		seen := map[string]bool{}
		for d.More() {
			if delimiter == '{' {
				key, err := d.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return errors.New("duplicate quota metadata key")
				}
				seen[name] = true
			}
			if err := uniqueQuotaKeys(d, depth+1); err != nil {
				return err
			}
		}
		_, err = d.Token()
		return err
	}
	return nil
}

func readQuotaMetadata(path string) ([]byte, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	stat, err := f.Stat()
	if err != nil || !stat.Mode().IsRegular() {
		return nil, errors.New("quota metadata must be a regular file")
	}
	return io.ReadAll(io.LimitReader(f, quotaLedgerLimit+1))
}

func validateQuotaLedger(ledger quotaLedger) error {
	if ledger.Version != 1 || ledger.Reservations == nil || len(ledger.Reservations) > 4096 {
		return errors.New("unsupported quota ledger")
	}
	ids, sites := map[string]bool{}, map[string]bool{}
	for _, entry := range ledger.Reservations {
		key := fmt.Sprintf("%s:%d", entry.FilesystemUUID, entry.ProjectID)
		if !core.ValidID(entry.SiteID) || !quotaUUID.MatchString(entry.FilesystemUUID) || entry.ProjectID == 0 || ids[key] || sites[entry.SiteID] {
			return errors.New("invalid or conflicting quota reservation")
		}
		ids[key], sites[entry.SiteID] = true, true
	}
	return nil
}

func (w *worker) storageInventory(ctx context.Context, id string) (quotaInventory, error) {
	out, err := w.runStorageProbe(ctx, id, "--inventory")
	var result quotaInventory
	if err != nil {
		return result, err
	}
	if err = strictQuotaJSON(out, &result); err != nil {
		return result, err
	}
	if !result.Verified || result.Reason != "verified" || !quotaUUID.MatchString(result.FilesystemUUID) || result.ScannedInodes == 0 ||
		result.ScannedInodes > 1000000 || result.WordPressInodes == 0 || result.WordPressInodes > 1000000 ||
		result.DatabaseInodes == 0 || result.DatabaseInodes > 1000000 || result.ProjectIDs == nil || len(result.ProjectIDs) > 65536 {
		return result, errors.New("quota ownership inventory is incomplete or unsafe")
	}
	if result.WordPressInodes+result.DatabaseInodes > result.ScannedInodes {
		return result, errors.New("quota inode inventory changed or overlaps")
	}
	var previous uint32
	for _, id := range result.ProjectIDs {
		if id == 0 || id <= previous {
			return result, errors.New("invalid quota census identifiers")
		}
		previous = id
	}
	return result, nil
}

func syncQuotaDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// The separate lock inode is never renamed or unlinked. Atomic ledger renames
// therefore cannot let another process acquire a different lock inode.
func (w *worker) lockQuotaLedger(ctx context.Context) (*os.File, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	// Acquire the persistent root-level lock before even creating .quota.
	// Concurrent first-time callers cannot observe half-initialized state.
	fd, err := syscall.Open(filepath.Join(w.root, ".quota-lock"), syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return nil, false, err
	}
	f := os.NewFile(uintptr(fd), "quota registry lock")
	retained := false
	defer func() {
		if !retained {
			f.Close()
		}
	}()
	lockStat, err := f.Stat()
	if err != nil || !lockStat.Mode().IsRegular() {
		return nil, false, errors.New("quota lock must be a regular file")
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			return nil, false, err
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	dir := filepath.Join(w.root, ".quota")
	stat, err := os.Lstat(dir)
	fresh := errors.Is(err, os.ErrNotExist)
	if err != nil && !fresh {
		return nil, false, err
	}
	if !fresh && !stat.IsDir() {
		return nil, false, errors.New("quota registry directory is not a directory")
	}
	if fresh {
		if err := os.Mkdir(dir, 0700); err != nil {
			return nil, false, err
		}
	}
	if err := syncQuotaDirectory(w.root); err != nil {
		return nil, false, err
	}
	retained = true
	return f, fresh, nil
}

func (w *worker) loadQuotaLedger(fresh bool) (quotaLedger, error) {
	var ledger quotaLedger
	path := filepath.Join(w.root, ".quota", "projects.json")
	sentinelPath := filepath.Join(w.root, ".quota-initialized")
	data, ledgerErr := readQuotaMetadata(path)
	sentinel, sentinelErr := readQuotaMetadata(sentinelPath)
	if fresh && errors.Is(ledgerErr, os.ErrNotExist) && errors.Is(sentinelErr, os.ErrNotExist) {
		ledger = quotaLedger{Version: 1, Reservations: []quotaReservation{}}
		entries, err := os.ReadDir(w.root)
		if err != nil {
			return ledger, err
		}
		for _, entry := range entries {
			if !core.ValidID(entry.Name()) {
				continue
			}
			if marked, err := w.quotaMarked(entry.Name()); marked || err != nil {
				return ledger, errors.New("existing quota markers require ledger recovery")
			}
		}
		// Sentinel lives outside .quota: losing that directory cannot silently
		// reset burned reservations. Interrupted initialization fails closed.
		if err := atomicConfig(sentinelPath, []byte(quotaLedgerSentinel)); err != nil {
			return ledger, err
		}
		body, _ := json.Marshal(ledger)
		return ledger, atomicConfig(path, body)
	}
	if ledgerErr != nil || sentinelErr != nil || string(sentinel) != quotaLedgerSentinel {
		return ledger, errors.New("quota ledger is missing or uninitialized; restore complete installation metadata")
	}
	if err := strictQuotaJSON(data, &ledger); err != nil {
		return ledger, err
	}
	return ledger, validateQuotaLedger(ledger)
}

func (w *worker) reserveQuotaProject(ctx context.Context, id string) (quotaReservation, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var result quotaReservation
	if !core.ValidID(id) {
		return result, errors.New("invalid site ID")
	}
	if _, err := os.Stat(filepath.Join(w.siteDir(id), "compose.yaml")); err != nil {
		return result, err
	}
	if marked, err := w.quotaMarked(id); marked || err != nil {
		return result, errors.New("quota-managed sites require ownership recovery, not a new reservation")
	}
	lock, fresh, err := w.lockQuotaLedger(ctx)
	if err != nil {
		return result, err
	}
	defer lock.Close()
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	ledger, err := w.loadQuotaLedger(fresh)
	if err != nil {
		return result, err
	}
	inventory, err := w.storageInventory(ctx, id)
	if err != nil {
		return result, err
	}
	occupied := map[uint32]bool{}
	for _, project := range inventory.ProjectIDs {
		occupied[project] = true
	}
	for _, entry := range ledger.Reservations {
		if entry.SiteID == id {
			if entry.FilesystemUUID != inventory.FilesystemUUID || occupied[entry.ProjectID] {
				return result, errors.New("reserved project changed filesystem or became occupied; inspect ownership")
			}
			return entry, nil
		}
	}
	for _, entry := range ledger.Reservations {
		if entry.FilesystemUUID == inventory.FilesystemUUID {
			occupied[entry.ProjectID] = true
		}
	}
	if len(ledger.Reservations) >= 4096 {
		return result, errors.New("quota ledger reservation limit reached")
	}
	project := uint32(1000000)
	for occupied[project] {
		if project == ^uint32(0) {
			return result, errors.New("quota identifiers exhausted")
		}
		project++
	}
	result = quotaReservation{SiteID: id, FilesystemUUID: inventory.FilesystemUUID, ProjectID: project}
	ledger.Reservations = append(ledger.Reservations, result)
	body, err := json.Marshal(ledger)
	if err != nil || len(body) > quotaLedgerLimit {
		return quotaReservation{}, errors.New("quota ledger exceeds size limit")
	}
	if err := atomicConfig(filepath.Join(w.root, ".quota", "projects.json"), body); err != nil {
		return quotaReservation{}, err
	}
	return result, nil
}
