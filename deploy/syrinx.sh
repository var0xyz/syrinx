#!/bin/bash
# Run a deploy/scripts/syrinx script on the app host without manually
# copying files over first: scp that directory to the host, then ssh in and
# run the requested script there (as root, via sudo).
#
# Usage:
#   ./syrinx.sh <command> [args...]
#
# Commands (map 1:1 to the script of the same name on the host):
#   setup                       sudo ./setup.sh
#   update [--branch <name>]    sudo ./update.sh [--branch <name>]
#   restart                     sudo ./restart.sh
#   signup-mode <mode>          sudo ./set-signup-mode.sh <mode>
#   psql [args...]              sudo ./psql.sh [args...]
#   wipe-db [--force]           sudo ./wipe-db.sh [--force]
#                                (interactive confirmation unless --force)
#
# Examples:
#   ./syrinx.sh setup
#   DEPLOY_HOST=pi@10.0.0.50 ./syrinx.sh update
#   ./syrinx.sh update --branch canonicalmerge
#   ./syrinx.sh signup-mode invite
#   ./syrinx.sh psql -c 'select count(*) from users;'
#   ./syrinx.sh wipe-db
#   ./syrinx.sh wipe-db --force
#
# Only `setup` prompts for the host address — it's the one command allowed
# to establish or change it, saved to deploy/scripts/syrinx/deploy.env
# (mode 600, gitignored) — same convention as deploy/telemetry.sh. Every
# other command runs silently against whatever's already saved: no banner,
# no host prompt, no connectivity probe. If there's nothing saved yet, or
# the saved host stops working, the fix is the same either way: run
# `./syrinx.sh setup`. Set DEPLOY_HOST or SSH_USER to override for one run
# without touching the saved value.
#
# Root-requiring remote scripts (setup, update, restart, ...) are run with
# sudo; wipe-db and psql prompt interactively (unless wipe-db gets --force),
# and those prompts pass straight through your terminal.
#
# After the first root identity mint, run
# ./deploy/scripts/syrinx/cp-root-creds.sh separately to copy the export
# file + passphrase down from the host (it runs locally, like this script,
# and shares the same deploy.env).

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SYRINX_SCRIPTS_DIR="$SCRIPT_DIR/scripts/syrinx"
DEPLOY_ENV="${DEPLOY_ENV:-$SYRINX_SCRIPTS_DIR/deploy.env}"
SSH_USER_DEFAULT="${SSH_USER:-$(id -un)}"
REMOTE_DIR="${REMOTE_DIR:-syrinx}"

die() { echo "Error: $*" >&2; exit 1; }
info() { echo "-> $*"; }
ok() { echo "OK: $*"; }

usage() {
    sed -n '2,43p' "$0" | sed -e 's/^# //' -e 's/^#$//'
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

# Only `setup` calls this — every other command uses load_saved_host_or_die
# instead. Always asks (even with a saved value) since setup is how you
# change the host; DEPLOY_HOST from the environment skips the prompt for a
# one-off override without touching the saved file.
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

# Every command except setup: use the saved host silently, no prompt/banner/
# connectivity probe. Nothing saved yet means setup has never run here.
load_saved_host_or_die() {
    load_saved_host
    local host="${DEPLOY_HOST:-}"
    [ -n "$host" ] || die "No saved host — run './syrinx.sh setup' first"
    normalize_host "$host"
}

[ $# -ge 1 ] || { usage; exit 2; }
CMD="$1"
shift

# Every mapped command here runs on the host via sudo.
case "$CMD" in
    setup)         REMOTE_SCRIPT="setup.sh" ;;
    update)        REMOTE_SCRIPT="update.sh" ;;
    restart)       REMOTE_SCRIPT="restart.sh" ;;
    signup-mode)   REMOTE_SCRIPT="set-signup-mode.sh" ;;
    psql)          REMOTE_SCRIPT="psql.sh" ;;
    wipe-db)       REMOTE_SCRIPT="wipe-db.sh" ;;
    -h|--help|help) usage; exit 0 ;;
    *) die "Unknown command: $CMD (run '$0 --help' for the list)" ;;
esac

if [ "$CMD" != "setup" ]; then
    DEPLOY_HOST="$(load_saved_host_or_die)"

    scp -q "$SYRINX_SCRIPTS_DIR"/*.sh "$SYRINX_SCRIPTS_DIR/README.md" "${DEPLOY_HOST}:${REMOTE_DIR}/"
    REMOTE_CMD="cd $(printf '%q' "$REMOTE_DIR") && chmod +x ./*.sh && sudo ./$REMOTE_SCRIPT"
    for arg in "$@"; do
        REMOTE_CMD="$REMOTE_CMD $(printf '%q' "$arg")"
    done
    exec ssh -t "$DEPLOY_HOST" "$REMOTE_CMD"
fi

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

info "Copying deploy/scripts/syrinx to ${DEPLOY_HOST}:${REMOTE_DIR} ..."
ssh "$DEPLOY_HOST" "mkdir -p $(printf '%q' "$REMOTE_DIR")"
scp -q "$SYRINX_SCRIPTS_DIR"/*.sh "$SYRINX_SCRIPTS_DIR/README.md" "${DEPLOY_HOST}:${REMOTE_DIR}/"
ok "Copied"

REMOTE_CMD="cd $(printf '%q' "$REMOTE_DIR") && chmod +x ./*.sh && sudo ./$REMOTE_SCRIPT"
for arg in "$@"; do
    REMOTE_CMD="$REMOTE_CMD $(printf '%q' "$arg")"
done

info "Running on ${DEPLOY_HOST}: ./$REMOTE_SCRIPT $*"
echo ""
exec ssh -t "$DEPLOY_HOST" "$REMOTE_CMD"
