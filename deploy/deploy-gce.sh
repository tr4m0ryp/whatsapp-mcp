#!/usr/bin/env bash
# Reproducible Compute Engine deploy for whatsapp_mcp, exposing the MCP server
# over public HTTPS with WorkOS AuthKit OAuth so claude.ai can connect.
#
# Unlike finding_house_mcp (stateless, Cloud Run), the WhatsApp bridge holds a
# live paired session and a local SQLite archive, so it needs a persistent,
# always-on host. This provisions a small e2-micro VM: the bridge and the MCP
# server run as systemd daemons on the same box (localhost + shared disk),
# fronted by Caddy for auto-TLS.
#
# Prereqs: gcloud authenticated as the deploying account.
set -euo pipefail

# ---- configure these ----
ACCOUNT="${ACCOUNT:-shorsinas@gmail.com}"           # the "shor" Google account
PROJECT_ID="${PROJECT_ID:-shor-x-sinas}"
REGION="${REGION:-europe-west1}"
ZONE="${ZONE:-europe-west1-b}"
VM="${VM:-whatsapp-mcp}"
MACHINE="${MACHINE:-e2-micro}"                       # ~$7/mo in europe-west1
IP_NAME="${IP_NAME:-whatsapp-mcp-ip}"
# Reuse the existing AuthKit tenant (shared across the sibling MCP servers).
AUTHKIT_DOMAIN="${AUTHKIT_DOMAIN:-https://meticulous-pumpkin-34-staging.authkit.app}"
# --------------------------

gcloud config set account "$ACCOUNT"
gcloud config set project "$PROJECT_ID"

echo "== APIs =="
gcloud services enable compute.googleapis.com

echo "== static IP =="
gcloud compute addresses create "$IP_NAME" --region "$REGION" 2>/dev/null || true
IP=$(gcloud compute addresses describe "$IP_NAME" --region "$REGION" --format='value(address)')
HOST="${IP//./-}.sslip.io"     # 34-1-2-3.sslip.io -> resolves to the static IP
echo "Static IP: $IP   Public host: $HOST"

echo "== firewall (80/443 in; bridge :8080 stays loopback-only) =="
gcloud compute firewall-rules create "${VM}-web" \
  --allow tcp:80,tcp:443 --direction INGRESS --target-tags "$VM" 2>/dev/null || true

echo "== VM =="
# Startup script: 2G swap (so the Go build fits 1GB RAM), then run provision.sh
# from the cloned repo with the resolved public host.
STARTUP=$(cat <<EOF
#!/usr/bin/env bash
set -e
if [ ! -f /swapfile ]; then
  fallocate -l 2G /swapfile && chmod 600 /swapfile && mkswap /swapfile && swapon /swapfile
  echo '/swapfile none swap sw 0 0' >> /etc/fstab
fi
apt-get update -y && apt-get install -y git
rm -rf /opt/whatsapp_mcp
git clone https://github.com/tr4m0ryp/whatsapp-mcp.git /opt/whatsapp_mcp
bash /opt/whatsapp_mcp/deploy/provision.sh "$HOST" "$AUTHKIT_DOMAIN" \
  >/var/log/whatsapp-provision.log 2>&1
EOF
)

gcloud compute instances create "$VM" \
  --zone "$ZONE" --machine-type "$MACHINE" \
  --image-family debian-12 --image-project debian-cloud \
  --boot-disk-size 20GB --tags "$VM" \
  --address "$IP" \
  --metadata startup-script="$STARTUP"

echo
echo "VM created. Provisioning runs in the background (~5-10 min: Go build + venv)."
echo "Watch it:   gcloud compute ssh $VM --zone $ZONE --command 'sudo tail -f /var/log/whatsapp-provision.log'"
echo
echo "Then pair the bridge (scan its QR once), and add to claude.ai:"
echo "  Settings -> Connectors -> Add custom connector"
echo "  URL: https://${HOST}/mcp   (leave OAuth fields blank; claude.ai discovers AuthKit)"
