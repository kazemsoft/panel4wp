# Security policy

This repository is an experimental MVP and has not yet had an independent security audit. Do not use it for untrusted customers or irreplaceable data.

## Reporting a vulnerability

Do not open a public issue for a suspected vulnerability. Contact the repository maintainer privately and include the affected version, reproduction steps, impact, and any suggested mitigation. A public security contact will be added before the first hosted release.

## Trust boundaries

The `worker` container controls Docker through `/var/run/docker.sock` and is therefore equivalent to a host administrator. It is reachable only on the internal Compose control network and requires a secret token. The browser-facing `panel` has no Docker socket. Caddy is the only service that publishes host ports.

Each managed site has a distinct WordPress container, MariaDB container, volumes, private database network, credentials, and Caddy route. This reduces accidental cross-site access, but containers sharing a Docker host are not a strong boundary against a hostile customer. Multi customer operation requires further isolation and a security review.

Secrets are generated locally, stored beneath a mode `0700` data directory, excluded from Git and Docker build contexts, and mounted into site containers as files. Anyone with root or Docker access on the host can read them.
