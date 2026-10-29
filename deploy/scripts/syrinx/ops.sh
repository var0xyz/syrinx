#!/bin/bash
# ==============================================================================
# Run the operator CLI ($APP_NAME-ops) with the app env file loaded — the
# binary only reads its config from the environment.
# Requires setup.env from a prior ./setup.sh run. Args are passed to ops.
#
# Usage:
#   sudo ./ops.sh check-identity-backup
#   sudo ./ops.sh export-identity <file>
#   sudo ./ops.sh rotate-key "reason" [--compromised]
# ==============================================================================

set -euo pipefail

if [ "$EUID" -ne 0 ]; then
    echo "❌ Error: run as root (sudo $0 $*)" >&2
    exit 1
fi

# Resolve the ~/syrinx/ops symlink back to the scripts directory.
SCRIPT_DIR="$(dirname "$(readlink -f "$0")")"
SETUP_ENV="$(dirname "$SCRIPT_DIR")/setup.env"

if [ ! -f "$SETUP_ENV" ]; then
    echo "❌ Error: missing $SETUP_ENV — run setup first." >&2
    exit 1
fi

# shellcheck disable=SC1090
set -a
# shellcheck source=/dev/null
. "$SETUP_ENV"
set +a

APP_NAME=$(echo "${APP_NAME:-}" | tr -d ' ' | tr 'A-Z' 'a-z')
if [ -z "$APP_NAME" ]; then
    echo "❌ Error: APP_NAME must be set in $SETUP_ENV" >&2
    exit 1
fi

ENV_FILE="/etc/$APP_NAME/app.env"
OPS="/usr/local/bin/$APP_NAME-ops"
[ -r "$ENV_FILE" ] || { echo "❌ Error: cannot read $ENV_FILE" >&2; exit 1; }
[ -x "$OPS" ] || { echo "❌ Error: missing $OPS — run setup first." >&2; exit 1; }

# shellcheck disable=SC1090
set -a
# shellcheck source=/dev/null
. "$ENV_FILE"
set +a

exec "$OPS" "$@"
