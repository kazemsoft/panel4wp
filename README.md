# WP Host Panel

An early, self hosted, open source WordPress server panel. A single server administrator can create, start, stop, and remove independent WordPress sites from a browser. Each site gets its own WordPress and MariaDB containers, Docker volumes, credentials, and domain route. Caddy handles HTTPS for public domains.

If panel4wp is useful to you, please [star the project on GitHub](https://github.com/kazemsoft/panel4wp). Stars help other people discover the project. You can also support its development with a cryptocurrency donation:

<a href="https://nowpayments.io/donation?api_key=NB8S1VE-JHAM0P7-GT27HGT-43Q3CNA" target="_blank" rel="noreferrer noopener">
  <img src="https://nowpayments.io/images/embeds/donation-button-black.svg" alt="Donate cryptocurrency to panel4wp with NOWPayments">
</a>

**Status: experimental MVP.** Do not use it for paying customers or irreplaceable data yet. SFTP, automated alerting, container image upgrades, hard storage quotas, off-host backups, and customer accounts are planned but not implemented. The current worker has access to the Docker socket and must be treated as a privileged part of the host.

## Quick installation on Linux or a VM

Use a dedicated Linux machine or VM. Ubuntu 22.04/24.04 or Debian 12 is a simple starting point. The host needs:

- Linux server with Docker Engine and the Compose plugin
- Git, ports 80 and 443 available, and enough RAM and disk for the sites you create
- For a public panel or site, an A/AAAA DNS record pointing to the server and inbound ports 80/443

Docker Desktop on macOS may be used for development, but the installer itself requires Linux.

### 1. Prepare the server

Connect to the machine over SSH. On a fresh Ubuntu or Debian host, install Git and Docker:

```sh
sudo apt update
sudo apt install -y ca-certificates curl git
curl -fsSL https://get.docker.com -o /tmp/get-docker.sh
sudo sh /tmp/get-docker.sh
sudo usermod -aG docker "$USER"
```

Log out and reconnect after changing the Docker group. For a long-lived production server, use Docker's [distribution-specific installation instructions](https://docs.docker.com/engine/install/) instead of the convenience script.

Verify that Docker and Compose are available:

```sh
docker --version
docker compose version
```

If the server uses UFW, keep SSH accessible and allow web traffic:

```sh
sudo ufw allow OpenSSH
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw enable
```

Also open TCP ports 80 and 443 in the VM provider's firewall or security group, when it has one.

### 2. Configure a public domain

Create an A record such as `panel.example.com` pointing to the server's public IPv4 address. Add an AAAA record only when IPv6 is configured on the server. Wait for the record to resolve before installing so Caddy can obtain the HTTPS certificate.

Each WordPress site also needs its own A/AAAA record pointing to this server. The panel configures its route when the site is created.

### 3. Install panel4wp

Clone the repository and run the installer from its directory:

```sh
git clone https://github.com/kazemsoft/panel4wp.git
cd panel4wp
./install.sh panel.example.com
```

Replace `panel.example.com` with the panel domain you configured. The installer:

- creates the local data directories;
- generates private panel and worker secrets;
- builds and starts the panel, worker, and Caddy containers;
- prints the `admin` password once.

Save the generated password immediately. Open `https://panel.example.com` and sign in as `admin`.

### Local installation

To try panel4wp directly on a Linux computer without a public domain, run:

```sh
git clone https://github.com/kazemsoft/panel4wp.git
cd panel4wp
./install.sh
```

Open `http://localhost`. When creating a test site, leave its domain empty to receive an `http://<id>.localhost` address. These `.localhost` addresses are intended for the same computer that runs the panel.

### Update an installation

From the cloned repository directory:

```sh
git pull --ff-only
docker compose up -d --build
```

The persistent panel, site, backup, and Caddy data stays under `data/`. Back up this directory before server migration or major updates. Never commit `.env` or `data/`.

## Operation

Log in with the password printed by the installer. The dashboard contains server totals and one summary card per site. Creating a site, server settings, activity history, and each site's management areas use separate guided pages. Create a site using its domain, title, WordPress administrator email, and a built-in plan, saved named plan, or custom CPU/RAM values. The selected memory and CPU limits apply independently to its WordPress and MariaDB containers. The panel shows the WordPress `admin` password once after creation. Save it immediately. Site creation downloads the WordPress and MariaDB images and can take several minutes on the first run. Visit `/wp-admin` on the site's domain to manage WordPress content.

The panel supports Start, Stop, Retry after failed creation, verified Backup, in-place Restore, individual backup deletion, and permanent site deletion. A backup contains a consistent MariaDB dump, the complete WordPress volume, a manifest, and SHA-256 checksums. Restore first creates and retains a safety backup, then verifies the selected backup before replacing data. Local backups live under `data/backups/<site-id>`.

Each site can run verified backups automatically every 6 hours, 12 hours, day, or week. Retention keeps between 1 and 30 scheduled backups and never removes manual or safety backups. A stopped site is postponed until its next interval, and scheduler results are written to the audit log. Scheduled backups are currently local; off-host copies and encryption remain planned.

Each site's **Resources** page can change CPU/RAM limits or select custom values. Changes briefly recreate running service containers with their existing data; stopped sites remain stopped. Capacity is checked before creation, resource changes and Start, counting both services, stopped-site reservations, a host reserve and other running Docker workloads. New-site and Resources pages load capacity snapshots, including Docker stats for other containers without limits. See [resource and capacity guidance](docs/RESOURCES.md) for accounting, recovery and the remaining hard disk quota work.

The Sites page can request a live resource snapshot for every running site. It displays CPU, memory, network I/O, block I/O, and process counts separately for WordPress and MariaDB. Site usage metrics are loaded only when requested.

The same refresh measures the real disk space occupied by each site's WordPress volume, MariaDB volume, and local backups. Hard filesystem quotas are not enforced yet.

Resources also checks the site's backing filesystems and actual kernel support for project-quota enforcement. This read-only result is shown separately from an assigned quota; activation remains in development. A reproducible [isolated Linux VM drill](tests/quota-vm/README.md) tests the proposed storage backend without changing host volumes. See [hard disk quota progress and acceptance gates](docs/RESOURCES.md#hard-disk-quotas).

The **Back up and update WordPress** action first creates and records a verified safety backup. It then updates WordPress core, runs database migrations, and updates all plugins and themes through the site's isolated WP-CLI service. The safety backup remains available for an in-place restore if an extension update causes a regression.

Each running site has a browser file manager restricted to its `wp-content` directory. It can browse directories, upload and download files up to 10 MB, create directories, move entries, delete files, and remove empty directories. Rows can be selected from the table or opened through a context menu. It refuses unsafe relative paths and symbolic links, runs as the WordPress user, and constrains PHP filesystem access. Writes use a temporary file and atomic replacement. Directory listings are limited to 5,000 entries.

Each running site also has an on-demand phpMyAdmin database manager. It uses the site's restricted WordPress database account, is reachable only through the authenticated panel, publishes no host port, and is automatically removed after 15 minutes. Opening it again starts a fresh 15-minute session. The proxy removes panel session cookies before forwarding requests to phpMyAdmin.

The Settings section stores one global SMTP connection encrypted with AES-GCM using the panel session secret. The administrator can enable or disable it independently for each running site. Enabled sites receive a protected WordPress must-use plugin that configures PHPMailer and the sender identity. SMTP passwords are never rendered back into the browser.

Recent administrator operations are written to a private, size-limited JSON Lines audit log and shown in the Activity section.

The panel is available in English, Arabic, Persian, Spanish, German, French, Chinese, and Japanese. On the first visit it follows the browser's preferred supported language and falls back to English. The language selector is visible on both the login screen and the authenticated sidebar; an explicit choice is saved in a one-year cookie. Arabic and Persian render right-to-left.

Deleting a site removes its containers, Docker volumes, local backups, and all credentials. Enter the exact domain to confirm. Copy important backups to separate storage because local backups are lost with the server or disk.

## Design

- `panel`: serves the administrator UI and stores site metadata in `data/panel/sites.json`; it has no Docker socket.
- `worker`: accepts requests only from the private control network and operates Docker using a secret token.
- `caddy`: serves the panel and site domains. Public sites use Caddy's automatic HTTPS; `*.localhost` sites use HTTP.
- Each site is a separate Compose project under `data/sites/<id>` with named WordPress and database volumes. WordPress frontends join a shared proxy network that contains Caddy; every database remains on its site's private internal network. No site container exposes a host port or joins another site's database network.
- Backups are built by the private worker, verified before restore, and stored outside the site volumes. Restore currently targets the same site only.
- File operations are executed by the private worker inside the selected WordPress container and are constrained to the real `wp-content` path.
- phpMyAdmin joins only the selected site's private database network and a private tools proxy network shared with the panel. It is disabled by default and starts through a Compose profile on demand.

The installer, panel, worker, and Caddy are Apache-2.0 licensed. WordPress, MariaDB, Caddy, and their container images retain their own upstream licenses.

Long site operations persist a receipt before starting. The panel checks actual WordPress/database container state on startup and every 30 seconds, with a manual **Refresh service status** action. Unconfirmed requests remain blocked until the worker receipt is checked. Interrupted creation offers a retry with a new administrator password; interrupted restore/update requires inspecting the site and using the retained safety backup when necessary. Failed restores keep WordPress stopped. Worker startup closes previous phpMyAdmin sessions. See [restart recovery](docs/RECOVERY.md).

Successful long-running operations use Post/Redirect/Get with one-time in-memory result messages. This prevents Caddy route updates from interrupting the administrator response and avoids putting generated WordPress passwords in URLs or persistent metadata.

## Frontend

The panel uses type-safe `templ` components rendered by Go, self-hosted HTMX for progressive enhancement, and Tailwind CSS 4 CLI. Compiled CSS, JavaScript, inline SVG icons, and the OFL-licensed Vazirmatn font for Persian and Arabic are embedded into the panel binary; the runtime container contains no Node.js and the UI does not depend on a CDN. The authenticated sidebar can be collapsed and stores its display preference in local browser storage. Translation dictionaries live under `internal/i18n`, and the complete UI is shared across all languages.

## Development

```sh
npm ci
npm run build
go generate ./cmd/panel
go test ./...
docker compose config --quiet
```

Do not commit `.env` or `data/`; both are ignored by Git and excluded from Docker build contexts.
