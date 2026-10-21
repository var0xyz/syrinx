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
# deploy-openobserve-pi.sh already run from the workstation and do their own
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

# Pi hosts can't run upstream OpenObserve binaries (no AES), so setup needs
# a Pi build: an existing one, a local cross-compile, or an on-Pi build.
REMOTE_ENV=""
if [ "$CMD" = "setup" ] && ssh "$DEPLOY_HOST" \
    "tr -d '\\0' </proc/device-tree/model 2>/dev/null | grep -qi 'raspberry pi'"; then
    O2_VERSION="$(ssh "$DEPLOY_HOST" "sudo grep -s '^O2_VERSION=' $(printf '%q' "$REMOTE_DIR")/telemetry.env" | cut -d= -f2- || true)"
    O2_VERSION="${O2_VERSION:-0.91.5}"
    O2_VERSION_TAG="v${O2_VERSION#v}"
    O2_ARTIFACT_NAME="openobserve-${O2_VERSION_TAG}-pi-arm64"
    LOCAL_ARTIFACT="$TELEMETRY_SCRIPTS_DIR/dist/$O2_ARTIFACT_NAME"
    REMOTE_ARTIFACT="/var/lib/openobserve/build/$O2_ARTIFACT_NAME"

    existing="" existing_src=""
    if ssh "$DEPLOY_HOST" "sudo test -x $(printf '%q' "$REMOTE_ARTIFACT")"; then
        existing="on the Pi ($REMOTE_ARTIFACT)" existing_src="pi"
    elif [ -f "$LOCAL_ARTIFACT" ]; then
        existing="on this machine ($LOCAL_ARTIFACT)" existing_src="local"
    fi

    echo ""
    echo "OpenObserve ${O2_VERSION_TAG} build for the Raspberry Pi:"
    options=()
    [ -n "$existing" ] && options+=("existing")
    # cross-compile-openobserve-pi.sh runs on macOS, apt or pacman hosts.
    if [ "$(uname -s)" = "Darwin" ] || command -v apt-get >/dev/null 2>&1 \
        || command -v pacman >/dev/null 2>&1; then
        options+=("cross")
    fi
    options+=("pi")
    i=1
    for opt in "${options[@]}"; do
        case "$opt" in
            existing) echo "  $i) Use the already compiled binary ${existing}" ;;
            cross)    echo "  $i) Cross-compile on this machine (minutes)" ;;
            pi)       echo "  $i) Build on the Pi (1–3+ hours)" ;;
        esac
        i=$((i + 1))
    done
    read -r -p "Choice [1]: " choice
    choice="${choice:-1}"
    case "$choice" in
        *[!0-9]*|0) die "Invalid choice: $choice" ;;
    esac
    [ "$choice" -le "${#options[@]}" ] || die "Invalid choice: $choice"
    O2_BUILD_CHOICE="${options[$((choice - 1))]}"

    case "$O2_BUILD_CHOICE" in
        existing|cross)
            if [ "$O2_BUILD_CHOICE" = "cross" ]; then
                O2_VERSION="$O2_VERSION" "$TELEMETRY_SCRIPTS_DIR/cross-compile-openobserve-pi.sh"
            fi
            # Uploads the local artifact into the Pi's cache.
            if [ "$O2_BUILD_CHOICE" = "cross" ] || [ "$existing_src" = "local" ]; then
                DEPLOY_HOST="$DEPLOY_HOST" O2_VERSION="$O2_VERSION" RESTART=0 \
                    "$TELEMETRY_SCRIPTS_DIR/deploy-openobserve-pi.sh"
            fi
            ;;
        pi) REMOTE_ENV="O2_FORCE_REBUILD=1" ;;
    esac
fi

# Only what setup.sh/update.sh/build-openobserve-pi.sh actually need on the
# Pi — cross-compile-openobserve-pi.sh, deploy-openobserve-pi.sh, tunnel.sh,
# and this script itself all run from the workstation and are left out.
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
REMOTE_CMD="$REMOTE_CMD && sudo SHOW_PASSWORD=$(printf '%q' "${SHOW_PASSWORD:-0}") $REMOTE_ENV ./$REMOTE_SCRIPT"
for arg in "$@"; do
    REMOTE_CMD="$REMOTE_CMD $(printf '%q' "$arg")"
done

info "Running on ${DEPLOY_HOST}: ./$REMOTE_SCRIPT $*"
echo ""
exec ssh -t "$DEPLOY_HOST" "$REMOTE_CMD"
