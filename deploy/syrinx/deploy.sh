#!/bin/bash
# Run a deploy/syrinx script on the app host without manually copying files
# over first: scp this directory to the host, then ssh in and run the
# requested script there (as root, via sudo).
#
# Usage:
#   ./deploy.sh <command> [args...]
#
# Commands (map 1:1 to the script of the same name on the host):
#   setup                    sudo ./setup.sh
#   update                    sudo ./update.sh
#   restart                   sudo ./restart.sh
#   signup-mode <mode>        sudo ./set-signup-mode.sh <mode>
#   psql [args...]            sudo ./psql.sh [args...]
#   wipe-db                   sudo ./wipe-db.sh   (interactive confirmation)
#
# Examples:
#   ./deploy.sh setup
#   DEPLOY_HOST=pi@10.0.0.50 ./deploy.sh update
#   ./deploy.sh signup-mode invite
#   ./deploy.sh psql -c 'select count(*) from users;'
#   ./deploy.sh wipe-db
#
# The host address is prompted for once and saved to deploy.env (mode 600,
# gitignored) — same convention as deploy/telemetry/deploy-openobserve-pi.sh
# and deploy/telemetry/tunnel.sh. Set DEPLOY_HOST or SSH_USER to override.
#
# Root-requiring remote scripts (setup, update, restart, ...) are run with
# sudo; wipe-db and psql are interactive, so their prompts pass straight
# through your terminal.
#
# After the first root identity mint, run ./cp-root-creds.sh separately to
# copy the export file + passphrase down from the host (it runs locally,
# like this script, and shares the same deploy.env).

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
DEPLOY_ENV="${DEPLOY_ENV:-$SCRIPT_DIR/deploy.env}"
SSH_USER_DEFAULT="${SSH_USER:-$(id -un)}"
REMOTE_DIR="${REMOTE_DIR:-syrinx}"

die() { echo "Error: $*" >&2; exit 1; }
info() { echo "-> $*"; }
ok() { echo "OK: $*"; }

usage() {
    sed -n '2,34p' "$0" | sed -e 's/^# //' -e 's/^#$//'
}

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
    echo "Deploy Syrinx app scripts to host" >&2
    echo "================================================================" >&2
    if [ -n "$saved" ]; then
        read -r -p "App host address (user@ip or ip) [${saved}]: " input
        input="${input:-$saved}"
    else
        read -r -p "App host address (user@ip or ip): " input
    fi
    [ -n "$input" ] || die "App host address is required"
    normalize_host "$input"
}

[ $# -ge 1 ] || { usage; exit 2; }
CMD="$1"
shift

case "$CMD" in
    setup)         REMOTE_SCRIPT="setup.sh"; NEEDS_SUDO=1 ;;
    update)        REMOTE_SCRIPT="update.sh"; NEEDS_SUDO=1 ;;
    restart)       REMOTE_SCRIPT="restart.sh"; NEEDS_SUDO=1 ;;
    signup-mode)   REMOTE_SCRIPT="set-signup-mode.sh"; NEEDS_SUDO=1 ;;
    psql)          REMOTE_SCRIPT="psql.sh"; NEEDS_SUDO=1 ;;
    wipe-db)       REMOTE_SCRIPT="wipe-db.sh"; NEEDS_SUDO=1 ;;
    -h|--help|help) usage; exit 0 ;;
    *) die "Unknown command: $CMD (run '$0 --help' for the list)" ;;
esac

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
    die "Cannot SSH to $DEPLOY_HOST — check IP, user, and that the host is reachable on this network"
fi
ok "SSH OK"

info "Copying deploy/syrinx to ${DEPLOY_HOST}:${REMOTE_DIR} ..."
ssh "$DEPLOY_HOST" "mkdir -p $(printf '%q' "$REMOTE_DIR")"
scp -q "$SCRIPT_DIR"/*.sh "$SCRIPT_DIR/README.md" "${DEPLOY_HOST}:${REMOTE_DIR}/"
ok "Copied"

REMOTE_CMD="cd $(printf '%q' "$REMOTE_DIR") && chmod +x ./*.sh"
if [ "$NEEDS_SUDO" = "1" ]; then
    REMOTE_CMD="$REMOTE_CMD && sudo ./$REMOTE_SCRIPT"
else
    REMOTE_CMD="$REMOTE_CMD && ./$REMOTE_SCRIPT"
fi
for arg in "$@"; do
    REMOTE_CMD="$REMOTE_CMD $(printf '%q' "$arg")"
done

info "Running on ${DEPLOY_HOST}: ./$REMOTE_SCRIPT $*"
echo ""
exec ssh -t "$DEPLOY_HOST" "$REMOTE_CMD"
