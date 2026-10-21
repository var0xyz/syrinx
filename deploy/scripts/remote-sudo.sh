#!/bin/bash
# Sourced by workstation scripts that run sudo on $DEPLOY_HOST over plain ssh,
# where there's no tty for sudo to prompt on. Needs die() and DEPLOY_HOST.
# shellcheck disable=SC2029

REMOTE_SUDO_PASSWORD="${REMOTE_SUDO_PASSWORD-}"
REMOTE_SUDO_READY=0

# Reads the password from the first stdin line into the environment, where a
# SUDO_ASKPASS helper (holding no secret itself) hands it to `sudo -A`.
# shellcheck disable=SC2016
REMOTE_SUDO_PRELUDE='IFS= read -r __sudo_pw; export __sudo_pw; SUDO_ASKPASS=$(mktemp); export SUDO_ASKPASS; trap '\''rm -f "$SUDO_ASKPASS"'\'' EXIT; printf '\''#!/bin/sh\nprintf "%%s\\n" "$__sudo_pw"\n'\'' > "$SUDO_ASKPASS"; chmod 700 "$SUDO_ASKPASS"; sudo() { command sudo -A "$@"; };'

# Call at top level (not in $(...)) before remote_sudo, so the password sticks.
remote_sudo_init() {
    [ "$REMOTE_SUDO_READY" = 1 ] && return 0
    if [ -z "$REMOTE_SUDO_PASSWORD" ] && ssh "$DEPLOY_HOST" "sudo -n true" 2>/dev/null </dev/null; then
        REMOTE_SUDO_READY=1
        return 0
    fi
    if [ -z "$REMOTE_SUDO_PASSWORD" ]; then
        read -r -s -p "sudo password for ${DEPLOY_HOST} (that machine's, not this computer's): " REMOTE_SUDO_PASSWORD </dev/tty
        echo >&2
    fi
    REMOTE_SUDO_READY=1
    if ! remote_sudo "sudo -k; sudo true" >/dev/null 2>&1; then
        REMOTE_SUDO_PASSWORD=""
        REMOTE_SUDO_READY=0
        die "sudo on $DEPLOY_HOST rejected the password"
    fi
}

# Runs a shell command on $DEPLOY_HOST in which sudo works without a tty.
remote_sudo() {
    [ "$REMOTE_SUDO_READY" = 1 ] || die "remote_sudo_init must run first"
    if [ -z "$REMOTE_SUDO_PASSWORD" ]; then
        ssh "$DEPLOY_HOST" "$1" </dev/null
        return
    fi
    printf '%s\n' "$REMOTE_SUDO_PASSWORD" \
        | ssh "$DEPLOY_HOST" "bash -c $(printf '%q' "$REMOTE_SUDO_PRELUDE $1")"
}

# Like remote_sudo, passing this script's stdin (e.g. a heredoc) through.
remote_sudo_stdin() {
    [ "$REMOTE_SUDO_READY" = 1 ] || die "remote_sudo_init must run first"
    if [ -z "$REMOTE_SUDO_PASSWORD" ]; then
        ssh "$DEPLOY_HOST" "$1"
        return
    fi
    { printf '%s\n' "$REMOTE_SUDO_PASSWORD"; cat; } \
        | ssh "$DEPLOY_HOST" "bash -c $(printf '%q' "$REMOTE_SUDO_PRELUDE $1")"
}
