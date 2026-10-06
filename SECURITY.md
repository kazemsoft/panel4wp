# Security policy

This repository is an experimental MVP and has not yet had an independent security audit. Do not use it for untrusted customers or irreplaceable data.

## Reporting a vulnerability

Do not open a public issue for a suspected vulnerability. Contact the repository maintainer privately and include the affected version, reproduction steps, impact, and any suggested mitigation. A public security contact will be added before the first hosted release.

## Trust boundaries

The `worker` container controls Docker through `/var/run/docker.sock` and is therefore equivalent to a host administrator. It is reachable only on the internal Compose control network and requires a secret token. The browser-facing `panel` has no Docker socket. Caddy is the only service that publishes host ports.

Each managed site has a distinct WordPress container, MariaDB container, volumes, private database network, credentials, and Caddy route. This reduces accidental cross-site access, but containers sharing a Docker host are not a strong boundary against a hostile customer. Multi customer operation requires further isolation and a security review.

Storage quota activation is not implemented. Resources shows only a read-only filesystem capability result; it never presents support as an assigned quota. The probe checks existing Compose-owned local volumes, rejects volume-driver options, and mounts both roots read-only into the worker's immutable image with no network. Its fixed native entrypoint only queries filesystem and quota status; the extra `SYS_ADMIN` and `DAC_READ_SEARCH` capabilities are restricted to that probe container. It does not change project IDs, limits, mount options, or site data. The isolated kernel drill reproduced an inode-owner quota bypass at UID33; Versioned production writer seccomp profiles now block quota attribute mutation and retain Docker baseline protections. They apply to managed services, CLI and backup/restore helpers; service execs inherit the filter. Runtime checks compare actual Docker profile content, and older services require explicit Start to apply the saved migration. Real Docker tests cover UID33 and low-32-bit ioctl bypass variants. A gate for Docker/host restarts, durable project ownership and real quota-enabled site lifecycle tests are still required before activation. See `docs/RESOURCES.md` for the remaining acceptance checks.

WordPress frontends share a proxy network with Caddy so routes can be changed without attaching and detaching the public proxy during an administrator request. Databases never join that network. A compromised WordPress container can reach the HTTP port of another WordPress frontend, which is already publicly reachable through Caddy; it cannot use this network to reach another site's database.

Secrets are generated locally, stored beneath a mode `0700` data directory, excluded from Git and Docker build contexts, and mounted into site containers as files. Anyone with root or Docker access on the host can read them.

Global SMTP settings are encrypted at rest with AES-GCM using key material derived from `PANEL_SESSION_KEY`. The password is not returned to the browser after saving. A site with SMTP enabled must be able to use that credential, so its protected must-use plugin is readable by the WordPress process; a compromised enabled site can therefore recover its assigned SMTP credential. Use a dedicated, restricted mail account.

Backups are stored locally under the same protected data directory and contain site content plus a database dump. Checksums detect accidental corruption; they do not protect against an attacker who can rewrite both the files and manifest. Backups are not encrypted. Protect and copy them as sensitive production data.

The browser file manager accepts only normalized relative paths, resolves the target inside the WordPress container, and checks that the real path remains beneath `/var/www/html/wp-content`. File operations run as UID/GID 33 with PHP `open_basedir`, reject symbolic links (including dangling links and parent/root links), and replace uploaded files atomically. Site code shares the same UID and can modify its own files, so these controls do not make a compromised site trustworthy. Upload and download size is limited to 10 MB. These checks must remain in both the browser-facing panel and privileged worker.

Caddy overwrites the panel client-IP and proxy-token headers. The panel accepts that client identity only with the private proxy token; arbitrary forwarded headers are ignored. Caddy receives this token through its private environment and it is stripped before phpMyAdmin proxying. Login attempts reserve their per-client rate-limit slot before password verification.

The focused internal review on 2026-10-05 reproduced and fixed a dangling-link upload escape and shared-proxy login lockout, with regression tests. It also checked interrupted-operation receipts and database session cleanup. Surviving Docker helpers and database exec processes are fenced before an interrupted operation becomes available for recovery; a live SIGKILL/restart/restore drill verified this behavior. This is not an independent audit. Go vulnerability scanning found no reachable issue and the runtime npm dependency audit was clean at that review; a stack-exhaustion advisory in the development-only Tailwind watcher chain (`braces`, GHSA-vfj7-8cjw-p6xm) remains to be tracked before release. Docker image vulnerability scanning remains a release gate.
