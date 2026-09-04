#!/bin/bash
# ==============================================================================
# Syrinx root-user one-shot bootstrap helpers (sourced by setup.sh / update.sh).
#
# First mint: temporary systemd drop-in grants StateDirectory + ReadWritePaths
# on /var/lib/$APP_NAME (ProtectSystem=strict blocks leaf-only paths) plus
# ROOT_KEY_EXPORT_PATH; passphrase in app.env; start once → app writes .sxi.gpg
# and exits 0. Then strip passphrase/path from env and remove the drop-in so
# the drop-in so the hardened unit is write-locked again.
#
# Requires before sourcing:
#   ENV_FILE, APP_NAME, APP_USER
#   ensure_env_kv / remove_env_kv
# ==============================================================================

syrinx_root_gen_secret() {
    openssl rand -base64 18 | tr -dc 'a-zA-Z0-9'
}

syrinx_root_export_dir() {
    printf '%s' "/var/lib/${APP_NAME}/root-export"
}

syrinx_root_bootstrap_marker() {
    printf '%s' "$(syrinx_root_export_dir)/.bootstrap-complete"
}

# Latest keys-only export on disk (empty if none).
syrinx_root_export_file() {
    local export_dir
    export_dir="$(syrinx_root_export_dir)"
    find "$export_dir" -maxdepth 1 -type f -name 'syrinx-1-*.sxi.gpg' 2>/dev/null \
        | sort | tail -n1
}

syrinx_root_bootstrap_complete() {
    local marker export_file
    marker="$(syrinx_root_bootstrap_marker)"
    if [ -f "$marker" ]; then
        return 0
    fi
    export_file="$(syrinx_root_export_file)"
    # Require the .passphrase sidecar too — an export file alone (no
    # passphrase, no marker) is undecryptable and not a complete bootstrap.
    [ -n "$export_file" ] && [ -f "$export_file" ] && [ -f "${export_file}.passphrase" ]
}

# Failed one-shot mint persists users.id=1 before writing .sxi.gpg (see root.go).
syrinx_root_mint_in_progress() {
    [ -f "$(syrinx_root_export_dropin)" ] \
        || grep -q '^ROOT_KEY_EXPORT_PASSPHRASE=.\+' "$ENV_FILE" 2>/dev/null
}

syrinx_root_other_user_count() {
    local host port user pass db
    # shellcheck disable=SC1090
    set -a
    # shellcheck source=/dev/null
    . "$ENV_FILE"
    set +a
    host="${DB_HOST:-127.0.0.1}"
    port="${DB_PORT:-5432}"
    user="${DB_USER:?DB_USER missing in $ENV_FILE}"
    pass="${DB_PASSWORD:?DB_PASSWORD missing in $ENV_FILE}"
    db="${DB_NAME:?DB_NAME missing in $ENV_FILE}"

    PGPASSWORD="$pass" psql -h "$host" -p "$port" -U "$user" -d "$db" -tAc \
        "SELECT COUNT(*) FROM users WHERE id <> '1' AND id NOT LIKE '1@%'" 2>/dev/null | tr -d '[:space:]'
}

syrinx_delete_orphan_root() {
    local host port user pass db
    # shellcheck disable=SC1090
    set -a
    # shellcheck source=/dev/null
    . "$ENV_FILE"
    set +a
    host="${DB_HOST:-127.0.0.1}"
    port="${DB_PORT:-5432}"
    user="${DB_USER:?DB_USER missing in $ENV_FILE}"
    pass="${DB_PASSWORD:?DB_PASSWORD missing in $ENV_FILE}"
    db="${DB_NAME:?DB_NAME missing in $ENV_FILE}"

    echo "🔹 Removing incomplete root user (id=1) so mint can run again..."
    systemctl stop "$APP_NAME.service" 2>/dev/null || true
    # identities(id) cascades to users and public_keys(owner) — one delete
    # covers all three, and matches both the bare "1" and canonical "1@..." id.
    PGPASSWORD="$pass" psql -h "$host" -p "$port" -U "$user" -d "$db" -v ON_ERROR_STOP=1 <<'SQL'
DELETE FROM identities WHERE id = '1' OR id LIKE '1@%';
SQL
    rm -f "$(syrinx_root_bootstrap_marker)"
}

syrinx_root_export_dropin_dir() {
    printf '%s' "/etc/systemd/system/${APP_NAME}.service.d"
}

syrinx_root_export_dropin() {
    printf '%s' "$(syrinx_root_export_dropin_dir)/50-root-export.conf"
}

# True if the reserved root user (id=1) already exists in the app DB.
syrinx_root_exists() {
    local host port user pass db
    # shellcheck disable=SC1090
    set -a
    # shellcheck source=/dev/null
    . "$ENV_FILE"
    set +a
    host="${DB_HOST:-127.0.0.1}"
    port="${DB_PORT:-5432}"
    user="${DB_USER:?DB_USER missing in $ENV_FILE}"
    pass="${DB_PASSWORD:?DB_PASSWORD missing in $ENV_FILE}"
    db="${DB_NAME:?DB_NAME missing in $ENV_FILE}"

    PGPASSWORD="$pass" psql -h "$host" -p "$port" -U "$user" -d "$db" -tAc \
        "SELECT 1 FROM users WHERE id = '1' OR id LIKE '1@%' LIMIT 1" 2>/dev/null | grep -q 1
}

syrinx_strip_root_export_env() {
    remove_env_kv "ROOT_KEY_EXPORT_PASSPHRASE"
    remove_env_kv "ROOT_KEY_EXPORT_PATH"
}

syrinx_install_root_export_dropin() {
    local dir export_dir lib_dir dropin
    export_dir="$(syrinx_root_export_dir)"
    lib_dir="/var/lib/${APP_NAME}"
    dir="$(syrinx_root_export_dropin_dir)"
    dropin="$(syrinx_root_export_dropin)"

    mkdir -p "$export_dir"
    # Parent must be owned by the service user: ProtectSystem=strict makes /var
    # read-only unless StateDirectory/ReadWritePaths apply, and systemd will not
    # re-chown an existing root-owned StateDirectory path.
    chown "$APP_USER:$APP_USER" "$lib_dir" "$export_dir"
    chmod 755 "$lib_dir"
    chmod 700 "$export_dir"

    mkdir -p "$dir"
    cat > "$dropin" <<EOF
# Temporary — removed after root (id=1) is minted. Do not edit by hand.
[Service]
# ProtectSystem=strict mounts all of /var read-only; StateDirectory + parent
# ReadWritePaths are required so the one-shot .sxi.gpg export can be written.
StateDirectory=${APP_NAME}
ReadWritePaths=${lib_dir}
Environment=ROOT_KEY_EXPORT_PATH=${export_dir}
EOF
    systemctl daemon-reload
}

syrinx_remove_root_export_dropin() {
    local dropin dir
    dropin="$(syrinx_root_export_dropin)"
    dir="$(syrinx_root_export_dropin_dir)"
    if [ -f "$dropin" ]; then
        rm -f "$dropin"
        rmdir "$dir" 2>/dev/null || true
        systemctl daemon-reload
        echo "🔹 Removed temporary root-export write path from systemd unit"
    fi
}

# Wait for the one-shot mint to finish (exit 0 → inactive with Restart=on-failure).
# Checks the DB + export file (ground truth) first on every poll, not just
# ExecMainStatus/Result — a stray extra restart cycle (e.g. Restart=on-failure
# still catching up from a prior crash loop) can land the unit back on a
# *different* exit than the one that actually did the export, making
# ExecMainStatus alone unreliable right at the transition.
syrinx_wait_root_export_exit() {
    local i status result
    for i in $(seq 1 90); do
        if syrinx_root_bootstrap_complete; then
            return 0
        fi
        if ! systemctl is-active --quiet "$APP_NAME.service"; then
            status="$(systemctl show -p ExecMainStatus --value "$APP_NAME.service" 2>/dev/null || echo 1)"
            result="$(systemctl show -p Result --value "$APP_NAME.service" 2>/dev/null || echo unknown)"
            if [ "$status" = "0" ] || [ "$result" = "success" ]; then
                return 0
            fi
            echo "❌ Root export process exited unsuccessfully (status=$status result=$result)" >&2
            journalctl -u "$APP_NAME.service" -n 40 --no-pager >&2 || true
            return 1
        fi
        sleep 1
    done
    echo "❌ Timed out waiting for root export (service still running)" >&2
    journalctl -u "$APP_NAME.service" -n 40 --no-pager >&2 || true
    return 1
}

# If root is missing: grant write path, mint once, revoke write path + passphrase.
# If root exists: ensure drop-in and export env vars are gone.
# Caller then systemctl restart for a normal (locked-down) HTTP start.
syrinx_ensure_root_bootstrap() {
    local passphrase export_dir export_file

    if [ ! -f "$ENV_FILE" ]; then
        echo "❌ Error: missing $ENV_FILE — cannot bootstrap root user" >&2
        return 1
    fi
    if [ ! -x "/usr/local/bin/$APP_NAME" ]; then
        echo "❌ Error: missing /usr/local/bin/$APP_NAME — build/install first" >&2
        return 1
    fi
    if [ ! -f "/etc/systemd/system/${APP_NAME}.service" ]; then
        echo "❌ Error: missing systemd unit for $APP_NAME" >&2
        return 1
    fi

    if syrinx_root_exists; then
        if syrinx_root_bootstrap_complete && [ "${FORCE_ROOT_REMINT:-0}" != "1" ]; then
            syrinx_strip_root_export_env
            syrinx_remove_root_export_dropin
            echo "🔹 Root user already exists — skipping one-shot mint"
            return 0
        fi

        if syrinx_root_mint_in_progress; then
            echo "⚠️  Root user (id=1) exists but export file is missing — retrying failed mint..."
            syrinx_delete_orphan_root
        elif [ "${FORCE_ROOT_REMINT:-0}" = "1" ]; then
            if [ "$(syrinx_root_other_user_count)" != "0" ]; then
                echo "❌ Refusing FORCE_ROOT_REMINT=1: other users exist in the database" >&2
                echo "   Root re-mint would strand them. Use wipe-db only if you mean to reset everything." >&2
                return 1
            fi
            echo "⚠️  FORCE_ROOT_REMINT=1 — removing root (id=1) and minting again..."
            syrinx_delete_orphan_root
        else
            echo "❌ Root user (id=1) exists in the database but no .sxi.gpg export was found." >&2
            echo "   This happens when a prior mint wrote the DB row but failed to write the export file." >&2
            echo "   Without the export file the root private key is unrecoverable (it is never stored on server)." >&2
            echo "" >&2
            echo "   On a fresh install with no other users:" >&2
            echo "     sudo FORCE_ROOT_REMINT=1 ./scripts/setup.sh" >&2
            echo "   or: sudo ./scripts/wipe-db.sh && sudo ./scripts/setup.sh" >&2
            return 1
        fi
    fi

    echo -e "\n🔑 Root user (id=1) missing — one-shot mint with temporary write path..."
    export_dir="$(syrinx_root_export_dir)"
    passphrase="$(syrinx_root_gen_secret)"
    ensure_env_kv "ROOT_KEY_EXPORT_PASSPHRASE" "$passphrase"
    # Path is supplied via the systemd drop-in Environment= (not app.env), so a
    # failed mint cannot leave ROOT_KEY_EXPORT_PATH stuck in the env file.
    remove_env_kv "ROOT_KEY_EXPORT_PATH"

    syrinx_install_root_export_dropin

    echo "🔹 Starting $APP_NAME for root export (ReadWritePaths=$export_dir)..."
    systemctl restart "$APP_NAME.service"
    if ! syrinx_wait_root_export_exit; then
        # Stop rather than leave it running: with the passphrase still set,
        # Restart=on-failure would otherwise crash-loop the unit indefinitely
        # against maybeExportRootKey's already-exists guard.
        systemctl stop "$APP_NAME.service" 2>/dev/null || true
        echo "   Passphrase left in $ENV_FILE; drop-in left in place for retry" >&2
        return 1
    fi

    export_file="$(syrinx_root_export_file)"
    if [ -z "$export_file" ] || [ ! -f "$export_file" ]; then
        echo "❌ Root export exited 0 but no syrinx-1-*.sxi.gpg in $export_dir" >&2
        return 1
    fi
    chown "$APP_USER:$APP_USER" "$export_file" 2>/dev/null || true
    chmod 600 "$export_file" 2>/dev/null || true

    syrinx_strip_root_export_env
    syrinx_remove_root_export_dropin

    # Retry briefly: the export file (proof the app committed the insert
    # before writing it) can be visible here a beat before a fresh psql
    # connection sees the same row — seen in practice, cause unconfirmed.
    for i in 1 2 3 4 5; do
        syrinx_root_exists && break
        sleep 1
    done
    if ! syrinx_root_exists; then
        echo "❌ Root export wrote $export_file but users.id=1 is still missing" >&2
        return 1
    fi

    # Passphrase lives only next to the export file it unlocks (600, same
    # owner) — never printed to stdout, so it never lands in terminal
    # scrollback, shell history, or the setup/update log.
    passphrase_file="${export_file}.passphrase"
    printf '%s\n' "$passphrase" > "$passphrase_file"
    chown "$APP_USER:$APP_USER" "$passphrase_file" 2>/dev/null || true
    chmod 600 "$passphrase_file" 2>/dev/null || true

    # Marker is the fast-path signal syrinx_root_bootstrap_complete() trusts
    # without re-checking the passphrase file — must not be written until
    # the passphrase actually exists, or a later run's fast path skips
    # straight past a genuinely incomplete bootstrap.
    touch "$(syrinx_root_bootstrap_marker)"
    chown "$APP_USER:$APP_USER" "$(syrinx_root_bootstrap_marker)" 2>/dev/null || true
    chmod 600 "$(syrinx_root_bootstrap_marker)" 2>/dev/null || true

    echo "------------------------------------------------------------------"
    echo "✅ Root identity minted (users.id=1)"
    echo "📦 Export file:  $export_file"
    echo "🔐 Passphrase file: $passphrase_file"
    echo ""
    echo "   Read the passphrase with: sudo cat $passphrase_file"
    echo "   Import via SPA /import → \"I only have my keys\", then store the"
    echo "   .sxi.gpg somewhere safe. Losing it loses control of root."
    echo "   Temporary write access has been revoked from the systemd unit."
    echo "   ⚠️  Once you've copied both files off the server, delete"
    echo "   $passphrase_file — it is the only copy of this passphrase and"
    echo "   is not needed for the server to keep running."
    echo "------------------------------------------------------------------"
}
