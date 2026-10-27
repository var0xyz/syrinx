#!/bin/bash

# ==============================================================================
# UNIFIED SECURE DEPLOYMENT ENGINE (Idempotent Architecture Blueprint)
# Target Environment: Raspberry Pi 5 / Debian 13 (Trixie)
#
# Usage: ./setup.sh [--branch <name>] [--repo <url>]
#   --branch <name>   Clone this branch instead of the repo's default branch.
#   --repo <url>      Clone this repo (e.g. a fork) instead of the official one.
# ==============================================================================

# Exit immediately if a command exits with a non-zero status
set -e

BRANCH=""
REPO_OVERRIDE=""
while [ $# -gt 0 ]; do
    case "$1" in
        --branch)
            [ $# -ge 2 ] || { echo "❌ Error: --branch requires a value"; exit 1; }
            BRANCH="$2"
            shift 2
            ;;
        --repo)
            [ $# -ge 2 ] || { echo "❌ Error: --repo requires a value"; exit 1; }
            REPO_OVERRIDE="$2"
            shift 2
            ;;
        *)
            echo "❌ Error: unknown argument: $1"
            exit 1
            ;;
    esac
done

# clear fails (and, under set -e, kills the whole script) when TERM is
# unset/unknown — happens under sudo's use_pty with no real terminal, e.g.
# piped/non-interactive runs. Cosmetic only, so never fatal.
clear || true
echo "================================================================"
echo "🛡️  Go-Postgres-Vite Secure Fullstack Installer for Raspberry Pi 5"
echo "================================================================"

# Verify Root Execution Privilege Escalation
if [ "$EUID" -ne 0 ]; then
    echo "❌ Error: Please run this installation engine as root (sudo ./setup.sh)"
    exit 1
fi

# ==============================================================================
# INTERACTIVE VARIABLE RECOGNITION (Prompts)
# ==============================================================================
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SETUP_ENV="$SCRIPT_DIR/setup.env"
# shellcheck source=otel-agent.sh
. "$SCRIPT_DIR/otel-agent.sh"
# shellcheck source=root-bootstrap.sh
. "$SCRIPT_DIR/root-bootstrap.sh"
# shellcheck source=mtls.sh
. "$SCRIPT_DIR/mtls.sh"
# shellcheck source=ddns.sh
. "$SCRIPT_DIR/ddns.sh"
# shellcheck source=node.sh
. "$SCRIPT_DIR/node.sh"

if [ -f "$SETUP_ENV" ]; then
    # shellcheck disable=SC1090
    set -a
    # shellcheck source=/dev/null
    . "$SETUP_ENV"
    set +a
    echo "📂 Loaded saved settings from $SETUP_ENV (press Enter to keep a value)"
fi

echo "📝 Please configure your application parameters below:"

read -p "🔹 Enter System Identifier/Application Name [${APP_NAME:-syrinx}]: " APP_NAME_INPUT
APP_NAME=$(echo "${APP_NAME_INPUT:-$APP_NAME}" | tr -d ' ' | tr 'A-Z' 'a-z')
APP_NAME="${APP_NAME:-syrinx}"

read -p "🔹 Enter your Public Registered Domain [${APP_DOMAIN:-}]: " APP_DOMAIN_INPUT
APP_DOMAIN="${APP_DOMAIN_INPUT:-$APP_DOMAIN}"

# Fixed to Syrinx's own layout — this script deploys Syrinx specifically, not
# an arbitrary app. The repo is the official one unless --repo names a fork.
APP_REPO="${REPO_OVERRIDE:-https://github.com/var0xyz/syrinx.git}"
BACKEND_PATH="src/backend"
FRONTEND_PATH="src/frontend"

# Public edge: how the outside world reaches this host.
#   cloudflare (default) — outbound-only Tunnel, zero inbound ports, works
#     behind any NAT, TLS terminated by Cloudflare.
#   mtls — nginx listens on 443 directly and hard-rejects any connection
#     without a client certificate signed by a locally-generated CA
#     (ssl_verify_client on) — enforced at the TLS layer, before the request
#     reaches any app code. Requires the operator to port-forward 443 and
#     accept a rotating-public-IP DDNS updater (see ddns.sh) if their ISP
#     doesn't give a static IP.
edge_prompt_default="${EDGE_MODE:-cloudflare}"
read -r -p "🔹 Public edge mode: cloudflare/mtls [${edge_prompt_default}] (cloudflare = Tunnel, NAT-friendly, no port-forwarding; mtls = nginx on 443 with required client certs, needs port-forwarding): " EDGE_MODE_INPUT
EDGE_MODE="${EDGE_MODE_INPUT:-$edge_prompt_default}"
case "$EDGE_MODE" in
    cloudflare|mtls) ;;
    *) echo "❌ Error: Public edge mode must be 'cloudflare' or 'mtls', got '$EDGE_MODE'"; exit 1 ;;
esac

if [ "$EDGE_MODE" = "cloudflare" ]; then
    if [ -n "$CF_TOKEN" ]; then
        read -p "🔹 Enter your Cloudflare Zero Trust Tunnel Token [saved]: " CF_TOKEN_INPUT
    else
        read -p "🔹 Enter your Cloudflare Zero Trust Tunnel Token: " CF_TOKEN_INPUT
    fi
    CF_TOKEN="${CF_TOKEN_INPUT:-$CF_TOKEN}"
else
    CF_TOKEN=""
    echo "🔹 mTLS edge selected — nginx will terminate TLS on 443 directly."
    if [ -n "${CF_DNS_TOKEN:-}" ]; then
        read -p "🔹 Enter your Cloudflare DNS API token (Zone:DNS:Edit) for dynamic-IP updates [saved]: " CF_DNS_TOKEN_INPUT
    else
        read -p "🔹 Enter your Cloudflare DNS API token (Zone:DNS:Edit) for dynamic-IP updates: " CF_DNS_TOKEN_INPUT
    fi
    CF_DNS_TOKEN="${CF_DNS_TOKEN_INPUT:-${CF_DNS_TOKEN:-}}"

    if [ -n "${CF_ZONE_ID:-}" ]; then
        read -p "🔹 Enter the Cloudflare Zone ID for the domain's DNS zone [saved]: " CF_ZONE_ID_INPUT
    else
        read -p "🔹 Enter the Cloudflare Zone ID for the domain's DNS zone: " CF_ZONE_ID_INPUT
    fi
    CF_ZONE_ID="${CF_ZONE_ID_INPUT:-${CF_ZONE_ID:-}}"

    if [ -z "$CF_DNS_TOKEN" ] || [ -z "$CF_ZONE_ID" ]; then
        echo "❌ Error: EDGE_MODE=mtls requires a Cloudflare DNS API token and Zone ID"
        echo "   so this host can keep \$APP_DOMAIN pointed at its current public IP"
        echo "   (residential ISPs frequently rotate it). See README.md for how to"
        echo "   create a Zone:DNS:Edit scoped token."
        exit 1
    fi
fi

# OTEL / OpenObserve collector on the LAN. Empty (or "off") disables export.
otel_prompt_default="${TELEMETRY_HOST:-}"
if [ -n "$otel_prompt_default" ]; then
    read -r -p "🔹 OTEL collector IP or hostname (empty keeps current, 'off' disables) [${otel_prompt_default}]: " TELEMETRY_HOST_INPUT
else
    read -r -p "🔹 OTEL collector IP or hostname (leave empty to disable): " TELEMETRY_HOST_INPUT
fi
case "${TELEMETRY_HOST_INPUT}" in
    off|OFF|none|NONE|-)
        TELEMETRY_HOST=""
        ;;
    "")
        # Keep prior value when re-running; stay disabled on first run.
        TELEMETRY_HOST="${TELEMETRY_HOST:-}"
        ;;
    *)
        TELEMETRY_HOST="$TELEMETRY_HOST_INPUT"
        ;;
esac

if [ -n "$TELEMETRY_HOST" ]; then
    if ! getent hosts "$TELEMETRY_HOST" >/dev/null 2>&1; then
        echo "⚠️  Warning: '$TELEMETRY_HOST' does not resolve on this host."
        echo "   Use a reachable LAN IP (e.g. 10.0.0.20), or add it to /etc/hosts."
        read -r -p "   Continue anyway? [y/N]: " TELEMETRY_CONTINUE
        if [ "${TELEMETRY_CONTINUE:-N}" != "y" ] && [ "${TELEMETRY_CONTINUE:-N}" != "Y" ]; then
            echo "❌ Aborted."
            exit 1
        fi
    fi
    echo "📡 Observability: OTLP → ${TELEMETRY_HOST}:4317 (gRPC) / :4318 (HTTP)"
else
    echo "📡 Observability: disabled (no OTEL collector)"
fi

if [ -z "$APP_DOMAIN" ]; then
    echo "❌ Error: APP_DOMAIN is required."
    exit 1
fi
if [ "$EDGE_MODE" = "cloudflare" ] && [ -z "$CF_TOKEN" ]; then
    echo "❌ Error: CF_TOKEN is required when EDGE_MODE=cloudflare."
    exit 1
fi

# Persist answers for the next run (mode 600 — contains CF_TOKEN/CF_DNS_TOKEN)
umask 077
{
    printf "APP_NAME=%q\n" "$APP_NAME"
    printf "APP_DOMAIN=%q\n" "$APP_DOMAIN"
    printf "EDGE_MODE=%q\n" "$EDGE_MODE"
    printf "CF_TOKEN=%q\n" "$CF_TOKEN"
    printf "CF_DNS_TOKEN=%q\n" "${CF_DNS_TOKEN:-}"
    printf "CF_ZONE_ID=%q\n" "${CF_ZONE_ID:-}"
    printf "TELEMETRY_HOST=%q\n" "$TELEMETRY_HOST"
} > "$SETUP_ENV"
chmod 600 "$SETUP_ENV"
echo "💾 Saved settings to $SETUP_ENV"

# Generate Derived Structural Variables
APP_USER="$APP_NAME"
ENV_DIR="/etc/$APP_NAME"
ENV_FILE="$ENV_DIR/app.env"
WWW_ROOT="/var/www/html/$APP_NAME"
BUILD_DIR="/tmp/build-$APP_NAME"

# Persistent, timestamped log — survives even when the run fails partway, so
# a silent partial deploy (build succeeds, restart never happens) is no
# longer invisible after the fact. Keeps last 20 logs.
# Root-only (700/600): the log can capture secrets a sub-command prints
# (e.g. the root-bootstrap import passphrase), so it must not be world-readable.
LOG_DIR="/var/log/$APP_NAME"
mkdir -p "$LOG_DIR"
chmod 700 "$LOG_DIR"
LOG_FILE="$LOG_DIR/setup-$(date +%Y%m%dT%H%M%S).log"
: > "$LOG_FILE"
chmod 600 "$LOG_FILE"
ln -sfn "$LOG_FILE" "$LOG_DIR/setup-latest.log"
find "$LOG_DIR" -maxdepth 1 -name 'setup-*.log' -printf '%T@ %p\n' 2>/dev/null \
    | sort -rn | tail -n +21 | cut -d' ' -f2- | xargs -r rm -f
exec > >(tee -a "$LOG_FILE") 2>&1
echo "📝 Logging this run to $LOG_FILE"

on_error() {
    echo "------------------------------------------------------------------"
    echo "❌ SETUP FAILED at line $1 — check the full log for the real error."
    echo "   Full log: $LOG_FILE"
    echo "------------------------------------------------------------------"
}
trap 'on_error $LINENO' ERR

echo -e "\n⚙️  Validating target dependency versions on Debian Trixie..."
# Host's apt-listchanges.conf uses frontend=pager, which DEBIAN_FRONTEND
# doesn't override — hangs a non-tty run on any package with changelogs.
export DEBIAN_FRONTEND=noninteractive
APT_LISTCHANGES_FRONTEND=none apt update
APT_LISTCHANGES_FRONTEND=none apt install -y curl git postgresql postgresql-contrib ufw wget build-essential nginx openssl
node_ensure

# Ensure local system firewall blocks edge attempts (Zero ports open externally
# in cloudflare mode; mtls mode needs 443 reachable for its direct edge).
ufw default deny incoming
ufw default allow outgoing
ufw allow 22/tcp comment 'SSH Secure Administration Management'
if [ "$EDGE_MODE" = "mtls" ]; then
    ufw allow 443/tcp comment 'nginx mTLS public edge'
    ufw allow 80/tcp comment 'HTTP->HTTPS redirect only'
else
    ufw delete allow 443/tcp >/dev/null 2>&1 || true
    ufw delete allow 80/tcp >/dev/null 2>&1 || true
fi
ufw --force enable

# ==============================================================================
# IDEMPOTENT ROLE-BASED DATABASE ISOLATION
# ==============================================================================
generate_secret() {
    openssl rand -base64 18 | tr -dc 'a-zA-Z0-9'
}

echo -e "\n🗄️  Provisioning PostgreSQL database nodes and security accounts..."

# Set custom passwords
DB_PASS=$(generate_secret)

# Verify local authentication policy uses SCRAM
PG_HBA_CONF=$(ls /etc/postgresql/*/main/pg_hba.conf | head -n 1)
if ! grep -q "scram-sha-256" "$PG_HBA_CONF"; then
    sed -i "s/host    all             all             127.0.0.1\/32            allow/host    all             all             127.0.0.1\/32            scram-sha-256/g" "$PG_HBA_CONF"
    systemctl restart postgresql
fi

# Execute database structure definitions cleanly
sudo -i -u postgres psql -tAc "SELECT 1 FROM pg_roles WHERE rolname='$APP_USER'" | grep -q 1 || \
    sudo -i -u postgres psql -c "CREATE USER $APP_USER WITH PASSWORD '$DB_PASS';"

sudo -i -u postgres psql -lqt | cut -d \| -f 1 | grep -qw "${APP_NAME}" || \
    sudo -i -u postgres psql -c "CREATE DATABASE ${APP_NAME} OWNER $APP_USER;"

# Revoke default public layout definitions
sudo -i -u postgres psql -d "${APP_NAME}" -c "REVOKE CREATE ON SCHEMA public FROM PUBLIC; GRANT CREATE ON SCHEMA public TO $APP_USER;"

# ==============================================================================
# LOW-PRIVILEGE DESCRIPTOR & PATH GENERATION
# ==============================================================================
if ! id -u "$APP_USER" >/dev/null 2>&1; then
    useradd -r -s /bin/false "$APP_USER"
fi

mkdir -p "$ENV_DIR" "$WWW_ROOT" "$BUILD_DIR"

# Syrinx requires SERVER_NAME, DB_SSLMODE (not SSL_MODE), and a non-interactive
# SERVER_KEY_PASSPHRASE (systemd has no TTY / usable OS keychain).
ensure_env_kv() {
    local key="$1" value="$2"
    if grep -q "^${key}=" "$ENV_FILE" 2>/dev/null; then
        sed -i "s|^${key}=.*|${key}=${value}|" "$ENV_FILE"
    else
        echo "${key}=${value}" >> "$ENV_FILE"
    fi
}

remove_env_kv() {
    local key="$1"
    if [ -f "$ENV_FILE" ]; then
        sed -i "/^${key}=/d" "$ENV_FILE"
    fi
}

# Wire Syrinx → OTEL collector (OpenObserve on the telemetry Pi), or strip keys.
# Also install a journald→OTLP shipper — the app logs to journald only today.
wire_observability_env() {
    if [ -n "$TELEMETRY_HOST" ]; then
        # Syrinx sends traces (gRPC) and app metrics (HTTP) to the local agent;
        # otelcol-agent forwards everything to the telemetry Pi.
        ensure_env_kv "OTEL_COLLECTOR_HOST" "127.0.0.1"
        ensure_env_kv "OTEL_COLLECTOR_PORT" "4317"
        otel_agent_install "$TELEMETRY_HOST" "$APP_NAME"
    else
        remove_env_kv "OTEL_COLLECTOR_HOST"
        remove_env_kv "OTEL_COLLECTOR_PORT"
        otel_agent_remove
    fi
}

if [ ! -f "$ENV_FILE" ]; then
    SERVER_KEY_PASSPHRASE=$(generate_secret)
    cat <<EOF > "$ENV_FILE"
DB_HOST=127.0.0.1
DB_PORT=5432
DB_USER=$APP_USER
DB_PASSWORD=$DB_PASS
DB_NAME=$APP_NAME
DB_SSLMODE=disable
PORT=8080
SERVER_NAME=$APP_NAME
ALLOWED_ORIGIN=https://$APP_DOMAIN
API_BASE_URL=https://$APP_DOMAIN
SERVER_KEY_PASSPHRASE=$SERVER_KEY_PASSPHRASE
SIGNUP_MODE=invite
MAX_INVITES_PER_USER=3
CLEAR_PRESENCE_ON_BOOT=true
REALTIME_BUS_ENABLED=false
EOF
    wire_observability_env
    chown root:$APP_USER "$ENV_FILE"
    chmod 640 "$ENV_FILE"

    echo -e "\n✏️  Opening $ENV_FILE in vi — save and quit (:wq) to continue setup..."
    vi "$ENV_FILE"
else
    # Keep DB secrets; repair keys the app actually reads
    ensure_env_kv "DB_SSLMODE" "disable"
    ensure_env_kv "PORT" "8080"
    ensure_env_kv "SERVER_NAME" "$APP_NAME"
    ensure_env_kv "ALLOWED_ORIGIN" "https://$APP_DOMAIN"
    ensure_env_kv "API_BASE_URL" "https://$APP_DOMAIN"
    # Single replica: clear presence at boot, no cross-replica bus.
    ensure_env_kv "CLEAR_PRESENCE_ON_BOOT" "true"
    ensure_env_kv "REALTIME_BUS_ENABLED" "false"
    wire_observability_env
    if ! grep -q '^SERVER_KEY_PASSPHRASE=.\+' "$ENV_FILE"; then
        ensure_env_kv "SERVER_KEY_PASSPHRASE" "$(generate_secret)"
    fi
    # Drop legacy misspelled key if present
    sed -i '/^SSL_MODE=/d' "$ENV_FILE"
fi

# ==============================================================================
# REPO RETRIEVAL, COMPILATION & PLACEMENT
# ==============================================================================
if [ -n "$BRANCH" ]; then
    echo -e "\n📦 Cloning monorepo into build workspace (branch: $BRANCH)..."
else
    echo -e "\n📦 Cloning monorepo into build workspace..."
fi
rm -rf "$BUILD_DIR/src"
if [ -n "$BRANCH" ]; then
    git clone --depth 1 --branch "$BRANCH" "$APP_REPO" "$BUILD_DIR/src"
else
    git clone --depth 1 "$APP_REPO" "$BUILD_DIR/src"
fi
GIT_COMMIT="$(git -C "$BUILD_DIR/src" rev-parse HEAD)"
export GIT_COMMIT
echo "    Commit: $GIT_COMMIT"

# Ensure local Go compilation libraries are ready (Pi 5 / Debian = linux-arm64)
if ! command -v go &> /dev/null && [ ! -x /usr/local/go/bin/go ]; then
    GO_VERSION="1.26.5"
    ARCH="$(dpkg --print-architecture)"
    case "$ARCH" in
        arm64|amd64) ;;
        *) echo "❌ Unsupported architecture for Go install: $ARCH"; exit 1 ;;
    esac
    echo "⬇️  Installing Go ${GO_VERSION} for linux-${ARCH}..."
    wget -q "https://go.dev/dl/go${GO_VERSION}.linux-${ARCH}.tar.gz" -O /tmp/go.tar.gz
    rm -rf /usr/local/go && tar -C /usr/local -xzf /tmp/go.tar.gz
    rm -f /tmp/go.tar.gz
fi
export PATH="/usr/local/go/bin:${PATH}"

echo -e "\n💻 Building production Go Application binary artifacts (staged — not installed yet)..."
cd "$BUILD_DIR/src/$BACKEND_PATH"
CGO_ENABLED=0 go build -ldflags="-w -s" -o "$BUILD_DIR/$APP_NAME" .

echo -e "\n💻 Building ripples-cleanup cron job (staged — not installed yet)..."
CGO_ENABLED=0 go build -tags ripplescleanup -ldflags="-w -s" -o "$BUILD_DIR/$APP_NAME-ripples-cleanup" .

echo -e "\n💻 Building challenges-cleanup cron job (staged — not installed yet)..."
CGO_ENABLED=0 go build -tags challengescleanup -ldflags="-w -s" -o "$BUILD_DIR/$APP_NAME-challenges-cleanup" .

echo -e "\n💻 Building the operator CLI (staged — not installed yet)..."
CGO_ENABLED=0 go build -tags ops -ldflags="-w -s" -o "$BUILD_DIR/$APP_NAME-ops" .

echo -e "\n⚛️  Assembling and transpiling Vite Frontend SPA assets (staged — not published yet)..."
cd "$BUILD_DIR/src/$FRONTEND_PATH"

# Build static components (.svelte-kit/ is generated here — not at monorepo root)
npm install
# Best-effort only: update-browserslist-db has been flaky (npm "$baseline-browser-mapping"
# resolution bug) and is not required for a production SPA build.
npx --yes update-browserslist-db@latest \
    || echo "⚠️  update-browserslist-db failed — continuing with lockfile caniuse data"
npx svelte-kit sync
npm run build

# Both builds succeeded — everything from here on only touches the live
# system, so a build failure above can never leave a half-upgraded install:
# either both the binary and SPA ship together, or neither does.
echo -e "\n🚀 Both builds succeeded — installing atomically..."
install -o "$APP_USER" -g "$APP_USER" -m 500 "$BUILD_DIR/$APP_NAME" "/usr/local/bin/$APP_NAME"
install -o "$APP_USER" -g "$APP_USER" -m 500 "$BUILD_DIR/$APP_NAME-ripples-cleanup" "/usr/local/bin/$APP_NAME-ripples-cleanup"
install -o "$APP_USER" -g "$APP_USER" -m 500 "$BUILD_DIR/$APP_NAME-challenges-cleanup" "/usr/local/bin/$APP_NAME-challenges-cleanup"
# Operator CLI (export-identity, rotate-key, ...): run as root, never by the service.
install -o root -g root -m 500 "$BUILD_DIR/$APP_NAME-ops" "/usr/local/bin/$APP_NAME-ops"

# Deletes expired ripple threads every hour. Own log dir under LOG_DIR: the
# cron job runs as $APP_USER, not root, and needs somewhere it can
# actually write. LOG_DIR's own setup/update logs stay unreadable to
# $APP_USER (700 blocks read+write+traversal for non-owners) — but mode
# 700 on the PARENT also blocks a non-owner from merely passing through
# it to reach a subdirectory they DO own, even one they own outright.
# 711 (execute-only for group/other) fixes that: $APP_USER can traverse
# into LOG_DIR/jobs, but still can't list or read anything living
# directly in LOG_DIR itself (setup-*.log/update-*.log stay root:root 600).
chmod 711 "$LOG_DIR"
JOB_LOG_DIR="$LOG_DIR/jobs"
mkdir -p "$JOB_LOG_DIR"
chown "$APP_USER:$APP_USER" "$JOB_LOG_DIR"
chmod 700 "$JOB_LOG_DIR"

# Placeholders (@APP_USER@ etc.) come from deploy/jobs/ripples-cleanup.cron in the repo.
sed -e "s|@APP_USER@|$APP_USER|g" -e "s|@ENV_FILE@|$ENV_FILE|g" -e "s|@APP_NAME@|$APP_NAME|g" \
    -e "s|@JOB_LOG_DIR@|$JOB_LOG_DIR|g" \
    "$BUILD_DIR/src/deploy/jobs/ripples-cleanup.cron" > "/etc/cron.d/$APP_NAME-ripples-cleanup"
chmod 644 "/etc/cron.d/$APP_NAME-ripples-cleanup"
sed -e "s|@APP_USER@|$APP_USER|g" -e "s|@ENV_FILE@|$ENV_FILE|g" -e "s|@APP_NAME@|$APP_NAME|g" \
    -e "s|@JOB_LOG_DIR@|$JOB_LOG_DIR|g" \
    "$BUILD_DIR/src/deploy/jobs/challenges-cleanup.cron" > "/etc/cron.d/$APP_NAME-challenges-cleanup"
chmod 644 "/etc/cron.d/$APP_NAME-challenges-cleanup"

# @sveltejs/adapter-static writes to "build" (not Vite's default "dist").
# Ship into a timestamped release dir and point the `build` symlink at it —
# same atomic-swap scheme update.sh uses, so the very first install already
# has old-release retention in place instead of a plain directory a later
# update would need to migrate away from.
RELEASES_DIR="$WWW_ROOT/releases"
RELEASE_DIR="$RELEASES_DIR/$(date +%Y%m%dT%H%M%S)-${GIT_COMMIT:0:12}"
mkdir -p "$RELEASES_DIR"
# nginx (www-data) must traverse these; setup.sh's umask 077 would leave them 700.
chmod 755 "$WWW_ROOT" "$RELEASES_DIR"
cp -r build "$RELEASE_DIR"
chown -R root:www-data "$RELEASE_DIR"
find "$RELEASE_DIR" -type d -exec chmod 755 {} \;
find "$RELEASE_DIR" -type f -exec chmod 644 {} \;
# ln -sfn silently nests inside an existing plain directory instead of
# replacing it — matters if setup.sh is re-run against a host that was set
# up before this script adopted the symlink scheme.
if [ -d "$WWW_ROOT/build" ] && [ ! -L "$WWW_ROOT/build" ]; then
    rm -rf "$WWW_ROOT/build"
fi
ln -sfn "$RELEASE_DIR" "$WWW_ROOT/build"

# ==============================================================================
# LOCAL EDGE: nginx serves SPA + proxies /api and /ws to Go
# cloudflare mode: Tunnel Service URL → http://127.0.0.1:8081
# mtls mode: nginx itself is the public edge, listening on 443 with a
#   required client certificate (ssl_verify_client on) — see mtls.sh
# ==============================================================================
echo -e "\n🌐 Configuring nginx (SPA + API proxy, edge mode: $EDGE_MODE)..."
STATIC_PORT=8081

# Visitors' IPs never reach the app: nothing forwards them, and headers a
# client or cloudflared adds are cleared (an empty value drops the header).
NO_CLIENT_IP='        proxy_set_header X-Real-IP "";
        proxy_set_header X-Forwarded-For "";
        proxy_set_header Forwarded "";
        proxy_set_header Cf-Connecting-Ip "";
        proxy_set_header True-Client-IP "";
        proxy_set_header Cf-Ipcountry "";'

# Shared location blocks — only the listen/TLS preamble differs by edge mode.
NGINX_LOCATIONS=$(cat <<EOF
    root $WWW_ROOT/build;
    index index.html;

    # No access log (it would hold IPs and user agents); errors only at crit.
    access_log off;
    error_log /var/log/nginx/error.log crit;

    # Backend API
    location /api/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host \$host;
        proxy_set_header X-Forwarded-Proto \$http_x_forwarded_proto;
$NO_CLIENT_IP
    }

    # WebSocket
    location /ws/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host \$host;
        proxy_set_header X-Forwarded-Proto \$http_x_forwarded_proto;
$NO_CLIENT_IP
        proxy_read_timeout 86400;
    }

    # Hashed build assets: a 404 here must stay a 404, never the SPA shell.
    # Falling back to index.html (text/html) for a missing chunk is what
    # causes "Failed to load module script: ... MIME type of text/html" —
    # a client that loaded chunk hashes from a previous deploy hits this
    # once the \`build\` symlink moves on. update.sh mirrors every release's
    # _app/ (content-hashed, so collision-free) into shared-app/, which is
    # never pruned — fall back there before giving up, so an in-flight tab
    # keeps resolving old chunks instead of erroring out mid-navigation.
    location /_app/ {
        try_files \$uri @shared_app;
    }

    location @shared_app {
        root $WWW_ROOT;
        rewrite ^/_app/(.*)\$ /shared-app/\$1 break;
        try_files \$uri =404;
    }

    # Never edge-cached: a stale worker serves its own older shell, so the
    # page and worker versions never converge and the update banner loops.
    location = /service-worker.js {
        add_header Cache-Control "no-cache" always;
        try_files \$uri =404;
    }

    # Probes (assetlinks.json, security.txt, ...) get a real 404, not the
    # SPA shell; give a file served here its own exact-match block.
    location /.well-known/ {
        return 404;
    }

    # SPA assets + client-side router fallback
    location / {
        try_files \$uri \$uri/ /index.html;
    }
EOF
)

if [ "$EDGE_MODE" = "mtls" ]; then
    mtls_install "$APP_DOMAIN"

    {
        echo "server {"
        mtls_nginx_listen_block "$APP_DOMAIN"
        echo "$NGINX_LOCATIONS"
        echo "}"
        echo ""
        mtls_nginx_redirect_block "$APP_DOMAIN"
    } > "/etc/nginx/sites-available/$APP_NAME"

    ddns_install "$APP_DOMAIN" "$CF_DNS_TOKEN" "$CF_ZONE_ID"
else
    mtls_remove
    ddns_remove

    {
        echo "server {"
        echo "    listen 127.0.0.1:$STATIC_PORT;"
        echo "    server_name $APP_DOMAIN;"
        echo ""
        echo "    # Cloudflare Tunnel terminates TLS; redirect any cleartext client requests."
        echo "    if (\$http_x_forwarded_proto = \"http\") {"
        echo "        return 301 https://\$host\$request_uri;"
        echo "    }"
        echo ""
        echo "$NGINX_LOCATIONS"
        echo "}"
    } > "/etc/nginx/sites-available/$APP_NAME"
fi

ln -sfn "/etc/nginx/sites-available/$APP_NAME" "/etc/nginx/sites-enabled/$APP_NAME"
rm -f /etc/nginx/sites-enabled/default
nginx -t
systemctl enable nginx --now
systemctl reload nginx

# ==============================================================================
# ADVANCED SYSTEMD SERVICE SANDBOXING IMPLEMENTATION
# ==============================================================================
echo -e "\n🔒 Configuring hardened systemd runtime sandboxes..."
cat <<EOF > "/etc/systemd/system/$APP_NAME.service"
[Unit]
Description=Hardened Daemon for $APP_NAME
After=network.target postgresql.service

[Service]
Type=simple
User=$APP_USER
Group=$APP_USER
EnvironmentFile=$ENV_FILE
ExecStart=/usr/local/bin/$APP_NAME
Restart=on-failure
RestartSec=5s

# Sandboxing & Ingress Lockdown Rules
# Note: MemoryDenyWriteExecute breaks many Go runtimes (esp. on aarch64/Pi).
CapabilityBoundingSet=
SystemCallFilter=@system-service
SystemCallFilter=~@clock @module @mount @obsolete @privileged @raw-io @reboot @swap
ProtectProc=invisible
ProcSubset=pid
RestrictNamespaces=true
LockPersonality=true
NoNewPrivileges=true
RestrictRealtime=true
UMask=0077
ProtectHostname=true
RemoveIPC=true
RestrictSUIDSGID=true
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
PrivateDevices=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable "$APP_NAME.service"
# Mint root (id=1) on first boot if missing — temporary ReadWritePaths drop-in,
# then revoke it. Always followed by an explicit restart so the locked-down
# unit is what stays running.
syrinx_ensure_root_bootstrap
# The server won't start without a current identity backup.
syrinx_ensure_identity_backup
systemctl restart "$APP_NAME.service"

# ==============================================================================
# EDGE NETWORK EXPOSURE CONTROL (Cloudflare Outbound Tunnel Setup)
# Skipped entirely under EDGE_MODE=mtls — nginx is the public edge there.
# ==============================================================================
if [ "$EDGE_MODE" = "mtls" ]; then
    if systemctl is-active --quiet cloudflared 2>/dev/null || [ -f /etc/systemd/system/cloudflared.service ]; then
        echo -e "\n☁️  EDGE_MODE=mtls — tearing down previously configured cloudflared..."
        systemctl disable --now cloudflared 2>/dev/null || true
        rm -f /etc/systemd/system/cloudflared.service
        systemctl daemon-reload
        echo "✅ cloudflared removed (nginx mTLS is now the public edge)"
    fi
else
# Token tunnels only need reconfigure when missing, token changed, or unhealthy.
# Ping cannot validate a Cloudflare tunnel — use HTTPS to the public hostname.
cloudflare_tunnel_healthy() {
    systemctl is-active --quiet cloudflared || return 1
    [ -f /etc/systemd/system/cloudflared.service ] || return 1
    grep -qF -- "$CF_TOKEN" /etc/systemd/system/cloudflared.service || return 1
    local code
    # DoH: the local resolver may cache "no such host" from before the route.
    code="$(curl -sS -m 4 --doh-url https://1.1.1.1/dns-query -o /dev/null -w '%{http_code}' "https://${APP_DOMAIN}/" 2>/dev/null)" || true
    # 530 is Cloudflare's 1033: the hostname routes to a tunnel with no connector.
    case "$code" in
        ""|000|530) return 1 ;;
        *) return 0 ;;
    esac
}

echo -e "\n☁️  Checking Cloudflare tunnel..."

FORCE_CF="${FORCE_CF:-0}"
if [ "$FORCE_CF" != "1" ] && cloudflare_tunnel_healthy; then
    echo "✅ Cloudflare tunnel already up for https://${APP_DOMAIN} — skipping reconfigure"
    echo "   (set FORCE_CF=1 to force rewrite of cloudflared unit/config)"
else
    if [ "$FORCE_CF" = "1" ]; then
        echo "🔹 FORCE_CF=1 — reconfiguring cloudflared"
    else
        echo "🔹 Tunnel missing or unhealthy — configuring cloudflared"
    fi

    if ! command -v cloudflared &> /dev/null; then
        ARCH="$(dpkg --print-architecture)"
        wget -q "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-${ARCH}.deb" -O /tmp/cf.deb
        dpkg -i /tmp/cf.deb
        rm -f /tmp/cf.deb
    fi

    mkdir -p /etc/cloudflared

    if systemctl is-active --quiet cloudflared; then
        systemctl stop cloudflared
    fi

    # Token-managed tunnels ignore this file and take routes from the
    # Cloudflare dashboard; the check below prints the route to add there.
    cat <<EOF > /etc/cloudflared/config.yml
ingress:
  - hostname: "$APP_DOMAIN"
    service: http://127.0.0.1:$STATIC_PORT
  - service: http_status:404
EOF

    cat <<EOF > /etc/systemd/system/cloudflared.service
[Unit]
Description=Cloudflare Outbound Tunnel Runtime Engine
After=network-online.target nginx.service
Wants=network-online.target

[Service]
Type=simple
User=root
ExecStart=/usr/local/bin/cloudflared --no-autoupdate tunnel run --token $CF_TOKEN
Restart=always
RestartSec=5s

[Install]
WantedBy=multi-user.target
EOF
    # Unit files under /etc/systemd/system are world-readable (644) by
    # default, and this one embeds CF_TOKEN in ExecStart= — restrict it
    # explicitly rather than relying on the ambient umask.
    chmod 600 /etc/systemd/system/cloudflared.service

    systemctl daemon-reload
    systemctl enable cloudflared --now
    sleep 2
fi

# 000/530 mean Cloudflare has no route from the domain to this tunnel (only
# the dashboard can add one); 502 means a route whose Service URL is wrong.
cf_public_status() {
    local code
    # DoH: the local resolver may cache "no such host" from before the route.
    code="$(curl -sS -m 4 --doh-url https://1.1.1.1/dns-query -o /dev/null -w '%{http_code}' "https://${APP_DOMAIN}/" 2>/dev/null)" || true
    printf '%s' "${code:-000}"
}
cf_route_ok() {
    case "$1" in 000|502|530) return 1 ;; *) return 0 ;; esac
}
# 000 is curl's "no response", not an HTTP code; say what each result means.
cf_route_problem() {
    case "$1" in
        000) echo "no DNS record or no response yet (add the route)" ;;
        530) echo "HTTP 530: the route is missing or the DNS record points at another tunnel" ;;
        502) echo "HTTP 502: the route's Service URL is wrong; it must be http://127.0.0.1:${STATIC_PORT}" ;;
        *) echo "HTTP $1" ;;
    esac
}

cf_public_code=000
echo "🔎 Checking https://${APP_DOMAIN}/ ..."
for _ in 1 2 3; do
    cf_public_code="$(cf_public_status)"
    cf_route_ok "$cf_public_code" && break
    sleep 1
done
if ! cf_route_ok "$cf_public_code"; then
    cf_tunnel_id="$(printf '%s' "$CF_TOKEN" | base64 -d 2>/dev/null | sed -n 's/.*"t":"\([^"]*\)".*/\1/p')"
    echo ""
    echo "⚠️  https://${APP_DOMAIN}/ isn't reaching this host: $(cf_route_problem "$cf_public_code")."
    echo "   In the Cloudflare dashboard: Zero Trust → Networks → Tunnels → the tunnel"
    echo "   with ID ${cf_tunnel_id:-<tunnel ID>} → Add route → Published application:"
    echo "     Subdomain + Domain: ${APP_DOMAIN}   Path: (empty)"
    echo "     Service URL:        http://127.0.0.1:${STATIC_PORT}"
    echo "   If Cloudflare says a DNS record already exists, delete the"
    echo "   ${APP_DOMAIN} record under DNS first, then add the route again."
    echo ""
    echo "⏳ Waiting up to 5 minutes for the route (press Enter to skip)..."
    cf_wait_end=$((SECONDS + 300))
    cf_last_code=""
    cf_skipped=0
    while [ "$SECONDS" -lt "$cf_wait_end" ]; do
        # A line (Enter, or `yes ''`) skips; EOF on stdin just waits.
        if read -r -t 3 _ 2>/dev/null; then
            echo "   Skipped."
            cf_skipped=1
            break
        elif [ $? -le 128 ]; then
            sleep 3
        fi
        cf_public_code="$(cf_public_status)"
        if cf_route_ok "$cf_public_code"; then
            echo "✅ https://${APP_DOMAIN}/ reaches this host (HTTP ${cf_public_code})"
            break
        fi
        if [ "$cf_public_code" != "$cf_last_code" ]; then
            echo "   Now: $(cf_route_problem "$cf_public_code")"
            cf_last_code="$cf_public_code"
        fi
    done
    if ! cf_route_ok "$cf_public_code" && [ "$cf_skipped" = 0 ]; then
        echo "   Gave up after 5 minutes; setup continues without the route."
    fi
fi
fi

# ==============================================================================
# VERIFICATION STATUS REPORTS
# ==============================================================================
echo -e "\n🎉 Final Deployment Report Assessment Profile:"
echo "------------------------------------------------------------------"
systemctl is-active --quiet "$APP_NAME" && echo "✅ Application Process Node: ONLINE" || echo "❌ Application Process Node: FAILED"
systemctl is-active --quiet nginx && echo "✅ Local Static Origin (nginx): ONLINE" || echo "❌ Local Static Origin (nginx): FAILED"
if [ "$EDGE_MODE" = "mtls" ]; then
    echo "✅ Public edge: nginx mTLS on 443 (client cert required, CA in $MTLS_DIR)"
    echo "   Last known public IP: $(ddns_last_known_ip)"
    systemctl is-active --quiet "$DDNS_TIMER" && echo "✅ DDNS updater: ACTIVE" || echo "❌ DDNS updater: INACTIVE"
else
    systemctl is-active --quiet cloudflared && echo "✅ Cloudflare Gateway Network: CONNECTED" || echo "❌ Cloudflare Gateway Network: DISCONNECTED"
    if cf_route_ok "${cf_public_code:-000}"; then
        echo "✅ Tunnel route to https://${APP_DOMAIN}: REACHABLE (HTTP ${cf_public_code})"
    else
        echo "❌ Tunnel route to https://${APP_DOMAIN}: $(cf_route_problem "${cf_public_code:-000}")"
    fi
fi
if [ -n "$TELEMETRY_HOST" ]; then
    echo "✅ OTEL: journald shipper → ${TELEMETRY_HOST}:4318 (OpenObserve org=default)"
    systemctl is-active --quiet otelcol-agent && echo "✅ Journald OTLP shipper: ONLINE" || echo "❌ Journald OTLP shipper: FAILED"
else
    echo "⚪ OTEL export: disabled"
fi
echo "------------------------------------------------------------------"
if [ "$EDGE_MODE" = "mtls" ]; then
    echo "nginx is listening directly on 443 — make sure your router forwards"
    echo "443/tcp (and 80/tcp for the HTTPS redirect) to this host."
    echo "Issue a client cert for a device with:"
    echo "  sudo bash -c 'source $SCRIPT_DIR/mtls.sh; mtls_issue_client_cert <name>'"
    echo "Fetch it from your laptop with: ./cp-client-cert.sh <name>"
else
    echo "Cloudflare tunnel route (Add route → Published application) for $APP_DOMAIN"
    echo "  Service URL: http://127.0.0.1:$STATIC_PORT"
fi
if [ "$EDGE_MODE" != "mtls" ] && ! cf_route_ok "${cf_public_code:-000}"; then
    echo "⚠️  Environment build finished, but https://$APP_DOMAIN isn't reachable until the tunnel route is added (see above)"
else
    echo "🚀 Environment build finished! Your setup is safely listening at https://$APP_DOMAIN"
fi
echo "📝 Log saved to $LOG_FILE"
echo "📄 App env: $ENV_FILE"
echo "📄 Setup env: $SETUP_ENV"
echo "🗄️  DB shell: sudo $SCRIPT_DIR/psql.sh"
echo "📦 Root export dir: /var/lib/${APP_NAME}/root-export"

echo -e "\n🔏 Server public key:"
"$SCRIPT_DIR/server-key.sh" || echo "⚠️  Could not read the server key — try: sudo $SCRIPT_DIR/server-key.sh"

# Clean build workpaths up safely
rm -rf "$BUILD_DIR"
