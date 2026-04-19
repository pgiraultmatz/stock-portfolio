#!/bin/bash
# Deploy to EC2 — run from the project root
set -euo pipefail

SSH_KEY="${SSH_KEY:-~/.ssh/stock-portfolio}"
REMOTE_DIR="/opt/stock-portfolio"
BINARY="stock-portfolio-linux"

echo "==> Fetching EC2 host from Terraform..."
EC2_DNS=$(cd terraform && terraform output -raw ec2_public_dns)
EC2_IP=$(cd terraform && terraform output -raw ec2_public_ip)
EC2_HOST="ubuntu@${EC2_DNS}"

echo "==> Generating .env.prod from .env..."
grep -v "DYNAMODB_ENDPOINT\|AWS_ACCESS_KEY_ID\|AWS_SECRET_ACCESS_KEY" .env \
  | sed "s|APP_BASE_URL=.*|APP_BASE_URL=http://${EC2_IP}:8080|" \
  > .env.prod

echo "==> Copying .env.prod to EC2..."
scp -i "${SSH_KEY}" -o StrictHostKeyChecking=accept-new .env.prod "${EC2_HOST}:~/.env.prod"
ssh -i "${SSH_KEY}" "${EC2_HOST}" "sudo mv ~/.env.prod /opt/stock-portfolio/.env && sudo chown stock-portfolio:stock-portfolio /opt/stock-portfolio/.env && sudo chmod 600 /opt/stock-portfolio/.env"

echo "==> Building for linux/arm64..."
GOOS=linux GOARCH=arm64 go build -o "${BINARY}" .

echo "==> Copying binary to ${EC2_HOST}..."
scp -i "${SSH_KEY}" -o StrictHostKeyChecking=accept-new "${BINARY}" "${EC2_HOST}:~/stock-portfolio"
ssh -i "${SSH_KEY}" "${EC2_HOST}" "sudo mv ~/stock-portfolio ${REMOTE_DIR}/stock-portfolio && sudo chown stock-portfolio:stock-portfolio ${REMOTE_DIR}/stock-portfolio && sudo chmod +x ${REMOTE_DIR}/stock-portfolio"

echo "==> Restarting service..."
ssh -i "${SSH_KEY}" "${EC2_HOST}" "sudo systemctl restart stock-portfolio"

echo ""
echo "==> App disponible sur : http://${EC2_IP}:8080"
echo ""
echo "==> Logs (Ctrl+C pour quitter) :"
ssh -i "${SSH_KEY}" "${EC2_HOST}" "sudo journalctl -fu stock-portfolio"
