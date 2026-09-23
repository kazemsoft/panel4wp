# WP Host Panel

An early, self hosted, open source WordPress server panel. A single server administrator can create, start, stop, and remove independent WordPress sites from a browser. Each site gets its own WordPress and MariaDB containers, Docker volumes, credentials, and domain route. Caddy handles HTTPS for public domains.

**Status: experimental MVP.** Do not use it for paying customers or irreplaceable data yet. SFTP, automated alerting, container image upgrades, hard storage quotas, off-host backups, and customer accounts are planned but not implemented. The current worker has access to the Docker socket and must be treated as a privileged part of the host.

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

Log in with the password printed by the installer. The dashboard contains server totals and one summary card per site. Creating a site, server settings, activity history, and each site's management areas use separate guided pages. Create a site using its domain, title, WordPress administrator email, and a Small, Standard, or Large resource plan. The selected memory and CPU limits apply independently to its WordPress and MariaDB containers. The panel shows the WordPress `admin` password once after creation. Save it immediately. Site creation downloads the WordPress and MariaDB images and can take several minutes on the first run. Visit `/wp-admin` on the site's domain to manage WordPress content.

The panel supports Start, Stop, Retry after failed creation, verified Backup, in-place Restore, individual backup deletion, and permanent site deletion. A backup contains a consistent MariaDB dump, the complete WordPress volume, a manifest, and SHA-256 checksums. Restore first creates and retains a safety backup, then verifies the selected backup before replacing data. Local backups live under `data/backups/<site-id>`.

The Sites page can request a live resource snapshot for every running site. It displays CPU, memory, network I/O, block I/O, and process counts separately for WordPress and MariaDB. Metrics are loaded only when requested so routine panel navigation does not run Docker stats.

The same refresh measures the real disk space occupied by each site's WordPress volume, MariaDB volume, and local backups. Hard filesystem quotas are not enforced yet.

The **Back up and update WordPress** action first creates and records a verified safety backup. It then updates WordPress core, runs database migrations, and updates all plugins and themes through the site's isolated WP-CLI service. The safety backup remains available for an in-place restore if an extension update causes a regression.

Each running site has a browser file manager restricted to its `wp-content` directory. It can browse directories, upload and download files up to 10 MB, create directories, delete files, and remove empty directories. It refuses unsafe relative paths and does not allow operations on symbolic links.

Each running site also has an on-demand phpMyAdmin database manager. It uses the site's restricted WordPress database account, is reachable only through the authenticated panel, publishes no host port, and is automatically removed after 15 minutes. Opening it again starts a fresh 15-minute session. The proxy removes panel session cookies before forwarding requests to phpMyAdmin.

The Settings section stores one global SMTP connection encrypted with AES-GCM using the panel session secret. The administrator can enable or disable it independently for each running site. Enabled sites receive a protected WordPress must-use plugin that configures PHPMailer and the sender identity. SMTP passwords are never rendered back into the browser.

Recent administrator operations are written to a private, size-limited JSON Lines audit log and shown in the Activity section.

Deleting a site removes its containers, Docker volumes, local backups, and all credentials. Enter the exact domain to confirm. Copy important backups to separate storage because local backups are lost with the server or disk.

Update the panel from its repository directory with:

```sh
docker compose up -d --build
```

## Design

- `panel`: serves the administrator UI and stores site metadata in `data/panel/sites.json`; it has no Docker socket.
- `worker`: accepts requests only from the private control network and operates Docker using a secret token.
- `caddy`: serves the panel and site domains. Public sites use Caddy's automatic HTTPS; `*.localhost` sites use HTTP.
- Each site is a separate Compose project under `data/sites/<id>` with named WordPress and database volumes. WordPress frontends join a shared proxy network that contains Caddy; every database remains on its site's private internal network. No site container exposes a host port or joins another site's database network.
- Backups are built by the private worker, verified before restore, and stored outside the site volumes. Restore currently targets the same site only.
- File operations are executed by the private worker inside the selected WordPress container and are constrained to the real `wp-content` path.
- phpMyAdmin joins only the selected site's private database network and a private tools proxy network shared with the panel. It is disabled by default and starts through a Compose profile on demand.

The installer, panel, worker, and Caddy are Apache-2.0 licensed. WordPress, MariaDB, Caddy, and their container images retain their own upstream licenses.

Successful long-running operations use Post/Redirect/Get with one-time in-memory result messages. This prevents Caddy route updates from interrupting the administrator response and avoids putting generated WordPress passwords in URLs or persistent metadata.

## Development

```sh
go test ./...
docker compose config --quiet
```

Do not commit `.env` or `data/`; both are ignored by Git and excluded from Docker build contexts.
