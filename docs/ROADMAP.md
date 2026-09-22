# Roadmap

The project is intentionally split into a safe single administrator product first and a multi customer hosting platform later. A milestone is complete only when its acceptance checks pass on a clean Linux host.

## Milestone 0 — executable proof (current)

- One administrator login with rate limiting, signed sessions, and CSRF checks
- Create one isolated WordPress and MariaDB Compose project per site
- Automatic routing and public TLS through Caddy
- Generated database and WordPress credentials; no Docker socket in the web panel
- Start, stop, retry, and destructive delete with typed confirmation
- Unit tests and a local end to end smoke test

Exit checks: a clean install creates a working local and public site; start/stop survive panel restarts; delete removes containers, networks, volumes, routes, and credentials.

## Milestone 1 — usable single administrator MVP

1. **Backups and restore:** scheduled and on demand backups of database plus `wp-content`, retention settings, checksum verification, and a tested full restore to a new site ID.
2. **File access:** browser file manager constrained to `wp-content`; optional per-site SFTP using public keys. Never expose the WordPress or database container directly.
3. **Database tools:** an on-demand Adminer or phpMyAdmin container behind the authenticated panel, with a short lived route and credentials. It stays stopped when unused.
4. **Resource controls:** configurable CPU, memory, process, and storage quotas with host capacity validation before creation.
5. **Site operations:** domain change, PHP settings, WordPress/plugin/theme updates, maintenance mode, logs, and health state reconciliation after host restarts.
6. **Host operations:** disk/RAM overview, image updates, certificate and backup alerts, audit log, password rotation, and recovery workflow.
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
