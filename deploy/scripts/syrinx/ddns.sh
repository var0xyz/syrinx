#!/bin/bash
# Dynamic DNS updater for EDGE_MODE=mtls. Cloudflare Tunnel mode never
# needs this — DNS there points at Cloudflare's edge, not the Pi, and the
# tunnel is an outbound connection immune to the Pi's public IP changing.
# Direct nginx mode has no such shield: $APP_DOMAIN's A/AAAA record must
# track the Pi's current public IP, which residential ISPs frequently
# rotate. This installs a systemd timer that checks every 5 minutes and
# updates the record via the Cloudflare DNS API when it drifts.
#
# Usage (as root):
#   source ddns.sh
#   ddns_install <domain> <cf_dns_token> <zone_id>
#   ddns_remove
#
# Requires before sourcing: APP_NAME

DDNS_CONFIG_DIR="/etc/${APP_NAME:-app}/ddns"
DDNS_CONFIG="$DDNS_CONFIG_DIR/ddns.env"
DDNS_STATE="$DDNS_CONFIG_DIR/last-ip"
DDNS_SCRIPT="/usr/local/bin/${APP_NAME:-app}-ddns-update"
DDNS_SERVICE="${APP_NAME:-app}-ddns.service"
DDNS_TIMER="${APP_NAME:-app}-ddns.timer"

ddns_write_config() {
    local domain="$1" cf_token="$2" zone_id="$3"
    mkdir -p "$DDNS_CONFIG_DIR"
    chmod 700 "$DDNS_CONFIG_DIR"
    umask 077
    cat > "$DDNS_CONFIG" <<EOF
DOMAIN=${domain}
CF_DNS_TOKEN=${cf_token}
CF_ZONE_ID=${zone_id}
EOF
    chmod 600 "$DDNS_CONFIG"
}

# The update script itself: detects the current public IP, compares against
# the cached last-known value, and PATCHes the Cloudflare DNS record only
# when it has actually changed — avoids hammering the API every 5 minutes
# for no reason. Failures here must never propagate as a hard error to the
# systemd unit's caller (setup.sh/update.sh) — only the timer sees them.
ddns_write_update_script() {
    cat > "$DDNS_SCRIPT" <<'SCRIPT'
#!/bin/bash
set -u

CONFIG="__DDNS_CONFIG__"
STATE="__DDNS_STATE__"

[ -f "$CONFIG" ] || { echo "❌ Missing $CONFIG"; exit 1; }
# shellcheck disable=SC1090
set -a
. "$CONFIG"
set +a

[ -n "${DOMAIN:-}" ] && [ -n "${CF_DNS_TOKEN:-}" ] && [ -n "${CF_ZONE_ID:-}" ] \
    || { echo "❌ DOMAIN, CF_DNS_TOKEN, CF_ZONE_ID must be set in $CONFIG"; exit 1; }

CURRENT_IP="$(curl -fsS --max-time 8 https://1.1.1.1/cdn-cgi/trace 2>/dev/null | sed -n 's/^ip=//p')"
if [ -z "$CURRENT_IP" ]; then
    echo "⚠️  Could not determine current public IP — will retry next tick"
    exit 0
fi

LAST_IP=""
[ -f "$STATE" ] && LAST_IP="$(cat "$STATE" 2>/dev/null || true)"

if [ "$CURRENT_IP" = "$LAST_IP" ]; then
    exit 0
fi

echo "🔹 Public IP changed: ${LAST_IP:-<none>} → ${CURRENT_IP} — updating ${DOMAIN}"

RECORD_ID="$(curl -fsS --max-time 10 \
    -H "Authorization: Bearer ${CF_DNS_TOKEN}" \
    -H "Content-Type: application/json" \
    "https://api.cloudflare.com/client/v4/zones/${CF_ZONE_ID}/dns_records?type=A&name=${DOMAIN}" \
    | sed -n 's/.*"id":"\([a-f0-9]*\)".*/\1/p' | head -n1)"

if [ -z "$RECORD_ID" ]; then
    echo "⚠️  No existing A record found for ${DOMAIN} — creating one"
    RESPONSE="$(curl -fsS --max-time 10 -X POST \
        -H "Authorization: Bearer ${CF_DNS_TOKEN}" \
        -H "Content-Type: application/json" \
        "https://api.cloudflare.com/client/v4/zones/${CF_ZONE_ID}/dns_records" \
        --data "{\"type\":\"A\",\"name\":\"${DOMAIN}\",\"content\":\"${CURRENT_IP}\",\"ttl\":120,\"proxied\":false}")"
else
    RESPONSE="$(curl -fsS --max-time 10 -X PATCH \
        -H "Authorization: Bearer ${CF_DNS_TOKEN}" \
        -H "Content-Type: application/json" \
        "https://api.cloudflare.com/client/v4/zones/${CF_ZONE_ID}/dns_records/${RECORD_ID}" \
        --data "{\"content\":\"${CURRENT_IP}\"}")"
fi

case "$RESPONSE" in
    *'"success":true'*)
        printf '%s' "$CURRENT_IP" > "$STATE"
        echo "✅ DNS updated: ${DOMAIN} → ${CURRENT_IP}"
        ;;
    *)
        echo "⚠️  Cloudflare API call failed — will retry next tick: $RESPONSE"
        exit 0
        ;;
esac
SCRIPT
    sed -i "s|__DDNS_CONFIG__|$DDNS_CONFIG|; s|__DDNS_STATE__|$DDNS_STATE|" "$DDNS_SCRIPT"
    chmod 700 "$DDNS_SCRIPT"
}

ddns_write_units() {
    cat > "/etc/systemd/system/$DDNS_SERVICE" <<EOF
[Unit]
Description=Update ${APP_NAME:-app} DNS record when the public IP changes

[Service]
Type=oneshot
ExecStart=${DDNS_SCRIPT}
EOF

    cat > "/etc/systemd/system/$DDNS_TIMER" <<EOF
[Unit]
Description=Periodic public-IP check for ${APP_NAME:-app} (EDGE_MODE=mtls)

[Timer]
OnBootSec=1min
OnUnitActiveSec=5min
Persistent=true

[Install]
WantedBy=timers.target
EOF
}

# Top-level entry point. Idempotent: safe to call on every setup.sh/update.sh
# run — rewrites config/units (cheap, matches otel-agent.sh's approach) and
# (re)starts the timer.
ddns_install() {
    local domain="$1" cf_token="$2" zone_id="$3"
    if [ -z "$domain" ] || [ -z "$cf_token" ] || [ -z "$zone_id" ]; then
        echo "❌ ddns_install requires <domain> <cf_dns_token> <zone_id>" >&2
        return 1
    fi

    ddns_write_config "$domain" "$cf_token" "$zone_id"
    ddns_write_update_script
    ddns_write_units
    systemctl daemon-reload
    systemctl enable --now "$DDNS_TIMER" >/dev/null 2>&1

    echo "🔹 Running initial DNS check..."
    if "$DDNS_SCRIPT"; then
        echo "✅ DDNS updater installed and running (checks every 5 min)"
    else
        echo "⚠️  DDNS updater installed but the initial check failed — see above; it will retry on its own"
    fi
}

ddns_last_known_ip() {
    [ -f "$DDNS_STATE" ] && cat "$DDNS_STATE" 2>/dev/null || echo "unknown"
}

ddns_remove() {
    systemctl disable --now "$DDNS_TIMER" 2>/dev/null || true
    systemctl stop "$DDNS_SERVICE" 2>/dev/null || true
    rm -f "/etc/systemd/system/$DDNS_TIMER" "/etc/systemd/system/$DDNS_SERVICE" "$DDNS_SCRIPT"
    rm -rf "$DDNS_CONFIG_DIR"
    systemctl daemon-reload 2>/dev/null || true
    echo "✅ DDNS updater removed"
}
