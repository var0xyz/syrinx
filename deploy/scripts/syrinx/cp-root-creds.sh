#!/bin/bash
# Copy the reserved root identity's export file + passphrase down from the
# app host to this machine, then optionally delete them from the host.
#
# root-bootstrap.sh writes both files under /var/lib/$APP_NAME/root-export/
# (mode 600, owned by the app's service user) and never prints the
# passphrase to the terminal or a log — this is the intended way to
# retrieve them afterward without SSHing in and sudo-cat'ing by hand.
#
# Usage:
#   ./cp-root-creds.sh
#   DEPLOY_HOST=pi@10.0.0.50 ./cp-root-creds.sh
#   OUT_DIR=~/Desktop ./cp-root-creds.sh   # default: ./root-creds
#
# Reads APP_NAME from ~/syrinx/setup.env on the host (the directory
# deploy.sh copies scripts to and setup.sh saves its answers in), so run
# ./deploy.sh setup at least once on this host before this script.
#
# Downloads the latest syrinx-1-*.sxi.gpg and its .passphrase sidecar over
# SSH (sudo cat, since both are root-export-user-owned 600 files — nothing
# is chmod'd or left world-readable on the host in the process), writes them
# locally at mode 600, then asks whether to delete the remote copies. Losing
# both the local and remote copies without ever having imported the identity
# means losing the ability to import it — the prompt defaults to "no".

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
DEPLOY_ENV="${DEPLOY_ENV:-$SCRIPT_DIR/deploy.env}"
SSH_USER_DEFAULT="${SSH_USER:-$(id -un)}"
REMOTE_DIR="${REMOTE_DIR:-syrinx}"
OUT_DIR="${OUT_DIR:-$SCRIPT_DIR/root-creds}"

die() { echo "Error: $*" >&2; exit 1; }
info() { echo "-> $*"; }
ok() { echo "OK: $*"; }

usage() {
    sed -n '2,24p' "$0" | sed -e 's/^# //' -e 's/^#$//'
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
    echo "Copy root identity credentials from app host" >&2
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

if [ "${1:-}" = "-h" ] || [ "${1:-}" = "--help" ]; then
    usage
    exit 0
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

info "Reading APP_NAME from ~/${REMOTE_DIR}/setup.env on the host..."
# setup.env is written by setup.sh (run as root, see line 18-19 there) as
# mode 600 root:root — it holds CF_TOKEN. A non-privileged read here always
# comes back empty, so this must go through sudo like every other read of a
# root-owned file below (REMOTE_EXPORT_FILE, REMOTE_PASSPHRASE_FILE, etc.).
APP_NAME="$(ssh "$DEPLOY_HOST" "sudo grep -m1 '^APP_NAME=' $(printf '%q' "$REMOTE_DIR")/setup.env 2>/dev/null | cut -d= -f2-" || true)"
[ -n "$APP_NAME" ] || die "Could not read APP_NAME from ~/${REMOTE_DIR}/setup.env on $DEPLOY_HOST — run setup first (./deploy.sh setup)"
ok "APP_NAME=$APP_NAME"

REMOTE_EXPORT_DIR="/var/lib/${APP_NAME}/root-export"
info "Locating latest export in ${REMOTE_EXPORT_DIR} ..."
REMOTE_EXPORT_FILE="$(ssh "$DEPLOY_HOST" "sudo find $(printf '%q' "$REMOTE_EXPORT_DIR") -maxdepth 1 -type f -name 'syrinx-1-*.sxi.gpg' 2>/dev/null | sort | tail -n1" || true)"
[ -n "$REMOTE_EXPORT_FILE" ] || die "No syrinx-1-*.sxi.gpg found in ${REMOTE_EXPORT_DIR} on $DEPLOY_HOST — has the root identity been minted yet?"
REMOTE_PASSPHRASE_FILE="${REMOTE_EXPORT_FILE}.passphrase"

if ! ssh "$DEPLOY_HOST" "sudo test -f $(printf '%q' "$REMOTE_PASSPHRASE_FILE")"; then
    die "Found $REMOTE_EXPORT_FILE but its .passphrase sidecar is missing on $DEPLOY_HOST — cannot import without it"
fi

EXPORT_BASENAME="$(basename "$REMOTE_EXPORT_FILE")"
mkdir -p "$OUT_DIR"
umask 077
LOCAL_EXPORT_FILE="$OUT_DIR/$EXPORT_BASENAME"
LOCAL_PASSPHRASE_FILE="$OUT_DIR/${EXPORT_BASENAME}.passphrase"

info "Copying export file..."
ssh "$DEPLOY_HOST" "sudo cat $(printf '%q' "$REMOTE_EXPORT_FILE")" > "$LOCAL_EXPORT_FILE"
chmod 600 "$LOCAL_EXPORT_FILE"

info "Copying passphrase..."
ssh "$DEPLOY_HOST" "sudo cat $(printf '%q' "$REMOTE_PASSPHRASE_FILE")" > "$LOCAL_PASSPHRASE_FILE"
chmod 600 "$LOCAL_PASSPHRASE_FILE"

ok "Saved to:"
echo "  $LOCAL_EXPORT_FILE"
echo "  $LOCAL_PASSPHRASE_FILE"
echo ""
echo "Import via SPA /import → \"I only have my keys\", using the passphrase"
echo "in the .passphrase file. Store both somewhere safe — losing them loses"
echo "the ability to import this identity."
echo ""

read -r -p "Delete these files from ${DEPLOY_HOST} now? [y/N]: " DELETE_REMOTE
case "${DELETE_REMOTE:-N}" in
    y|Y)
        ssh "$DEPLOY_HOST" "sudo rm -f $(printf '%q' "$REMOTE_EXPORT_FILE") $(printf '%q' "$REMOTE_PASSPHRASE_FILE")"
        ok "Deleted from $DEPLOY_HOST"
        ;;
    *)
        info "Left in place on $DEPLOY_HOST at $REMOTE_EXPORT_FILE (+ .passphrase)"
        ;;
esac
