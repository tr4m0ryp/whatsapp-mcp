#!/usr/bin/env bash
# Runs ON the GCE VM (as root, Debian 12) to install and start both processes.
#
#   provision.sh <PUBLIC_HOST> <AUTHKIT_DOMAIN>
#
# <PUBLIC_HOST>      e.g. 34-1-2-3.sslip.io  (resolves to the VM's static IP;
#                    Caddy provisions Let's Encrypt TLS for it)
# <AUTHKIT_DOMAIN>   the shared WorkOS AuthKit tenant, e.g.
#                    https://meticulous-pumpkin-34-staging.authkit.app
#
# Idempotent enough to re-run: it re-clones/pulls, rebuilds, and restarts.
set -euo pipefail

PUBLIC_HOST="${1:?usage: provision.sh <PUBLIC_HOST> <AUTHKIT_DOMAIN>}"
AUTHKIT_DOMAIN="${2:?usage: provision.sh <PUBLIC_HOST> <AUTHKIT_DOMAIN>}"

REPO="https://github.com/tr4m0ryp/whatsapp-mcp.git"
APP_DIR="/opt/whatsapp_mcp"
GO_VERSION="1.26.2"
export DEBIAN_FRONTEND=noninteractive

echo "== packages =="
apt-get update -y
apt-get install -y git curl ca-certificates build-essential debian-keyring \
  debian-archive-keyring apt-transport-https ffmpeg

echo "== Go ${GO_VERSION} =="
if [ ! -x /usr/local/go/bin/go ] || ! /usr/local/go/bin/go version | grep -q "$GO_VERSION"; then
  curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" -o /tmp/go.tgz
  rm -rf /usr/local/go && tar -C /usr/local -xzf /tmp/go.tgz && rm /tmp/go.tgz
fi
export PATH=/usr/local/go/bin:$PATH

echo "== uv (system-wide) =="
curl -LsSf https://astral.sh/uv/install.sh | env UV_INSTALL_DIR=/usr/local/bin sh

echo "== Caddy =="
if ! command -v caddy >/dev/null; then
  curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' \
    | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
  curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' \
    > /etc/apt/sources.list.d/caddy-stable.list
  apt-get update -y && apt-get install -y caddy
fi

echo "== service user + code =="
id whatsapp >/dev/null 2>&1 || useradd --system --create-home --shell /usr/sbin/nologin whatsapp
if [ -d "$APP_DIR/.git" ]; then git -C "$APP_DIR" pull --ff-only; else git clone "$REPO" "$APP_DIR"; fi
chown -R whatsapp:whatsapp "$APP_DIR"

echo "== build bridge (single-threaded to fit 1GB + swap) =="
sudo -u whatsapp env PATH=/usr/local/go/bin:$PATH GOFLAGS=-p=1 CGO_ENABLED=1 \
  bash -c "cd $APP_DIR/bridge && go build -o whatsapp-bridge ."

echo "== sync server venv =="
sudo -u whatsapp env PATH=/usr/local/bin:$PATH \
  bash -c "cd $APP_DIR/server && uv sync"

echo "== MCP env file =="
cat >/etc/whatsapp-mcp.env <<EOF
MCP_TRANSPORT=http
MCP_HOST=127.0.0.1
MCP_PORT=8000
MCP_OAUTH_PROVIDER=authkit
WORKOS_AUTHKIT_DOMAIN=${AUTHKIT_DOMAIN}
MCP_BASE_URL=https://${PUBLIC_HOST}
WHATSAPP_API_URL=http://127.0.0.1:8080/api
WHATSAPP_DB_PATH=${APP_DIR}/bridge/store/messages.db
WHATSMEOW_DB_PATH=${APP_DIR}/bridge/store/whatsapp.db
EOF
chmod 640 /etc/whatsapp-mcp.env && chown root:whatsapp /etc/whatsapp-mcp.env

echo "== Caddy reverse proxy (auto-TLS for ${PUBLIC_HOST}) =="
cat >/etc/caddy/Caddyfile <<EOF
${PUBLIC_HOST} {
    reverse_proxy 127.0.0.1:8000
}
EOF

echo "== systemd units =="
install -m644 "$APP_DIR/deploy/systemd/whatsapp-bridge.service" /etc/systemd/system/
install -m644 "$APP_DIR/deploy/systemd/whatsapp-mcp.service" /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now whatsapp-bridge.service whatsapp-mcp.service
systemctl reload caddy || systemctl restart caddy

echo "== done =="
echo "Bridge + server up. Pair the bridge (scan its QR) if store/whatsapp.db is absent:"
echo "  journalctl -u whatsapp-bridge -n 60 --no-pager"
echo "Connector URL for claude.ai:  https://${PUBLIC_HOST}/mcp"
