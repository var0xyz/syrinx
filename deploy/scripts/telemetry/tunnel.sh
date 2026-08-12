#!/bin/bash
# Open an SSH local-port-forward to a LAN-only telemetry host's OpenObserve
# UI, for when no Cloudflare Tunnel (TELEMETRY_DOMAIN) is configured.
#
# Forwards localhost:$LOCAL_PORT -> $DEPLOY_HOST:5080, blocking in the
# foreground until interrupted (Ctrl-C). Reuses the same DEPLOY_HOST/
# deploy.env saved by deploy-openobserve-pi.sh, so if you've already
# deployed from this Mac you won't be prompted again.
#
# Usage:
#   ./tunnel.sh
#   DEPLOY_HOST=pi@10.0.0.50 ./tunnel.sh
#   LOCAL_PORT=15080 ./tunnel.sh   # forward a different local port
#   SSH_USER=pi ./tunnel.sh        # default user when only an IP is given

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
DEPLOY_ENV="${DEPLOY_ENV:-$SCRIPT_DIR/deploy.env}"
SSH_USER_DEFAULT="${SSH_USER:-$(id -un)}"
LOCAL_PORT="${LOCAL_PORT:-5080}"
REMOTE_PORT=5080

die() { echo "Error: $*" >&2; exit 1; }
info() { echo "-> $*"; }
ok() { echo "OK: $*"; }

load_saved_host() {
    if [ -f "$DEPLOY_ENV" ]; then
        # shellcheck disable=SC1090
        set -a
        # shellcheck source=/dev/null
        . "$DEPLOY_ENV"
        set +a
    fi
}

save_host() {
    local host="$1"
    umask 077
    {
        printf 'DEPLOY_HOST=%q\n' "$host"
    } > "$DEPLOY_ENV"
}

# Normalize "10.0.0.50" or "pi@10.0.0.50" → user@host (user defaults to $SSH_USER / local login)
normalize_host() {
    local raw="$1"
    raw="${raw#"${raw%%[![:space:]]*}"}"
    raw="${raw%"${raw##*[![:space:]]}"}"
    [ -n "$raw" ] || return 1
    case "$raw" in
        *@*) printf '%s' "$raw" ;;
        *) printf '%s@%s' "$SSH_USER_DEFAULT" "$raw" ;;
    esac
}

prompt_host() {
    local saved="${DEPLOY_HOST:-}" input=""
    if [ -n "${DEPLOY_HOST:-}" ] && [ "${DEPLOY_HOST_SET_BY_ENV:-0}" = "1" ]; then
        normalize_host "$DEPLOY_HOST"
        return 0
    fi

    # UI must go to stderr — stdout is captured into DEPLOY_HOST.
    echo "================================================================" >&2
    echo "SSH tunnel to telemetry host's OpenObserve UI" >&2
    echo "================================================================" >&2
    if [ -n "$saved" ]; then
        read -r -p "Pi address (user@ip or ip) [${saved}]: " input
        input="${input:-$saved}"
    else
        read -r -p "Pi address (user@ip or ip): " input
    fi
    [ -n "$input" ] || die "Pi address is required"
    normalize_host "$input"
}

# --- main --------------------------------------------------------------------

DEPLOY_HOST_FROM_ENV="${DEPLOY_HOST:-}"
load_saved_host
if [ -n "$DEPLOY_HOST_FROM_ENV" ]; then
    DEPLOY_HOST_SET_BY_ENV=1
    DEPLOY_HOST="$DEPLOY_HOST_FROM_ENV"
else
    DEPLOY_HOST_SET_BY_ENV=0
fi
DEPLOY_HOST="$(prompt_host)"
save_host "$DEPLOY_HOST"

info "Checking SSH connectivity..."
if ! ssh -o ConnectTimeout=8 "$DEPLOY_HOST" "true"; then
    die "Cannot SSH to $DEPLOY_HOST — check IP, user, and that the Pi is reachable on this network"
fi
ok "SSH OK"

echo ""
echo "Tunneling http://127.0.0.1:${LOCAL_PORT} -> ${DEPLOY_HOST}:${REMOTE_PORT}"
echo "Open http://127.0.0.1:${LOCAL_PORT} in your browser. Ctrl-C to close."
echo ""

exec ssh -N -L "${LOCAL_PORT}:127.0.0.1:${REMOTE_PORT}" "$DEPLOY_HOST"
