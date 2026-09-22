#!/bin/sh
set -eu

if [ "$(uname -s)" != Linux ]; then
  echo 'This installer currently supports Linux only.' >&2
  exit 1
fi
if ! command -v docker >/dev/null 2>&1 || ! docker compose version >/dev/null 2>&1; then
  echo 'Docker Engine and the Docker Compose plugin are required.' >&2
  exit 1
fi

cd "$(dirname "$0")"
root_dir=$(pwd -P)
if [ -f .env ]; then
  echo 'Existing .env found. Use docker compose up -d --build to update.' >&2
  exit 1
fi

panel_domain=${1:-http://localhost}
case "$panel_domain" in
  http://localhost|http://127.0.0.1|https://localhost|https://127.0.0.1) ;;
  http://*|https://*|*://*|*[!a-zA-Z0-9.-]*|'') echo 'Invalid panel domain. Use a hostname such as panel.example.com or http://localhost.' >&2; exit 1 ;;
esac
mkdir -p data/panel data/sites data/caddy
chmod 700 data data/panel data/sites data/caddy

random_hex() { od -An -N32 -tx1 /dev/urandom | tr -d ' \n'; }
admin_password=$(random_hex)
session_key=$(random_hex)
worker_token=$(random_hex)

umask 077
cat > .env <<EOF
PANEL_ADMIN_HASH=temporary
PANEL_SESSION_KEY=$session_key
WORKER_TOKEN=$worker_token
PANEL_DOMAIN=$panel_domain
WPH_DATA_DIR=$root_dir/data
EOF

docker compose build panel worker
admin_hash=$(printf %s "$admin_password" | docker compose run --rm -T --no-deps panel hash-password)
cat > .env <<EOF
PANEL_ADMIN_HASH='$admin_hash'
PANEL_SESSION_KEY=$session_key
WORKER_TOKEN=$worker_token
PANEL_DOMAIN=$panel_domain
WPH_DATA_DIR=$root_dir/data
EOF
docker compose up -d

echo "Panel URL: $panel_domain"
echo 'Username: admin'
echo "Password (save it now): $admin_password"
