#!/bin/bash
# ==============================================================================
# Wipe app database: backup, DROP + recreate empty DB (same owner / grants as setup).
# Requires setup.env from a prior ./setup.sh run.
# ==============================================================================

set -euo pipefail

if [ "$EUID" -ne 0 ]; then
    echo "❌ Error: run as root (sudo ./wipe-db.sh)"
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

APP_USER="$APP_NAME"
ENV_FILE="/etc/$APP_NAME/app.env"
# Prefer the name the running app already uses; fall back to APP_NAME (no _db suffix).
if [ -f "$ENV_FILE" ] && grep -q '^DB_NAME=.\+' "$ENV_FILE"; then
    DB_NAME="$(grep '^DB_NAME=' "$ENV_FILE" | head -n1 | cut -d= -f2-)"
else
    DB_NAME="$APP_NAME"
fi
BACKUP_DIR="/var/backups/$APP_NAME/db"

echo "================================================================"
echo "⚠️  WIPE DATABASE — all data in $DB_NAME will be permanently deleted"
echo "================================================================"
echo "App:      $APP_NAME"
echo "Database: $DB_NAME"
echo "Owner:    $APP_USER"
echo "Backup:   $BACKUP_DIR (created before wipe if DB exists)"
echo ""
read -r -p "Type the database name ($DB_NAME) to confirm wipe: " CONFIRM
if [ "$CONFIRM" != "$DB_NAME" ]; then
    echo "❌ Confirmation mismatch — aborting. No changes made."
    exit 1
fi

echo -e "\n🛑 Stopping $APP_NAME to release DB connections..."
systemctl stop "$APP_NAME.service" 2>/dev/null || true

BACKUP_FILE=""
if sudo -i -u postgres psql -lqt | cut -d \| -f 1 | grep -qw "$DB_NAME"; then
    mkdir -p "$BACKUP_DIR"
    chmod 700 "$BACKUP_DIR"
    BACKUP_FILE="$BACKUP_DIR/${DB_NAME}-$(date +%Y%m%d-%H%M%S).sql.gz"
    echo -e "\n💾 Backing up $DB_NAME to $BACKUP_FILE..."
    # Pre-create with restrictive perms so the dump is never briefly
    # world-readable between the redirect creating the file and chmod.
    ( umask 077 && : > "$BACKUP_FILE" )
    sudo -i -u postgres pg_dump "$DB_NAME" | gzip > "$BACKUP_FILE"
    chmod 600 "$BACKUP_FILE"
    if [ ! -s "$BACKUP_FILE" ]; then
        echo "❌ Error: backup file is empty — aborting. Database was not modified."
        rm -f "$BACKUP_FILE"
        systemctl start "$APP_NAME.service" 2>/dev/null || true
        exit 1
    fi
    echo "✅ Backup saved"
else
    echo "⚠️  Database $DB_NAME does not exist — skipping backup, creating empty instance..."
fi

if [ -n "$BACKUP_FILE" ]; then
    echo -e "\n🗑️  Dropping $DB_NAME..."
    # WITH (FORCE) terminates remaining sessions (PostgreSQL 13+)
    sudo -i -u postgres psql -c "DROP DATABASE ${DB_NAME} WITH (FORCE);"
fi

echo "🆕 Creating empty $DB_NAME (owner: $APP_USER)..."
sudo -i -u postgres psql -c "CREATE DATABASE ${DB_NAME} OWNER ${APP_USER};"
sudo -i -u postgres psql -d "$DB_NAME" -c \
    "REVOKE CREATE ON SCHEMA public FROM PUBLIC; GRANT CREATE ON SCHEMA public TO ${APP_USER};"

echo "✅ Empty database ready — run ./setup.sh to migrate and restart $APP_NAME"

if [ -n "$BACKUP_FILE" ]; then
    echo "📦 Backup: $BACKUP_FILE"
    echo "↩️  Restore: gunzip -c $BACKUP_FILE | sudo -u postgres psql $DB_NAME"
fi
echo "🚀 Database wipe complete."
