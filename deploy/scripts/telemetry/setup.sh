#!/bin/bash
# ==============================================================================
# Telemetry host setup: OpenObserve + OTEL Collector (OTLP bridge on 4317/4318)
# Run on the dedicated telemetry Pi (not the app host).
# https://openobserve.ai/docs/getting-started/
# ==============================================================================

set -euo pipefail

if [ "$EUID" -ne 0 ]; then
    echo "❌ Error: run as root (sudo ./setup.sh)"
    exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=common.sh
. "$SCRIPT_DIR/common.sh"

SHOW_PASSWORD="${SHOW_PASSWORD:-0}"

TELEMETRY_ENV="$SCRIPT_DIR/telemetry.env"
DEFAULT_HOSTNAME="telemetry"

if [ -f "$TELEMETRY_ENV" ]; then
    # shellcheck disable=SC1090
    set -a
    # shellcheck source=/dev/null
    . "$TELEMETRY_ENV"
    set +a
    echo "📂 Loaded saved settings from $TELEMETRY_ENV (press Enter to keep a value)"
fi

echo "================================================================"
echo "📡 OpenObserve telemetry host setup"
echo "================================================================"
echo "📝 Configure telemetry parameters below:"

CURRENT_HOSTNAME="$(hostname -s 2>/dev/null || hostname)"
read -r -p "🔹 Telemetry hostname [${TELEMETRY_HOST:-$DEFAULT_HOSTNAME}] (used by app hosts): " HOST_INPUT
TELEMETRY_HOST="${HOST_INPUT:-${TELEMETRY_HOST:-$DEFAULT_HOSTNAME}}"

if [ "$TELEMETRY_HOST" != "$CURRENT_HOSTNAME" ]; then
    read -r -p "🔹 Set system hostname to '$TELEMETRY_HOST'? [Y/n]: " SET_HOSTNAME
    if [ "${SET_HOSTNAME:-Y}" != "n" ] && [ "${SET_HOSTNAME:-Y}" != "N" ]; then
        hostnamectl set-hostname "$TELEMETRY_HOST"
        echo "✅ Hostname set to $TELEMETRY_HOST"
    fi
fi

if [ -n "${ZO_ROOT_USER_EMAIL:-}" ]; then
    read -r -p "🔹 OpenObserve admin email [${ZO_ROOT_USER_EMAIL}]: " EMAIL_INPUT
else
    read -r -p "🔹 OpenObserve admin email [admin@telemetry.local]: " EMAIL_INPUT
fi
ZO_ROOT_USER_EMAIL="${EMAIL_INPUT:-${ZO_ROOT_USER_EMAIL:-admin@telemetry.local}}"

if [ -n "${ZO_ROOT_USER_PASSWORD:-}" ]; then
    read -r -s -p "🔹 OpenObserve admin password [saved — Enter to keep]: " PASS_INPUT
    echo ""
    ZO_ROOT_USER_PASSWORD="${PASS_INPUT:-$ZO_ROOT_USER_PASSWORD}"
else
    read -r -s -p "🔹 OpenObserve admin password (leave empty to generate): " PASS_INPUT
    echo ""
    if [ -n "$PASS_INPUT" ]; then
        ZO_ROOT_USER_PASSWORD="$PASS_INPUT"
    else
        ZO_ROOT_USER_PASSWORD="$(telemetry_generate_secret)"
        echo "🔹 Generated OpenObserve admin password"
    fi
fi

# Always ingest into the built-in default org (custom orgs are optional / unused).
ZO_ORG="default"
ZO_STREAM="${ZO_STREAM:-syrinx}"
echo "🔹 OpenObserve organization: $ZO_ORG (stream: $ZO_STREAM)"

# Cloudflare Tunnel (optional) — exposes the OpenObserve UI at a public
# hostname with no inbound port opened. Leave empty to keep this host LAN-only.
if [ -n "${TELEMETRY_DOMAIN:-}" ]; then
    read -r -p "🔹 Public domain for remote access via Cloudflare Tunnel (empty keeps current, 'off' disables) [${TELEMETRY_DOMAIN}]: " DOMAIN_INPUT
else
    read -r -p "🔹 Public domain for remote access via Cloudflare Tunnel (leave empty to disable): " DOMAIN_INPUT
fi
case "$DOMAIN_INPUT" in
    off|OFF|none|NONE|-) TELEMETRY_DOMAIN="" ;;
    "") TELEMETRY_DOMAIN="${TELEMETRY_DOMAIN:-}" ;;
    *) TELEMETRY_DOMAIN="$DOMAIN_INPUT" ;;
esac

if [ -n "$TELEMETRY_DOMAIN" ]; then
    if [ -n "${CF_TOKEN:-}" ]; then
        read -r -p "🔹 Cloudflare Zero Trust Tunnel Token [saved]: " CF_TOKEN_INPUT
    else
        read -r -p "🔹 Cloudflare Zero Trust Tunnel Token: " CF_TOKEN_INPUT
    fi
    CF_TOKEN="${CF_TOKEN_INPUT:-${CF_TOKEN:-}}"
    if [ -z "$CF_TOKEN" ]; then
        echo "❌ Error: a Cloudflare Tunnel Token is required when a public domain is set."
        exit 1
    fi
    echo "☁️  Remote access: https://${TELEMETRY_DOMAIN} → 127.0.0.1:5080"
else
    CF_TOKEN=""
    echo "☁️  Remote access: disabled (LAN-only, http://<host>:5080)"
fi

# Which private networks may reach OTLP (:4317/:4318) and the OpenObserve UI (:5080).
# Default covers all RFC1918 ranges so the script works on 10.x, 172.16–31.x, and 192.168.x.
DEFAULT_LAN_CIDRS="192.168.0.0/16 10.0.0.0/8 172.16.0.0/12"
lan_prompt_default="${TELEMETRY_LAN_CIDRS:-$DEFAULT_LAN_CIDRS}"
read -r -p "🔹 LAN CIDRs allowed to OTLP/UI (space-separated) [${lan_prompt_default}]: " LAN_CIDRS_INPUT
TELEMETRY_LAN_CIDRS="${LAN_CIDRS_INPUT:-$lan_prompt_default}"
echo "🔹 Firewall will allow OTLP/UI from: $TELEMETRY_LAN_CIDRS"

telemetry_persist_env "$TELEMETRY_ENV"
echo "💾 Saved settings to $TELEMETRY_ENV"

telemetry_assert_supported_platform

echo -e "\n⬆️  Updating system packages..."
DEBIAN_FRONTEND=noninteractive apt-get update
DEBIAN_FRONTEND=noninteractive apt-get upgrade -y

echo -e "\n⚙️  Installing dependencies..."
apt-get install -y curl ufw

AUTH_HEADER="$(telemetry_auth_header "$ZO_ROOT_USER_EMAIL" "$ZO_ROOT_USER_PASSWORD")"
telemetry_write_o2_env "$ZO_ROOT_USER_EMAIL" "$ZO_ROOT_USER_PASSWORD"
telemetry_write_otel_config "$AUTH_HEADER"

echo -e "\n📦 Installing OpenObserve and OTEL Collector..."
if telemetry_is_raspberry_pi; then
    echo "ℹ️  Raspberry Pi: prefers a Mac cross-compiled binary in $O2_PI_BUILD_DIR."
    echo "   Deploy with: ./telemetry/deploy-openobserve-pi.sh"
    echo "   On-Pi source build only when O2_ALLOW_SOURCE_BUILD=1 (or O2_FORCE_REBUILD=1)."
fi
telemetry_install_stack
telemetry_configure_firewall
telemetry_enable_services

telemetry_persist_env "$TELEMETRY_ENV"
telemetry_save_platform_state "$TELEMETRY_ENV"
chmod 600 "$TELEMETRY_ENV"

TELEMETRY_IP="$(hostname -I 2>/dev/null | awk '{print $1}')"

echo ""
telemetry_verify

echo -e "\n📊 Provisioning dashboards..."
telemetry_ensure_host_metrics_dashboard "$ZO_ROOT_USER_EMAIL" "$ZO_ROOT_USER_PASSWORD" "$ZO_ORG" || true
telemetry_ensure_syrinx_dashboard "$ZO_ROOT_USER_EMAIL" "$ZO_ROOT_USER_PASSWORD" "$ZO_ORG" || true

echo -e "\n☁️  Checking Cloudflare tunnel..."
FORCE_CF="${FORCE_CF:-0}"
if [ -n "$TELEMETRY_DOMAIN" ]; then
    telemetry_setup_cloudflare_tunnel "$TELEMETRY_DOMAIN" "$CF_TOKEN" "$FORCE_CF"
else
    telemetry_disable_cloudflare_tunnel
    echo "⚪ Cloudflare tunnel: disabled — metrics stay LAN-only (set a public domain above to enable)"
fi

echo "------------------------------------------------------------------"
echo "Telemetry host: $TELEMETRY_HOST (${TELEMETRY_IP:-unknown IP})"
echo "Install: $(telemetry_platform_info)"
echo "OpenObserve UI: http://${TELEMETRY_HOST}:5080"
if [ "$SHOW_PASSWORD" = "1" ]; then
    echo "Login:          $ZO_ROOT_USER_EMAIL / $ZO_ROOT_USER_PASSWORD"
else
    echo "Login:          $ZO_ROOT_USER_EMAIL / (hidden — rerun with SHOW_PASSWORD=1 to print it)"
fi
echo "  (saved in $TELEMETRY_ENV, mode 600 — rerun setup.sh to view/change it later)"
echo "OTLP endpoint:  ${TELEMETRY_HOST}:4317 (gRPC) / :4318 (HTTP)"
if [ -n "$TELEMETRY_DOMAIN" ]; then
    echo "Public UI:      https://${TELEMETRY_DOMAIN} (via Cloudflare Tunnel)"
    echo "  ⚠️  This exposes the OpenObserve login page to the internet (still gated by"
    echo "     the admin email/password). Strongly recommend adding a Cloudflare Access"
    echo "     policy in the Zero Trust dashboard to restrict who can reach it."
fi
echo ""
echo "📄 Env file: $TELEMETRY_ENV"
echo "On each app host, ensure the hostname resolves. If DNS/mDNS is unavailable:"
echo "  echo '${TELEMETRY_IP:-<telemetry-ip>} ${TELEMETRY_HOST}' | sudo tee -a /etc/hosts"
echo ""
echo "Then run scripts/setup.sh on the app host and set TELEMETRY_HOST=$TELEMETRY_HOST"
echo "🚀 Telemetry setup complete."
