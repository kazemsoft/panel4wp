package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/kazemsoft/panel4wp/internal/core"
)

type workerResponseError struct {
	status  int
	message string
}

func (e *workerResponseError) Error() string { return "worker: " + e.message }

// The marker is durable before the side effect. A lost response stays pending
// until the worker's receipt resolves it; the browser must not replay it.
func (a *app) callTracked(site *core.Site, kind, path string, body, result any) error {
	id, err := core.NewID()
	if err != nil {
		return err
	}
	site.Operation = &core.Operation{ID: id, Kind: kind, State: "pending", StartedAt: time.Now().UTC()}
	if err := a.store.Put(*site); err != nil {
		return fmt.Errorf("unable to record operation: %w", err)
	}
	err = a.callWorkerContext(context.Background(), path, body, result, id)
	if err != nil {
		site.Operation.Error = "Response was not confirmed. Refresh service status to resolve this operation."
	} else {
		site.Operation.State, site.Operation.FinishedAt = "succeeded", time.Now().UTC()
	}
	// On success, the handler commits this receipt together with final site/backup
	// metadata. Until that atomic write, the disk marker must remain pending.
	if err != nil {
		if saveErr := a.store.Put(*site); saveErr != nil {
			return fmt.Errorf("unable to save operation result: %w", saveErr)
		}
	}
	return err
}

func operationPending(site core.Site) bool {
	return site.Operation != nil && site.Operation.State == "pending"
}
func visibleStatus(site core.Site) string {
	if site.HealthError != "" {
		return "unknown"
	}
	if operationPending(site) {
		return "busy"
	}
	return string(site.Status)
}

func (a *app) runRuntimeReconciler(ctx context.Context, interval time.Duration) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		if err := a.reconcileRuntime(checkCtx); err != nil && ctx.Err() == nil {
			log.Printf("runtime reconciliation: %v", err)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (a *app) reconcileRuntime(ctx context.Context) error {
	if !a.opsMu.TryLock() {
		return errors.New("An operation is in progress. Try refreshing status when it finishes.")
	}
	defer a.opsMu.Unlock()
	sites, err := a.store.List()
	if err != nil {
		return err
	}
	var firstError error
	for _, site := range sites {
		checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		var snapshot core.SiteRuntime
		err := a.callWorkerContext(checkCtx, "/runtime", core.RuntimeRequest{SiteID: site.ID}, &snapshot, "")
		cancel()
		var responseError *workerResponseError
		if errors.As(err, &responseError) && responseError.status == 409 {
			return nil
		} // live worker operation, including one surviving a panel restart
		if err != nil {
			site.HealthError = "Service status could not be checked. Refresh status after the worker and Docker are available."
			if firstError == nil {
				firstError = err
			}
		} else {
			if snapshot.Status != core.StatusRunning && snapshot.Status != core.StatusStopped && snapshot.Status != core.StatusUnhealthy {
				return errors.New("invalid worker service status")
			}
			site.HealthError = ""
			if operationPending(site) {
				receipt := snapshot.Operation
				if receipt == nil || receipt.ID != site.Operation.ID {
					site.Operation.State, site.Operation.Error = "interrupted", "Operation was interrupted before a confirmed worker result."
				} else {
					// Keep the panel's kind: it knows whether the backup was manual/scheduled/safety.
					kind := site.Operation.Kind
					site.Operation = receipt
					site.Operation.Kind = kind
					if receipt.Backup != nil && receipt.State == "succeeded" {
						backup := *receipt.Backup
						switch kind {
						case "scheduled-backup":
							backup.Source = core.BackupSourceScheduled
						case "safety-backup":
							backup.Source = core.BackupSourceSafety
						default:
							backup.Source = core.BackupSourceManual
						}
						found := false
						for _, b := range site.Backups {
							if b.ID == backup.ID {
								found = true
							}
						}
						if !found {
							site.Backups = append(site.Backups, backup)
						}
					}
				}
				if site.Operation.Kind == "delete" && site.Operation.State == "succeeded" {
					if err := a.store.Delete(site.ID); err != nil {
						return err
					}
					a.record("operation-recovered", site, true, "confirmed deletion")
					continue
				}
				a.record("operation-recovered", site, site.Operation.State == "succeeded", site.Operation.Kind+": "+site.Operation.State)
				switch site.Operation.Kind {
				case "create":
					site.Status, site.Error = core.StatusFailed, "Site creation response was interrupted. Retry creation to finish installation and obtain a new WordPress administrator password. Existing data and database credentials are preserved."
				case "restore", "update":
					site.Status = snapshot.Status
					if site.Operation.State != "succeeded" {
						site.Error = "Restore or update was interrupted. Inspect the site before continuing; restore a retained safety backup if needed."
					} else {
						site.Error = snapshot.Detail
					}
				default:
					site.Status, site.Error = snapshot.Status, snapshot.Detail
					if site.Operation.State != "succeeded" {
						site.Error = site.Operation.Error + " Inspect the site before trying the operation again."
					}
				}
			} else if site.Status == core.StatusCreating || site.Status == core.StatusDeleting {
				oldStatus := site.Status
				site.Status, site.Error = core.StatusFailed, "Site creation was interrupted. Retry creation to finish installation and obtain a new administrator password."
				if oldStatus == core.StatusDeleting {
					site.Status, site.Error = core.StatusUnhealthy, "Deletion was interrupted. Inspect the site, then confirm Delete again if required."
				}
			} else if site.Status != core.StatusFailed {
				site.Status = snapshot.Status
				// Preserve recovery warnings until a confirmed user operation replaces them.
				if site.Operation == nil || site.Operation.State == "succeeded" {
					site.Error = snapshot.Detail
				}
			}
		}
		site.HealthCheckedAt = time.Now().UTC()
		if err := a.store.Put(site); err != nil {
			return err
		}
	}
	return firstError
}
