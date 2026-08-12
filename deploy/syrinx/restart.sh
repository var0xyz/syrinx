#!/bin/bash
# ==============================================================================
# App-only restart: reload the systemd service without rebuilding or redeploying.
# Requires setup.env from a prior ./setup.sh run.
# ==============================================================================

set -euo pipefail

if [ "$EUID" -ne 0 ]; then
    echo "❌ Error: run as root (sudo ./restart.sh)"
    exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SETUP_ENV="$SCRIPT_DIR/setup.env"

if [ ! -f "$SETUP_ENV" ]; then
    echo "❌ Error: missing $SETUP_ENV — run setup first."
    exit 1
fi

# shellcheck disable=SC1090
set -a
# shellcheck source=/dev/null
. "$SETUP_ENV"
set +a

APP_NAME=$(echo "${APP_NAME:-}" | tr -d ' ' | tr 'A-Z' 'a-z')

if [ -z "$APP_NAME" ]; then
    echo "❌ Error: APP_NAME must be set in $SETUP_ENV"
    exit 1
fi

echo "================================================================"
echo "🔁 Restarting $APP_NAME"
echo "================================================================"

systemctl restart "$APP_NAME.service"

sleep 1
if systemctl is-active --quiet "$APP_NAME"; then
    echo "✅ $APP_NAME is ONLINE"
else
    echo "❌ $APP_NAME failed to start — check: journalctl -u $APP_NAME -n 50 --no-pager"
    systemctl status "$APP_NAME.service" --no-pager || true
    exit 1
fi

echo "🚀 Restart complete."
