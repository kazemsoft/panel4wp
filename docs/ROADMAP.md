# Roadmap

The project is intentionally split into a safe single administrator product first and a multi customer hosting platform later. A milestone is complete only when its acceptance checks pass on a clean Linux host.

## Approved development order

The administrator approved this order on 2026-10-05. Complete and test each step before moving to the next:

1. **Service reliability and restart recovery:** Docker runtime reconciliation, durable receipts for long site operations, interrupted-operation recovery guidance, and restart-safe database tool sessions. Implemented with focused restart and security regression tests; live Docker checks are recorded in `docs/RECOVERY.md`. Fresh Linux VM/host reboot acceptance remains part of step 5.
2. **Resources and capacity:** CPU/RAM edits, custom per-site allocations, reusable named plans, capacity admission before create/start/change, and a separate guided Resources page are implemented. Other running Docker workloads are included in the budget. Read-only storage capability detection and a reproducible Linux VM quota drill are implemented; the drill proves aggregate limits, inheritance, persistence over a guest reboot, and an inode-owner quota bypass. Versioned production writer profiles, actual Docker-filter verification, and an atomic saved-configuration migration are implemented with real container mutation tests. The verified restart gate is implemented with durable intent, native XFS UUID/project/limit checks, and an isolated guest Docker reboot control. Provisional durable project-ID reservations and read-only filesystem-global ownership/tree audits are implemented. Exclusive filesystem authority, writer fencing and durable pre-mutation inode journals are implemented with crash/reboot drills. Native tagging and journal-driven partial-tag retries are also implemented. Remaining: verified hard-limit activation, real site quota/restore lifecycle, and a data-preserving migration path. See `docs/RESOURCES.md`; step 2 remains open.
3. **File and site administration:** file creation/editing/copy/rename, bulk and archive operations, SFTP keys, domain changes, PHP settings, maintenance mode, and site logs.
4. **Remote backups and alerts:** off-host backup copies, encryption, restore into a new site, and notifications for backup failures, low capacity, and service/certificate problems.
5. **Release packaging and fresh VM tests:** versioned AMD64/ARM64 images, upgrade/rollback and data migration workflows, data-preserving uninstall, and installation/recovery tests on fresh Debian/Ubuntu VMs.

Multi-customer accounts, wallet/payments, multiple hosts, and AI editing remain a separate later phase.

## Milestone 0 — executable proof

- One administrator login with rate limiting, signed sessions, and CSRF checks
- Create one isolated WordPress and MariaDB Compose project per site
- Automatic routing and public TLS through Caddy
- Generated database and WordPress credentials; no Docker socket in the web panel
- Start, stop, retry, and destructive delete with typed confirmation
- Unit tests and a local end to end smoke test

Exit checks: a clean install creates a working local and public site; start/stop survive panel restarts; delete removes containers, networks, volumes, routes, and credentials.

## Milestone 1 — usable single administrator MVP

1. **Backups and restore:** on-demand and scheduled checksum-verified backups, retention, and in-place restore are complete. Remaining: off-host copies, encryption, and restore to a new site ID.
2. **File access:** browser file manager constrained to `wp-content` is complete for files up to 10 MB. Remaining: per-site SFTP using public keys, archive extraction, bulk operations, and detailed file-operation audit events.
3. **Database tools:** the on-demand phpMyAdmin manager behind the authenticated panel is complete, with a 15-minute session and startup cleanup.
4. **Resource controls:** CPU, memory and process limits, custom per-site allocations, edits after creation and host capacity admission are implemented. Remaining: enforced storage quotas.
5. **Site operations:** domain change, PHP settings, maintenance mode and logs remain. WordPress/plugin/theme updates with safety backups and runtime reconciliation are implemented.
6. **Host operations:** disk/RAM overview, image updates, certificate and backup alerts, password rotation, and recovery workflow.
7. **Packaging:** versioned images, release checksums, upgrade and rollback scripts, database/schema migrations, and an uninstall command that preserves site data by default.

Exit checks: restore drills pass; path traversal and cross-site access tests pass; upgrades and rollbacks preserve sites; failures leave an actionable status; security review has no critical or high findings.

## Milestone 2 — stable open source release

- Debian/Ubuntu installation matrix and automated integration tests in a fresh virtual machine
- ARM64 and AMD64 images with software bill of materials and signed provenance
- Prometheus compatible metrics, structured logs, documented recovery runbooks
- Contributor guide, threat model, support policy, semantic versioning, and vulnerability reporting
- At least one beta cycle on noncritical servers before a `1.0` release

## Later product — multi customer hosting

Build this as a separate control plane after the single administrator release is stable:

- Customer accounts, organizations, roles, email verification, MFA, and account recovery
- Plans, wallet ledger, payment provider integration, invoices, refunds, fraud and abuse controls
- Idempotent provisioning jobs, capacity scheduler, multiple worker hosts, quotas, and customer audit events
- Customer domain verification, metering, suspension and grace periods, support access controls
- AI site editing through explicit, reviewable WordPress operations with preview, approval, version history, rollback, token/cost limits, and content safety controls

The customer control plane must not receive a raw Docker socket or arbitrary shell access. Workers should expose a narrow, authenticated, versioned API and execute only declared operations.

## Deliberate scope choices

- Docker Compose is appropriate for the first single host product. A scheduler is evaluated only when multiple hosts are required.
- Caddy owns TLS and routes; the panel does not mount the Docker socket.
- Sites receive separate database containers and private database networks. This costs more memory but makes isolation and backup ownership clearer.
- FTP is excluded. SFTP with per-site keys is the planned file transfer mechanism.
- The number of sites is limited by host CPU, RAM, disk, and operational limits; the UI must not promise an unlimited capacity.
