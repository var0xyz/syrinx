#!/bin/bash
# ==============================================================================
# Set SIGNUP_MODE (open | invite | closed) in the app's env file and restart
# the service. Requires setup.env from a prior ./setup.sh run.
#
# Usage:
#   sudo ./set-signup-mode.sh open
#   sudo ./set-signup-mode.sh invite
#   sudo ./set-signup-mode.sh closed
# ==============================================================================

set -euo pipefail

if [ "$EUID" -ne 0 ]; then
    echo "❌ Error: run as root (sudo ./set-signup-mode.sh <mode>)"
    exit 1
fi

MODE="${1:-}"
case "$MODE" in
    open|invite|closed) ;;
    *)
        echo "❌ Error: invalid mode '${MODE}'. Must be one of: open, invite, closed" >&2
        echo "Usage: sudo ./set-signup-mode.sh <open|invite|closed>" >&2
        exit 1
        ;;
esac

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

ENV_FILE="/etc/$APP_NAME/app.env"
if [ ! -f "$ENV_FILE" ]; then
    echo "❌ Error: missing $ENV_FILE — run setup first."
    exit 1
fi

if grep -q "^SIGNUP_MODE=" "$ENV_FILE"; then
    sed -i "s|^SIGNUP_MODE=.*|SIGNUP_MODE=${MODE}|" "$ENV_FILE"
else
    echo "SIGNUP_MODE=${MODE}" >> "$ENV_FILE"
fi
echo "✅ SIGNUP_MODE=${MODE} written to $ENV_FILE"

echo "🔄 Restarting $APP_NAME.service..."
systemctl restart "$APP_NAME.service"
sleep 1

if systemctl is-active --quiet "$APP_NAME.service"; then
    echo "✅ $APP_NAME.service: ONLINE (signup mode: ${MODE})"
else
    echo "❌ $APP_NAME.service failed to restart" >&2
    journalctl -u "$APP_NAME.service" -n 30 --no-pager >&2 || true
    exit 1
fi
