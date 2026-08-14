#!/bin/bash
# Copy a device's mTLS client certificate bundle down from the app host to
# this machine, then optionally delete it from the host.
#
# mtls.sh's mtls_issue_client_cert() writes client.key/client.crt/ca.crt
# under /etc/$APP_NAME/mtls/clients/<name>/ (mode 600/700, root-owned) and
# never prints key material to the terminal or a log — this is the intended
# way to retrieve it afterward without SSHing in and sudo-cat'ing by hand.
#
# Usage:
#   ./cp-client-cert.sh <name>
#   DEPLOY_HOST=pi@10.0.0.50 ./cp-client-cert.sh <name>
#   OUT_DIR=~/Desktop ./cp-client-cert.sh <name>   # default: ./client-certs/<name>
#
# The host address is only asked for once — after that it's saved to
# deploy.env (next to this script) and reused silently on every later run.
# Set FORCE_HOST_PROMPT=1 to be asked again (e.g. switching hosts).
#
# Reads APP_NAME from ~/syrinx/setup.env on the host (the directory
# deploy.sh copies scripts to and setup.sh saves its answers in), so run
# ./deploy.sh setup at least once on this host before this script. To issue
# a new client cert first (before fetching it), run on the host:
#   sudo bash -c 'source mtls.sh; mtls_issue_client_cert <name>'

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
DEPLOY_ENV="${DEPLOY_ENV:-$SCRIPT_DIR/deploy.env}"
SSH_USER_DEFAULT="${SSH_USER:-$(id -un)}"
REMOTE_DIR="${REMOTE_DIR:-syrinx}"

die() { echo "Error: $*" >&2; exit 1; }
info() { echo "-> $*"; }
ok() { echo "OK: $*"; }

usage() {
    sed -n '2,25p' "$0" | sed -e 's/^# //' -e 's/^#$//'
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
    if [ -n "$saved" ] && [ "${FORCE_HOST_PROMPT:-0}" != "1" ]; then
        normalize_host "$saved"
        return 0
    fi

    # UI must go to stderr — stdout is captured into DEPLOY_HOST.
    echo "================================================================" >&2
    echo "Copy mTLS client certificate from app host" >&2
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

CLIENT_NAME="${1:-}"
[ -n "$CLIENT_NAME" ] || die "Usage: $0 <name> — the name a client cert was issued under (mtls_issue_client_cert <name>)"
case "$CLIENT_NAME" in
    *[!a-zA-Z0-9_-]*) die "Client cert name must be alphanumeric/dash/underscore only" ;;
esac

OUT_DIR="${OUT_DIR:-$SCRIPT_DIR/client-certs/$CLIENT_NAME}"

load_saved_host
DEPLOY_HOST="$(prompt_host)"
save_host "$DEPLOY_HOST"

info "Checking SSH connectivity..."
if ! ssh -o ConnectTimeout=8 "$DEPLOY_HOST" "true"; then
    die "Cannot SSH to $DEPLOY_HOST — check IP, user, and that the host is reachable on this network"
fi
ok "SSH OK"

info "Reading APP_NAME from ~/${REMOTE_DIR}/setup.env on the host..."
APP_NAME="$(ssh "$DEPLOY_HOST" "sudo grep -m1 '^APP_NAME=' $(printf '%q' "$REMOTE_DIR")/setup.env 2>/dev/null | cut -d= -f2-" || true)"
[ -n "$APP_NAME" ] || die "Could not read APP_NAME from ~/${REMOTE_DIR}/setup.env on $DEPLOY_HOST — run setup first (./deploy.sh setup)"
ok "APP_NAME=$APP_NAME"

REMOTE_CLIENT_DIR="/etc/${APP_NAME}/mtls/clients/${CLIENT_NAME}"
if ! ssh "$DEPLOY_HOST" "sudo test -f $(printf '%q' "$REMOTE_CLIENT_DIR/client.crt")"; then
    die "No client cert named '$CLIENT_NAME' found at $REMOTE_CLIENT_DIR on $DEPLOY_HOST — issue one first with: sudo bash -c 'source mtls.sh; mtls_issue_client_cert $CLIENT_NAME'"
fi

mkdir -p "$OUT_DIR"
umask 077

info "Copying client.key, client.crt, ca.crt..."
ssh "$DEPLOY_HOST" "sudo cat $(printf '%q' "$REMOTE_CLIENT_DIR/client.key")" > "$OUT_DIR/client.key"
ssh "$DEPLOY_HOST" "sudo cat $(printf '%q' "$REMOTE_CLIENT_DIR/client.crt")" > "$OUT_DIR/client.crt"
ssh "$DEPLOY_HOST" "sudo cat $(printf '%q' "$REMOTE_CLIENT_DIR/ca.crt")" > "$OUT_DIR/ca.crt"
chmod 600 "$OUT_DIR/client.key" "$OUT_DIR/client.crt" "$OUT_DIR/ca.crt"

ok "Saved to $OUT_DIR/ (client.key, client.crt, ca.crt)"
echo ""
echo "Test with:"
echo "  curl --cert $OUT_DIR/client.crt --key $OUT_DIR/client.key https://<domain>/"
echo ""
echo "Import client.crt + client.key into your browser/OS certificate store"
echo "(as a PKCS#12 bundle if needed: openssl pkcs12 -export -in client.crt"
echo " -inkey client.key -out client.p12) to use this device for regular"
echo "browsing against the mTLS-protected site."
echo ""

read -r -p "Delete this client cert from ${DEPLOY_HOST} now? [y/N]: " DELETE_REMOTE
case "${DELETE_REMOTE:-N}" in
    y|Y)
        ssh "$DEPLOY_HOST" "sudo rm -rf $(printf '%q' "$REMOTE_CLIENT_DIR")"
        ok "Deleted from $DEPLOY_HOST"
        ;;
    *)
        info "Left in place on $DEPLOY_HOST at $REMOTE_CLIENT_DIR"
        ;;
esac
