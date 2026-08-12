#!/bin/bash
# ==============================================================================
# Telemetry host update: refresh OpenObserve + OTEL Collector binaries
# Re-detects architecture on every run (seamless Pi migration armhf → arm64).
# Does NOT reconfigure Cloudflare — the tunnel is unrelated to stack updates.
# setup.sh skips Cloudflare too when https://$TELEMETRY_DOMAIN already responds
# (use FORCE_CF=1 there to force a tunnel rewrite).
# ==============================================================================

set -euo pipefail

if [ "$EUID" -ne 0 ]; then
    echo "❌ Error: run as root (sudo ./update.sh)"
    exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=common.sh
. "$SCRIPT_DIR/common.sh"

SHOW_PASSWORD="${SHOW_PASSWORD:-0}"

TELEMETRY_ENV="$SCRIPT_DIR/telemetry.env"

if [ ! -f "$TELEMETRY_ENV" ]; then
    echo "❌ Error: missing $TELEMETRY_ENV — run setup first."
    exit 1
fi

# shellcheck disable=SC1090
set -a
# shellcheck source=/dev/null
. "$TELEMETRY_ENV"
set +a

# Always ingest into the built-in default org.
ZO_ORG="default"
ZO_STREAM="${ZO_STREAM:-syrinx}"

echo "================================================================"
echo "🔄 Updating telemetry stack (OpenObserve + OTEL Collector)"
echo "================================================================"

telemetry_install_stack

if [ -n "${ZO_ROOT_USER_EMAIL:-}" ] && [ -n "${ZO_ROOT_USER_PASSWORD:-}" ]; then
    AUTH_HEADER="$(telemetry_auth_header "$ZO_ROOT_USER_EMAIL" "$ZO_ROOT_USER_PASSWORD")"
    telemetry_write_otel_config "$AUTH_HEADER"
fi

telemetry_save_platform_state "$TELEMETRY_ENV"
telemetry_configure_firewall
telemetry_restart_services
telemetry_verify

if [ -n "${ZO_ROOT_USER_EMAIL:-}" ] && [ -n "${ZO_ROOT_USER_PASSWORD:-}" ]; then
    telemetry_ensure_host_metrics_dashboard "$ZO_ROOT_USER_EMAIL" "$ZO_ROOT_USER_PASSWORD" "$ZO_ORG" || true
    telemetry_ensure_syrinx_dashboard "$ZO_ROOT_USER_EMAIL" "$ZO_ROOT_USER_PASSWORD" "$ZO_ORG" || true
fi

# Informational only — update never touches cloudflared.
if [ -n "${TELEMETRY_DOMAIN:-}" ]; then
    if systemctl is-active --quiet cloudflared 2>/dev/null; then
        code="$(curl -sS -m 8 -o /dev/null -w '%{http_code}' "https://${TELEMETRY_DOMAIN}/" 2>/dev/null || echo 000)"
        case "$code" in
            000) echo "⚠️  cloudflared is active but https://${TELEMETRY_DOMAIN} did not respond" ;;
            *)   echo "✅ Cloudflare tunnel looks up (https://${TELEMETRY_DOMAIN} → HTTP ${code})" ;;
        esac
    else
        echo "⚠️  cloudflared is not active — run sudo ./setup.sh if the public URL is down"
    fi
fi

echo "------------------------------------------------------------------"
echo "OpenObserve UI: http://${TELEMETRY_HOST}:5080"
if [ -n "${ZO_ROOT_USER_EMAIL:-}" ] && [ -n "${ZO_ROOT_USER_PASSWORD:-}" ]; then
    if [ "$SHOW_PASSWORD" = "1" ]; then
        echo "Login:          $ZO_ROOT_USER_EMAIL / $ZO_ROOT_USER_PASSWORD"
    else
        echo "Login:          $ZO_ROOT_USER_EMAIL / (hidden — rerun with SHOW_PASSWORD=1 to print it)"
    fi
    echo "  (saved in $TELEMETRY_ENV, mode 600)"
fi
if [ -n "${TELEMETRY_DOMAIN:-}" ]; then
    echo "Public UI:      https://${TELEMETRY_DOMAIN} (via Cloudflare Tunnel)"
fi
echo "📄 Env file: $TELEMETRY_ENV"
echo "🚀 Telemetry update complete."
