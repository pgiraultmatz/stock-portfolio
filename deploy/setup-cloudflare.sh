#!/bin/bash
# Cloudflare Tunnel setup — run once on EC2 after the app is running
# Prérequis : domaine géré par Cloudflare
set -euo pipefail

# ---- install cloudflared (ARM64) ----
curl -L --output cloudflared.rpm \
  https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-aarch64.rpm
sudo rpm -ivh cloudflared.rpm
rm cloudflared.rpm

# ---- authenticate (ouvre un lien dans le terminal) ----
cloudflared tunnel login

# ---- create tunnel ----
cloudflared tunnel create stock-portfolio

# La commande ci-dessus affiche un TUNNEL_ID — notez-le.
# Elle crée aussi ~/.cloudflared/<TUNNEL_ID>.json

# ---- config ----
TUNNEL_ID=$(cloudflared tunnel list | grep stock-portfolio | awk '{print $1}')
DOMAIN="portfolio.votredomaine.com"   # <-- à remplacer

mkdir -p ~/.cloudflared
cat > ~/.cloudflared/config.yml <<EOF
tunnel: ${TUNNEL_ID}
credentials-file: /home/ec2-user/.cloudflared/${TUNNEL_ID}.json

ingress:
  - hostname: ${DOMAIN}
    service: http://localhost:8080
  - service: http_status:404
EOF

# ---- DNS (crée l'entrée CNAME chez Cloudflare automatiquement) ----
cloudflared tunnel route dns stock-portfolio "${DOMAIN}"

# ---- install as systemd service ----
sudo cloudflared service install
sudo systemctl enable cloudflared
sudo systemctl start cloudflared

echo ""
echo "Tunnel actif. Teste : curl https://${DOMAIN}/auth/login"
