# WP Host Panel

An early, self hosted, open source WordPress server panel. A single server administrator can create, start, stop, and remove independent WordPress sites from a browser. Each site gets its own WordPress and MariaDB containers, Docker volumes, credentials, and domain route. Caddy handles HTTPS for public domains.

**Status: experimental MVP.** Do not use it for paying customers or irreplaceable data yet. File management, database UI, SFTP, resource selection, monitoring, upgrades, off-host backups, and customer accounts are planned but not implemented. The current worker has access to the Docker socket and must be treated as a privileged part of the host.

## Requirements

- Linux server with Docker Engine and the Compose plugin
- Ports 80 and 443 available; enough RAM and disk for the number of sites you create
- For a public panel or site, an A/AAAA DNS record pointing to the server and inbound ports 80/443

Docker Desktop on macOS may be used for local development. The installer itself requires Linux.

## Install

Clone this repository and run from its directory:

```sh
./install.sh panel.example.com
```

The installer builds the panel and worker, writes private secrets to `.env`, starts the services, and prints a randomly generated administrator password once. Save it immediately. The panel is then available at `https://panel.example.com` after DNS resolves and Caddy obtains a certificate.

For a local experiment on a Linux machine, run `./install.sh` without a domain. This uses `http://localhost`. Leave the domain field empty when creating a site to get an `http://<id>.localhost` address on the same machine.

## Operation

Log in with the password printed by the installer. Create a site using its domain, title, WordPress administrator email, and a Small, Standard, or Large resource plan. The selected memory and CPU limits apply independently to its WordPress and MariaDB containers. The panel shows the WordPress `admin` password once after creation. Save it immediately. Site creation downloads the WordPress and MariaDB images and can take several minutes on the first run. Visit `/wp-admin` on the site's domain to manage WordPress content.

The panel supports Start, Stop, Retry after failed creation, verified Backup, in-place Restore, and permanent Delete. A backup contains a consistent MariaDB dump, the complete WordPress volume, a manifest, and SHA-256 checksums. Restore first creates and retains a safety backup, then verifies the selected backup before replacing data. Local backups live under `data/backups/<site-id>`.

Deleting a site removes its containers, Docker volumes, local backups, and all credentials. Enter the exact domain to confirm. Copy important backups to separate storage because local backups are lost with the server or disk.

Update the panel from its repository directory with:

```sh
docker compose up -d --build
```

## Design

- `panel`: serves the administrator UI and stores site metadata in `data/panel/sites.json`; it has no Docker socket.
- `worker`: accepts requests only from the private control network and operates Docker using a secret token.
- `caddy`: serves the panel and site domains. Public sites use Caddy's automatic HTTPS; `*.localhost` sites use HTTP.
- Each site is a separate Compose project under `data/sites/<id>` with named WordPress and database volumes. No site container exposes its own host port or joins another site's database network.
- Backups are built by the private worker, verified before restore, and stored outside the site volumes. Restore currently targets the same site only.

The installer, panel, worker, and Caddy are Apache-2.0 licensed. WordPress, MariaDB, Caddy, and their container images retain their own upstream licenses.

## Development

```sh
go test ./...
docker compose config --quiet
```

Do not commit `.env` or `data/`; both are ignored by Git and excluded from Docker build contexts.
