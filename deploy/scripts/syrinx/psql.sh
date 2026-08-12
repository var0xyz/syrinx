#!/bin/bash
# ==============================================================================
# Open an interactive psql session using credentials from the app env file.
# Requires setup.env from a prior ./setup.sh run. Extra args are passed to psql.
#
# Usage:
#   sudo ./psql.sh
#   sudo ./psql.sh -c 'SELECT 1;'
#   sudo ./psql.sh -c '\dt'
# ==============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SETUP_ENV="$SCRIPT_DIR/setup.env"

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
if [ ! -r "$ENV_FILE" ]; then
    echo "❌ Error: cannot read $ENV_FILE — run as root: sudo $0 $*" >&2
    exit 1
fi

# shellcheck disable=SC1090
set -a
# shellcheck source=/dev/null
. "$ENV_FILE"
set +a

: "${DB_USER:?DB_USER missing in $ENV_FILE}"
: "${DB_NAME:?DB_NAME missing in $ENV_FILE}"
: "${DB_PASSWORD:?DB_PASSWORD missing in $ENV_FILE}"

if ! command -v psql >/dev/null 2>&1; then
    echo "❌ Error: psql not found — install postgresql-client" >&2
    exit 1
fi

exec env PGPASSWORD="$DB_PASSWORD" psql \
    -h "${DB_HOST:-127.0.0.1}" \
    -p "${DB_PORT:-5432}" \
    -U "$DB_USER" \
    -d "$DB_NAME" \
    "$@"
