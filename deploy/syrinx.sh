#!/bin/bash
# Run a deploy/scripts/syrinx script on the app host without manually
# copying files over first: scp that directory to the host, then ssh in and
# run the requested script there (as root, via sudo).
#
# Usage:
#   ./syrinx.sh <command> [args...]
#
# Commands (map 1:1 to the script of the same name on the host):
#   setup [--branch <name>] [--repo <url>]
#                               sudo ./setup.sh [same args]
#   update [--branch <name>] [--repo <url>]
#                               sudo ./update.sh [same args]
#   restart                     sudo ./restart.sh
#   signup-mode <mode>          sudo ./set-signup-mode.sh <mode>
#   psql [args...]              sudo ./psql.sh [args...]
#   server-key                  sudo ./server-key.sh
#   ops <args...>               sudo ./ops.sh <args...>
#   wipe-db [--force]           sudo ./wipe-db.sh [--force]
#                                (interactive confirmation unless --force)
#
# Commands that copy files down to this machine instead:
#   root-creds <dir>            the root identity export + its .passphrase
#   server-identity <dir>       the server identity bundle + its .passphrase
#   Each fetches the newest one from /var/lib/$APP_NAME/root-export/ into
#   <dir> (mode 600), then asks whether to delete it from the host.
#
# Examples:
#   ./syrinx.sh setup
#   DEPLOY_HOST=pi@10.0.0.50 ./syrinx.sh update
#   ./syrinx.sh update --branch canonicalmerge
#   ./syrinx.sh update --repo https://github.com/someone/syrinx.git
#   ./syrinx.sh signup-mode invite
#   ./syrinx.sh psql -c 'select count(*) from users;'
#   ./syrinx.sh server-key
#   ./syrinx.sh ops check-identity-backup
#   ./syrinx.sh wipe-db
#   ./syrinx.sh wipe-db --force
#   ./syrinx.sh root-creds ~/syrinx-creds
#   ./syrinx.sh server-identity /Volumes/backup/syrinx
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

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SYRINX_SCRIPTS_DIR="$SCRIPT_DIR/scripts/syrinx"
DEPLOY_ENV="${DEPLOY_ENV:-$SYRINX_SCRIPTS_DIR/deploy.env}"
SSH_USER_DEFAULT="${SSH_USER:-$(id -un)}"
# Root-only on the host: everything here runs as root, so the login user
# must not be able to change it. setup.env sits beside scripts/.
REMOTE_DIR="${REMOTE_DIR:-/root/syrinx}"
REMOTE_SCRIPTS_DIR="$REMOTE_DIR/scripts"

# sudo runs on the host, so make its prompt say whose password it wants.
SUDO_PROMPT="[sudo] password for %u on %h (the remote machine, not this computer): "

die() { echo "Error: $*" >&2; exit 1; }
# shellcheck source=scripts/remote-sudo.sh
. "$SCRIPT_DIR/scripts/remote-sudo.sh"
info() { echo "-> $*"; }
ok() { echo "OK: $*"; }

# Copies the scripts into a fresh temp dir the login user owns; root
# installs them into $REMOTE_SCRIPTS_DIR from there. Sets STAGE_DIR.
stage_scripts() {
    STAGE_DIR="$(ssh "$DEPLOY_HOST" "mktemp -d")"
    [ -n "$STAGE_DIR" ] || die "Could not create a staging dir on $DEPLOY_HOST"
    scp -q "$SYRINX_SCRIPTS_DIR"/*.sh "$SYRINX_SCRIPTS_DIR/README.md" "${DEPLOY_HOST}:${STAGE_DIR}/"
}

# Installs the staged scripts as root, then runs $REMOTE_SCRIPT with the
# given args from $REMOTE_SCRIPTS_DIR.
remote_script_cmd() {
    local stage dir root_cmd
    stage="$(printf '%q' "$STAGE_DIR")"
    dir="$(printf '%q' "$REMOTE_SCRIPTS_DIR")"
    # macOS forwards LC_CTYPE=UTF-8 over ssh, which isn't a Linux locale and
    # makes apt/dpkg/psql's perl warn; C.UTF-8 exists on every Debian host.
    root_cmd="install -d -m 700 $(printf '%q' "$REMOTE_DIR") $dir && install -m 700 $stage/*.sh $dir/ && install -m 600 $stage/README.md $dir/ && rm -rf $stage && cd $dir && exec env LC_ALL=C.UTF-8 ./$REMOTE_SCRIPT"
    for arg in "$@"; do
        root_cmd="$root_cmd $(printf '%q' "$arg")"
    done
    printf 'sudo -p %q sh -c %q' "$SUDO_PROMPT" "$root_cmd"
}

usage() {
    sed -n '2,53p' "$0" | sed -e 's/^# //' -e 's/^#$//'
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
    # DEPLOY_HOST from the environment wins for one run; deploy.env would
    # otherwise overwrite it.
    local from_env="${DEPLOY_HOST:-}"
    load_saved_host
    local host="${from_env:-${DEPLOY_HOST:-}}"
    [ -n "$host" ] || die "No saved host — run './syrinx.sh setup' first"
    normalize_host "$host"
}

# Copies the newest export matching $1, plus its .passphrase sidecar, into
# $2 over sudo cat (nothing is chmod'd on the host), then offers to delete
# both there. Losing every copy before importing loses the identity.
fetch_export() {
    local pattern="$1" out_dir="$2" export_dir remote passphrase base
    DEPLOY_HOST="$(load_saved_host_or_die)"
    remote_sudo_init

    # setup.env is root-owned 600 (it holds CF_TOKEN), hence sudo.
    APP_NAME="$(remote_sudo "sudo grep -m1 '^APP_NAME=' $(printf '%q' "$REMOTE_DIR")/setup.env 2>/dev/null | cut -d= -f2-" || true)"
    [ -n "$APP_NAME" ] || die "Could not read APP_NAME from ${REMOTE_DIR}/setup.env on $DEPLOY_HOST — run './syrinx.sh setup' first"
    export_dir="/var/lib/${APP_NAME}/root-export"

    remote="$(remote_sudo "sudo find $(printf '%q' "$export_dir") -maxdepth 1 -type f -name '$pattern' 2>/dev/null | sort | tail -n1" || true)"
    [ -n "$remote" ] || die "No $pattern in $export_dir on $DEPLOY_HOST — has setup run, or was it already deleted?"
    passphrase="${remote}.passphrase"
    remote_sudo "sudo test -f $(printf '%q' "$passphrase")" \
        || die "Found $remote but its .passphrase sidecar is missing on $DEPLOY_HOST — cannot import without it"

    base="$(basename "$remote")"
    (
        umask 077
        mkdir -p "$out_dir"
        remote_sudo "sudo cat $(printf '%q' "$remote")" > "$out_dir/$base"
        remote_sudo "sudo cat $(printf '%q' "$passphrase")" > "$out_dir/$base.passphrase"
    )
    chmod 600 "$out_dir/$base" "$out_dir/$base.passphrase"
    ok "Saved $out_dir/$base (+ .passphrase)"
    FETCHED_FILE="$out_dir/$base"
    FETCHED_REMOTE=("$remote" "$passphrase")
}

# Prints the stakes ($1), then asks before deleting what fetch_export
# copied; defaults to keeping it.
offer_remote_delete() {
    local answer red="" reset=""
    if [ -t 1 ]; then red=$'\033[1;31m'; reset=$'\033[0m'; fi
    echo ""
    echo "${red}$1${reset}"
    echo "   Keep the file and its passphrase apart, somewhere only you can"
    echo "   reach, and don't leave them lying around on the host either."
    echo ""
    read -r -p "Delete them from ${DEPLOY_HOST} now? [y/N]: " answer || true
    case "${answer:-N}" in
        y|Y)
            remote_sudo "sudo rm -f $(printf '%q' "${FETCHED_REMOTE[0]}") $(printf '%q' "${FETCHED_REMOTE[1]}")"
            ok "Deleted from $DEPLOY_HOST"
            ;;
        *) info "Left in place on $DEPLOY_HOST" ;;
    esac
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
    server-key)    REMOTE_SCRIPT="server-key.sh" ;;
    ops)           REMOTE_SCRIPT="ops.sh" ;;
    wipe-db)       REMOTE_SCRIPT="wipe-db.sh" ;;
    root-creds)
        [ $# -eq 1 ] && [ -n "$1" ] || die "Usage: $0 root-creds <dir>"
        fetch_export 'syrinx-1-*.sxi.gpg' "$1"
        echo "   Import it via the SPA's /import → \"I only have my keys\","
        echo "   using the passphrase in $(basename "$FETCHED_FILE").passphrase."
        offer_remote_delete "Whoever holds these controls the root account, and with it the whole community."
        exit 0
        ;;
    server-identity)
        [ $# -eq 1 ] && [ -n "$1" ] || die "Usage: $0 server-identity <dir>"
        fetch_export 'syrinx-identity-*.sxi.gpg' "$1"
        echo "   A recovery is rebuilt from this bundle:"
        echo "   ${APP_NAME}-ops import-identity <bundle> < <bundle>.passphrase"
        offer_remote_delete "Whoever holds these can sign as this server and impersonate it to every user and peer."
        exit 0
        ;;
    -h|--help|help) usage; exit 0 ;;
    *) die "Unknown command: $CMD (run '$0 --help' for the list)" ;;
esac

if [ "$CMD" != "setup" ]; then
    DEPLOY_HOST="$(load_saved_host_or_die)"

    stage_scripts
    REMOTE_CMD="$(remote_script_cmd "$@")"
    # No terminal for server-key, so only the key reaches stdout.
    if [ "$CMD" = "server-key" ]; then
        exec ssh "$DEPLOY_HOST" "$REMOTE_CMD"
    fi
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

info "Copying deploy/scripts/syrinx to ${DEPLOY_HOST}:${REMOTE_SCRIPTS_DIR} ..."
stage_scripts
ok "Copied"

REMOTE_CMD="$(remote_script_cmd "$@")"

info "Running on ${DEPLOY_HOST}: ./$REMOTE_SCRIPT $*"
echo ""
exec ssh -t "$DEPLOY_HOST" "$REMOTE_CMD"
