#!/bin/bash
# Run a deploy/scripts/telemetry script on the telemetry host without
# manually copying files over first: scp that directory to the host, then
# ssh in and run the requested script there (as root, via sudo).
#
# Usage:
#   ./telemetry.sh <command> [args...]
#
# Commands (map 1:1 to the script of the same name on the host):
#   setup                      sudo ./setup.sh
#   update                     sudo ./update.sh
#   build                      sudo ./build-openobserve-pi.sh
#   tunnel                     runs tunnel.sh locally (no scp/ssh-to-script)
#
# Examples:
#   ./telemetry.sh setup
#   DEPLOY_HOST=pi@10.0.0.50 ./telemetry.sh update
#   ./telemetry.sh build
#   SHOW_PASSWORD=1 ./telemetry.sh setup    # print the OpenObserve admin password
#   ./telemetry.sh tunnel                   # SSH port-forward to the UI
#
# setup/update/build wrap the on-Pi scripts of the same name — this scp's
# them over and runs them via ssh+sudo. cross-compile-openobserve-pi.sh and
# deploy-openobserve-pi.sh already run from the Mac and do their own
# ssh/scp — run those directly (from deploy/scripts/telemetry/) instead of
# through this script.
#
# The host address is prompted for once and saved to
# deploy/scripts/telemetry/deploy.env (mode 600, gitignored) — the same
# file/convention used by deploy-openobserve-pi.sh and tunnel.sh, so all
# three share one saved host.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
TELEMETRY_SCRIPTS_DIR="$SCRIPT_DIR/scripts/telemetry"
DEPLOY_ENV="${DEPLOY_ENV:-$TELEMETRY_SCRIPTS_DIR/deploy.env}"
SSH_USER_DEFAULT="${SSH_USER:-$(id -un)}"
REMOTE_DIR="${REMOTE_DIR:-telemetry}"

die() { echo "Error: $*" >&2; exit 1; }
info() { echo "-> $*"; }
ok() { echo "OK: $*"; }

usage() {
    sed -n '2,31p' "$0" | sed -e 's/^# //' -e 's/^#$//'
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
    echo "Deploy telemetry scripts to host" >&2
    echo "================================================================" >&2
    if [ -n "$saved" ]; then
        read -r -p "Telemetry host address (user@ip or ip) [${saved}]: " input
        input="${input:-$saved}"
    else
        read -r -p "Telemetry host address (user@ip or ip): " input
    fi
    [ -n "$input" ] || die "Telemetry host address is required"
    normalize_host "$input"
}

[ $# -ge 1 ] || { usage; exit 2; }
CMD="$1"
shift

# tunnel.sh runs entirely locally (an SSH port-forward, nothing to scp or
# run remotely) — hand off to it directly instead of the scp/ssh flow below.
if [ "$CMD" = "tunnel" ]; then
    exec "$TELEMETRY_SCRIPTS_DIR/tunnel.sh" "$@"
fi

case "$CMD" in
    setup)  REMOTE_SCRIPT="setup.sh" ;;
    update) REMOTE_SCRIPT="update.sh" ;;
    build)  REMOTE_SCRIPT="build-openobserve-pi.sh" ;;
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

# Only what setup.sh/update.sh/build-openobserve-pi.sh actually need on the
# Pi — cross-compile-openobserve-pi.sh, deploy-openobserve-pi.sh, tunnel.sh,
# and this script itself all run from the Mac and are left out.
info "Copying deploy/scripts/telemetry to ${DEPLOY_HOST}:${REMOTE_DIR} ..."
ssh "$DEPLOY_HOST" "mkdir -p $(printf '%q' "$REMOTE_DIR")/dashboards"
scp -q "$TELEMETRY_SCRIPTS_DIR/common.sh" "$TELEMETRY_SCRIPTS_DIR/setup.sh" "$TELEMETRY_SCRIPTS_DIR/update.sh" \
    "$TELEMETRY_SCRIPTS_DIR/build-openobserve-pi.sh" "$TELEMETRY_SCRIPTS_DIR/README.md" \
    "${DEPLOY_HOST}:${REMOTE_DIR}/"
scp -q "$TELEMETRY_SCRIPTS_DIR"/dashboards/*.json "$TELEMETRY_SCRIPTS_DIR"/dashboards/*.py "${DEPLOY_HOST}:${REMOTE_DIR}/dashboards/"
ok "Copied"

# Forward SHOW_PASSWORD (setup.sh/update.sh read it as a plain env var) —
# ssh doesn't carry local env vars over on its own, so it has to be spelled
# out explicitly in the remote command.
REMOTE_CMD="cd $(printf '%q' "$REMOTE_DIR") && chmod +x ./*.sh"
REMOTE_CMD="$REMOTE_CMD && sudo SHOW_PASSWORD=$(printf '%q' "${SHOW_PASSWORD:-0}") ./$REMOTE_SCRIPT"
for arg in "$@"; do
    REMOTE_CMD="$REMOTE_CMD $(printf '%q' "$arg")"
done

info "Running on ${DEPLOY_HOST}: ./$REMOTE_SCRIPT $*"
echo ""
exec ssh -t "$DEPLOY_HOST" "$REMOTE_CMD"
