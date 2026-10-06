# Service status and restart recovery

The panel checks Docker's WordPress and MariaDB states on startup and every 30 seconds. **Refresh service status** checks immediately. The timestamp is the last attempted check, not proof that the public website is responding. A running state requires both containers running, healthy checks when present, and an existing domain-route file. HTTP content, DNS, and public certificate verification are separate release checks.

## Administrator actions

- **Status unavailable:** the worker or Docker could not be checked. Bring those services back and refresh; the panel preserves the previous saved state and shows an unknown badge.
- **Operation in progress:** the result is unconfirmed. Refresh after it finishes. Repeated site mutations are blocked until the receipt is resolved.
- **Stopped:** both site services are stopped. Start when desired.
- **Service issue:** one service is missing, unhealthy, restarting, paused, or inconsistent with the other. Start can recreate services using their existing volumes; inspect Docker logs if the issue persists. A missing domain route requires inspecting the route configuration.
- **Interrupted creation:** use Retry to finish the installation and receive a new administrator password. Database credentials and existing data are preserved. Even a completed worker install whose one-time password response was lost requires this retry.
- **Interrupted restore/update:** inspect the site's content and use a retained safety backup if required. A failed restore keeps WordPress stopped so partially restored data is not automatically served. A retained backup can be restored while stopped: the worker starts only MariaDB for the safety dump and keeps WordPress stopped until restore succeeds.
- **Interrupted deletion:** inspect the site and explicitly confirm Delete again if appropriate. The panel removes its metadata automatically only when a matching worker receipt confirms the deletion completed.

No interrupted operation is automatically replayed. For backups completed before a lost response, the matching worker receipt recovers the backup entry and its manual/scheduled/safety classification. A worker crash between completing a backup and committing its receipt can leave an unlisted backup directory; inspect protected backup storage before cleanup. A failed metadata save retains the backup.

## Persistence and boundaries

Panel markers live in `data/panel/sites.json`; worker receipts live in `data/sites/.operations`. Receipts contain operation IDs, kinds, timestamps, result state, and backup metadata, never passwords or request bodies. Back up these directories with the installation data.

Worker mutations are serialized. Runtime inspection waits for the active operation to finish by reporting a busy response, including when only the panel was restarted. After worker restart, owned CLI/volume helpers are stopped before the panel can recover an operation. Interrupted backup/restore/update work also stops WordPress and MariaDB to terminate surviving database exec processes. Only after successful fencing is a pending receipt marked interrupted; a cleanup failure keeps recovery blocked. Startup also closes owned phpMyAdmin containers, so an old browser database session must be reopened.

Panel runtime reconciliation does not restart or delete sites. The separate worker quota supervisor can resume only future quota-managed sites with verified storage, persisted running intent, and a resolved operation. A blocked site requires explicit recovery. Missing Docker services are not treated as confirmed deletion. Restarts do not expose the Docker socket to the panel.

## Future quota-managed sites

Quota allocation is not yet exposed. When the future allocator writes a trusted record, the worker disables Docker automatic restart and owns the verified resume decision. Missing intent means stopped; malformed records, failed enforcement checks, unresolved operations and failed mutations leave owned writers stopped/blocked. Restore maintenance never resumes automatically after worker restart. A verified resource admission rejection without effects retains the existing intent. The worker checks every 30 seconds and before writer operations; this is not an instantaneous guard against a trusted root administrator changing the host.

Keep internal quota records and startup intent in installation backups. Do not remove records, retag files or downgrade to a pre-gate worker to bypass a blocked state. Repair the storage and saved record using an allocator-aware version, then explicitly Start. See [the resource guide](RESOURCES.md#verified-restart-gate) for proof boundaries and pending allocation gates.

## Verification

Automated tests simulate a new panel/worker process over persisted storage and cover:

- external stops, unhealthy databases, project/container ownership, and missing services;
- pending, completed, failed, and interrupted receipts;
- lost start/create/delete/backup responses, preserved safety warnings, and idempotent polls;
- worker unavailability and a worker operation surviving a panel restart;
- blocked duplicate submissions and receipt persistence before side effects;
- incomplete provisioning directories and database tool startup cleanup;
- symlink upload/read/delete/move restrictions, normal uploads/listings, and trusted proxy login identities.

Run `go test -race ./...` and `go vet ./...`. PHP CLI is needed for the executable file-path security tests; the CI runner checks that it is present. Live verification uses a disposable site to test creation, a partial service outage, panel/worker restarts, start/stop, file operations, and deletion. Fresh Debian/Ubuntu VM reboots and public TLS verification remain in roadmap step 5.

Live Docker verification on 2026-10-05 passed the normal recovery checks using a disposable site. The test also uploaded through an outward dangling symlink and confirmed that no outside file was created and directory browsing still worked. The temporary site, volumes, backups and metadata were removed at completion; the existing demo site remained running.

A second disposable-site drill killed the worker with SIGKILL while an owned volume helper and a database exec process were active, with persisted pending restore markers. After explicitly starting the worker again, startup removed the helper, stopped WordPress and MariaDB, and recorded the interruption. Restoring a retained backup recovered the exact file content; neither delayed process could write its marker after recovery. The test site and its data were removed. This drill verifies crash fencing and restart recovery, not automatic restart after an administrator's `docker kill` command or a full host reboot.
