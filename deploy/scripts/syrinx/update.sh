#!/bin/bash
# ==============================================================================
# App-only update: shallow-fetch latest monorepo, rebuild backend + SPA, restart
# Requires setup.env from a prior ./setup.sh run.
#
# Does NOT reconfigure Cloudflare — the tunnel is unrelated to app rebuilds.
# setup.sh skips Cloudflare too when https://$APP_DOMAIN already responds
# (use FORCE_CF=1 there to force a tunnel rewrite).
# ==============================================================================

set -euo pipefail

if [ "$EUID" -ne 0 ]; then
    echo "❌ Error: run as root (sudo ./update.sh)"
    exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SETUP_ENV="$SCRIPT_DIR/setup.env"
# shellcheck source=otel-agent.sh
. "$SCRIPT_DIR/otel-agent.sh"
# shellcheck source=root-bootstrap.sh
. "$SCRIPT_DIR/root-bootstrap.sh"

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
# Fixed to Syrinx's own repo layout (see setup.sh) — not user-configurable.
BACKEND_PATH="."
FRONTEND_PATH="spa"

if [ -z "$APP_NAME" ] || [ -z "${APP_REPO:-}" ]; then
    echo "❌ Error: APP_NAME and APP_REPO must be set in $SETUP_ENV"
    exit 1
fi

APP_USER="$APP_NAME"
WWW_ROOT="/var/www/html/$APP_NAME"
BUILD_DIR="/tmp/update-$APP_NAME-$$"
ENV_FILE="/etc/$APP_NAME/app.env"
TELEMETRY_HOST="${TELEMETRY_HOST:-}"
export PATH="/usr/local/go/bin:${PATH}"

# Persistent, timestamped log — survives even when the run fails partway, so
# a silent partial deploy (build succeeds, restart never happens) is no
# longer invisible after the fact. Keeps last 20 logs.
# Root-only (700/600): the log can capture secrets a sub-command prints
# (e.g. the root-bootstrap import passphrase), so it must not be world-readable.
LOG_DIR="/var/log/$APP_NAME"
mkdir -p "$LOG_DIR"
chmod 700 "$LOG_DIR"
LOG_FILE="$LOG_DIR/update-$(date +%Y%m%dT%H%M%S).log"
: > "$LOG_FILE"
chmod 600 "$LOG_FILE"
ln -sfn "$LOG_FILE" "$LOG_DIR/update-latest.log"
find "$LOG_DIR" -maxdepth 1 -name 'update-*.log' -printf '%T@ %p\n' 2>/dev/null \
    | sort -rn | tail -n +21 | cut -d' ' -f2- | xargs -r rm -f
exec > >(tee -a "$LOG_FILE") 2>&1
echo "📝 Logging this run to $LOG_FILE"

on_error() {
    echo "------------------------------------------------------------------"
    echo "❌ UPDATE FAILED at line $1 — the running service was NOT touched."
    echo "   Old binary/SPA/service are still exactly as they were before this run."
    echo "   Full log: $LOG_FILE"
    echo "------------------------------------------------------------------"
}
trap 'on_error $LINENO' ERR

ensure_env_kv() {
    local key="$1" value="$2"
    if grep -q "^${key}=" "$ENV_FILE" 2>/dev/null; then
        sed -i "s|^${key}=.*|${key}=${value}|" "$ENV_FILE"
    else
        echo "${key}=${value}" >> "$ENV_FILE"
    fi
}

remove_env_kv() {
    local key="$1"
    if [ -f "$ENV_FILE" ]; then
        sed -i "/^${key}=/d" "$ENV_FILE"
    fi
}

wire_observability_env() {
    if [ -n "$TELEMETRY_HOST" ]; then
        # Syrinx sends traces (gRPC) and app metrics (HTTP) to the local agent;
        # otelcol-agent forwards everything to the telemetry Pi.
        ensure_env_kv "OTEL_COLLECTOR_HOST" "127.0.0.1"
        ensure_env_kv "OTEL_COLLECTOR_PORT" "4317"
        otel_agent_install "$TELEMETRY_HOST" "$APP_NAME"
    else
        remove_env_kv "OTEL_COLLECTOR_HOST"
        remove_env_kv "OTEL_COLLECTOR_PORT"
        otel_agent_remove
    fi
}

cleanup() {
    rm -rf "$BUILD_DIR"
}
trap cleanup EXIT

echo "================================================================"
echo "🔄 Updating $APP_NAME from $APP_REPO"
echo "================================================================"

if ! command -v go >/dev/null 2>&1 && [ ! -x /usr/local/go/bin/go ]; then
    echo "❌ Error: Go toolchain not found. Run ./setup.sh first."
    exit 1
fi

echo -e "\n📦 Shallow-cloning latest code..."
mkdir -p "$BUILD_DIR"
git clone --depth 1 "$APP_REPO" "$BUILD_DIR/src"
GIT_COMMIT="$(git -C "$BUILD_DIR/src" rev-parse HEAD)"
export GIT_COMMIT
echo "    Commit: $GIT_COMMIT"

echo -e "\n💻 Building Go backend (staged — not installed yet)..."
cd "$BUILD_DIR/src/$BACKEND_PATH"
CGO_ENABLED=0 go build -ldflags="-w -s" -o "$BUILD_DIR/$APP_NAME" .

echo -e "\n⚛️  Building SPA (staged — not published yet)..."
cd "$BUILD_DIR/src/$FRONTEND_PATH"
npm install
# Best-effort only: update-browserslist-db has been flaky (npm "$baseline-browser-mapping"
# resolution bug) and is not required for a production SPA build.
npx --yes update-browserslist-db@latest \
    || echo "⚠️  update-browserslist-db failed — continuing with lockfile caniuse data"
npx svelte-kit sync
npm run build

# Both builds succeeded — everything from here on only touches the live
# system, so a build failure above can never leave a half-upgraded install:
# either both the binary and SPA ship together, or neither does.
echo -e "\n🚀 Both builds succeeded — installing atomically..."
install -o "$APP_USER" -g "$APP_USER" -m 500 "$BUILD_DIR/$APP_NAME" "/usr/local/bin/$APP_NAME"

# Ship the SPA into its own timestamped release dir and repoint the `build`
# symlink with `ln -sfn` (atomic rename, not an in-place rm+cp). A tab left
# open from before this deploy keeps resolving its already-loaded chunk
# hashes against the OLD release dir until it reloads on its own — no more
# "chunk deleted out from under a live tab" 404/MIME-type errors, and no
# window where nginx serves a half-old/half-new build.
RELEASES_DIR="$WWW_ROOT/releases"
RELEASE_DIR="$RELEASES_DIR/$(date +%Y%m%dT%H%M%S)-${GIT_COMMIT:0:12}"
mkdir -p "$RELEASES_DIR"
cp -r build "$RELEASE_DIR"
chown -R root:www-data "$RELEASE_DIR"
find "$RELEASE_DIR" -type d -exec chmod 755 {} \;
find "$RELEASE_DIR" -type f -exec chmod 644 {} \;

# `_app/` filenames are content-hashed by Vite, so merging every release's
# _app/ into one cumulative, never-pruned directory is collision-free and
# lets nginx serve a still-referenced old chunk long after its release dir
# is gone. Without this, only the CURRENT release is ever reachable through
# the `build` symlink nginx serves from — a tab open across a deploy 404s
# on its next dynamic import ("chunk deleted out from under a live tab"),
# which is the exact bug the timestamped-releases scheme was meant to avoid.
SHARED_APP_DIR="$WWW_ROOT/shared-app"
mkdir -p "$SHARED_APP_DIR"
cp -rn "$RELEASE_DIR/_app/." "$SHARED_APP_DIR/"
chown -R root:www-data "$SHARED_APP_DIR"
find "$SHARED_APP_DIR" -type d -exec chmod 755 {} \;
find "$SHARED_APP_DIR" -type f -exec chmod 644 {} \;

# `mv -T` refuses to replace a real (non-symlink) directory with a symlink —
# expected on the first run after this script adopts the symlink scheme,
# since $WWW_ROOT/build was a plain directory before. Clear that one-time
# case explicitly; every run after this one is swapping symlink-for-symlink.
if [ -d "$WWW_ROOT/build" ] && [ ! -L "$WWW_ROOT/build" ]; then
    rm -rf "$WWW_ROOT/build"
fi
ln -sfn "$RELEASE_DIR" "$WWW_ROOT/build.new"
mv -Tf "$WWW_ROOT/build.new" "$WWW_ROOT/build"

# Keep the 5 most recent releases (current + a few for in-flight tabs), prune the rest.
# shared-app/ is intentionally NOT pruned here — see comment above.
find "$RELEASES_DIR" -maxdepth 1 -mindepth 1 -type d -printf '%T@ %p\n' 2>/dev/null \
    | sort -rn | tail -n +6 | cut -d' ' -f2- | xargs -r rm -rf

if [ -f "$ENV_FILE" ]; then
    wire_observability_env
fi

# Mint root (id=1) on first boot if missing — temporary ReadWritePaths drop-in,
# then revoke it. Always followed by an explicit restart so the locked-down
# unit is what stays running.
echo -e "\n🔑 Checking root user bootstrap..."
syrinx_ensure_root_bootstrap

echo -e "\n🔁 Restarting $APP_NAME..."
systemctl restart "$APP_NAME.service"
systemctl reload nginx 2>/dev/null || true

sleep 1
if systemctl is-active --quiet "$APP_NAME"; then
    echo "✅ $APP_NAME is ONLINE"
else
    echo "❌ $APP_NAME failed to start — check: journalctl -u $APP_NAME -n 50 --no-pager"
    systemctl status "$APP_NAME.service" --no-pager || true
    echo "   Full log: $LOG_FILE"
    exit 1
fi

# Informational only — update never touches cloudflared.
if [ -n "${APP_DOMAIN:-}" ]; then
    if systemctl is-active --quiet cloudflared 2>/dev/null; then
        code="$(curl -sS -m 8 -o /dev/null -w '%{http_code}' "https://${APP_DOMAIN}/" 2>/dev/null || echo 000)"
        case "$code" in
            000) echo "⚠️  cloudflared is active but https://${APP_DOMAIN} did not respond" ;;
            *)   echo "✅ Cloudflare tunnel looks up (https://${APP_DOMAIN} → HTTP ${code})" ;;
        esac
    else
        echo "⚠️  cloudflared is not active — run sudo ./setup.sh if the public URL is down"
    fi
fi

echo "🚀 Update complete."
echo "📝 Log saved to $LOG_FILE"
echo "📄 App env: $ENV_FILE"
echo "📄 Setup env: $SETUP_ENV"
echo "🗄️  DB shell: sudo $SCRIPT_DIR/psql.sh"
echo "📦 Root export dir: /var/lib/${APP_NAME}/root-export"
