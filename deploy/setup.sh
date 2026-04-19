#!/bin/bash
# Initial EC2 setup — run once from your Mac (project root)
set -euo pipefail

SSH_KEY="${SSH_KEY:-~/.ssh/stock-portfolio}"

echo "==> Fetching EC2 host from Terraform..."
EC2_DNS=$(cd terraform && terraform output -raw ec2_public_dns)
EC2_HOST="ubuntu@${EC2_DNS}"

echo "==> Copying service file..."
scp -i "${SSH_KEY}" -o StrictHostKeyChecking=accept-new deploy/stock-portfolio.service "${EC2_HOST}:~"

echo "==> Configuring EC2..."
ssh -i "${SSH_KEY}" -o StrictHostKeyChecking=accept-new "${EC2_HOST}" bash <<'EOF'
  sudo useradd -r -s /sbin/nologin stock-portfolio
  sudo mkdir -p /opt/stock-portfolio
  sudo chown stock-portfolio:stock-portfolio /opt/stock-portfolio
  sudo cp stock-portfolio.service /etc/systemd/system/
  sudo systemctl daemon-reload
  sudo systemctl enable stock-portfolio
  echo "Setup done."
EOF

echo ""
echo "==> Maintenant copie le .env de prod :"
echo "    scp -i ${SSH_KEY} .env.prod ${EC2_HOST}:/opt/stock-portfolio/.env"
echo "    ssh -i ${SSH_KEY} ${EC2_HOST} 'sudo chown stock-portfolio:stock-portfolio /opt/stock-portfolio/.env && sudo chmod 600 /opt/stock-portfolio/.env'"
