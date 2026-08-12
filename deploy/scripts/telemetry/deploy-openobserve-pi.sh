#!/bin/bash
# Deploy a Mac-cross-compiled OpenObserve binary to a Raspberry Pi.
#
# Expects the artifact from cross-compile-openobserve-pi.sh:
#   dist/openobserve-vX.Y.Z-pi-arm64
#
# Prompts for the Pi address (any LAN). Skips scp when the matching binary
# is already on the Pi. Installs into the OpenObserve cache + /usr/local/bin,
# wires openobserve.service, and installs/configures otelcol-contrib
# (OTLP :4317/:4318 → OpenObserve :5081).
#
# Usage:
#   ./deploy-openobserve-pi.sh
#   DEPLOY_HOST=pi@10.0.0.50 ./deploy-openobserve-pi.sh   # non-interactive
#   O2_VERSION=0.91.5 ./deploy-openobserve-pi.sh
#   ARTIFACT=/path/to/openobserve-v0.91.5-pi-arm64 ./deploy-openobserve-pi.sh
#   RESTART=0 ./deploy-openobserve-pi.sh
#   FORCE_COPY=1 ./deploy-openobserve-pi.sh   # scp even if remote file matches
#   SSH_USER=pi ./deploy-openobserve-pi.sh    # default user when only an IP is given
#   ./deploy-openobserve-pi.sh --show-password  # print the admin login at the end

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
O2_VERSION="${O2_VERSION:-0.91.5}"
DEPLOY_PATH="${DEPLOY_PATH:-/var/lib/openobserve/build}"
OUT_DIR="${OUT_DIR:-$SCRIPT_DIR/dist}"
RESTART="${RESTART:-1}"
FORCE_COPY="${FORCE_COPY:-0}"
DEPLOY_ENV="${DEPLOY_ENV:-$SCRIPT_DIR/deploy.env}"
SSH_USER_DEFAULT="${SSH_USER:-$(id -un)}"

SHOW_PASSWORD=0
for arg in "$@"; do
    case "$arg" in
        --show-password) SHOW_PASSWORD=1 ;;
        *)
            echo "Error: unknown argument: $arg (only --show-password is supported)" >&2
            exit 1
            ;;
    esac
done

version_tag() {
    case "$O2_VERSION" in
        v*) printf '%s' "$O2_VERSION" ;;
        *) printf 'v%s' "$O2_VERSION" ;;
    esac
}

VERSION_TAG="$(version_tag)"
ARTIFACT_NAME="openobserve-${VERSION_TAG}-pi-arm64"
ARTIFACT="${ARTIFACT:-$OUT_DIR/$ARTIFACT_NAME}"
REMOTE_CACHE="$DEPLOY_PATH/$ARTIFACT_NAME"

die() { echo "Error: $*" >&2; exit 1; }
info() { echo "-> $*"; }
ok() { echo "OK: $*"; }

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
    echo "Deploy OpenObserve ${VERSION_TAG} -> Raspberry Pi" >&2
    echo "================================================================" >&2
    if [ -n "$saved" ]; then
        read -r -p "Pi address (user@ip or ip) [${saved}]: " input
        input="${input:-$saved}"
    else
        read -r -p "Pi address (user@ip or ip): " input
    fi
    [ -n "$input" ] || die "Pi address is required"
    normalize_host "$input"
}

remote_file_fingerprint() {
    local host="$1" path="$2"
    # Cache dir is root-owned (0700) — must use sudo to see/hash the file.
    ssh "$host" "sudo test -f $(printf '%q' "$path") || exit 0
      size=\$(sudo stat -c%s $(printf '%q' "$path"))
      sum=\$(sudo sha256sum $(printf '%q' "$path") | awk '{print \$1}')
      printf '%s %s' \"\$size\" \"\$sum\""
}

local_file_fingerprint() {
    local path="$1" size sum
    size="$(stat -f%z "$path" 2>/dev/null || stat -c%s "$path")"
    if command -v shasum >/dev/null 2>&1; then
        sum="$(shasum -a 256 "$path" | awk '{print $1}')"
    elif command -v sha256sum >/dev/null 2>&1; then
        sum="$(sha256sum "$path" | awk '{print $1}')"
    else
        sum="nosum"
    fi
    printf '%s %s' "$size" "$sum"
}

# --- main --------------------------------------------------------------------

[ -f "$ARTIFACT" ] || die "Artifact not found: $ARTIFACT (run cross-compile-openobserve-pi.sh first)"
[ -x "$ARTIFACT" ] || chmod 755 "$ARTIFACT"

# Remember whether DEPLOY_HOST came from the environment (skip prompt).
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

HOST_ONLY="${DEPLOY_HOST#*@}"

info "Artifact: $ARTIFACT ($(du -h "$ARTIFACT" | awk '{print $1}'))"
info "Target:   $DEPLOY_HOST"
info "Cache:    $REMOTE_CACHE"
echo ""

info "Checking SSH connectivity..."
if ! ssh -o ConnectTimeout=8 "$DEPLOY_HOST" "true"; then
    die "Cannot SSH to $DEPLOY_HOST — check IP, user, and that the Pi is reachable on this network"
fi
ok "SSH OK"

info "Ensuring cache dir on Pi..."
ssh "$DEPLOY_HOST" "sudo mkdir -p $(printf '%q' "$DEPLOY_PATH")"

LOCAL_FP="$(local_file_fingerprint "$ARTIFACT")"
REMOTE_FP="$(remote_file_fingerprint "$DEPLOY_HOST" "$REMOTE_CACHE" || true)"

NEED_COPY=1
if [ "$FORCE_COPY" = "1" ]; then
    info "FORCE_COPY=1 — will scp binary"
elif [ -n "$REMOTE_FP" ] && [ "$REMOTE_FP" = "$LOCAL_FP" ]; then
    ok "Remote binary already matches local (size+hash) — skipping scp"
    NEED_COPY=0
elif [ -n "$REMOTE_FP" ]; then
    info "Remote binary differs or is incomplete — will scp"
else
    info "Remote binary not found at $REMOTE_CACHE — will scp"
fi

TMP_REMOTE="/tmp/${ARTIFACT_NAME}"

if [ "$NEED_COPY" = "1" ]; then
    info "Copying binary to ${DEPLOY_HOST}:${TMP_REMOTE} ..."
    scp "$ARTIFACT" "${DEPLOY_HOST}:${TMP_REMOTE}"
    if [ -f "${ARTIFACT}.version" ]; then
        scp "${ARTIFACT}.version" "${DEPLOY_HOST}:${TMP_REMOTE}.version"
    fi
    SOURCE_ON_PI="$TMP_REMOTE"
else
    SOURCE_ON_PI="$REMOTE_CACHE"
fi

info "Installing to ${REMOTE_CACHE} and /usr/local/bin/openobserve..."
ssh "$DEPLOY_HOST" "sudo install -o root -g root -m 755 $(printf '%q' "$SOURCE_ON_PI") $(printf '%q' "$REMOTE_CACHE")
  sudo install -o root -g root -m 755 $(printf '%q' "$REMOTE_CACHE") /usr/local/bin/openobserve
  if [ -f $(printf '%q' "${TMP_REMOTE}.version") ]; then
    sudo install -o root -g root -m 644 $(printf '%q' "${TMP_REMOTE}.version") $(printf '%q' "${REMOTE_CACHE}.version")
  fi
  printf 'cross\n' | sudo tee $(printf '%q' "${REMOTE_CACHE}.origin") >/dev/null
  rm -f $(printf '%q' "$TMP_REMOTE") $(printf '%q' "${TMP_REMOTE}.version")
  true"

# Remember install mode so on-Pi update.sh does not start a source build.
ssh "$DEPLOY_HOST" "sudo bash -s" <<'REMOTE'
set -euo pipefail
for f in /home/*/scripts/telemetry.env /root/scripts/telemetry.env; do
  [ -f "$f" ] || continue
  if grep -q '^O2_INSTALL_MODE=' "$f"; then
    sed -i 's/^O2_INSTALL_MODE=.*/O2_INSTALL_MODE=cross/' "$f"
  else
    echo 'O2_INSTALL_MODE=cross' >> "$f"
  fi
done
REMOTE

ok "Installed /usr/local/bin/openobserve (cached at $REMOTE_CACHE, origin=cross)"

# Ensure systemd unit + service user exist (config may already be on the Pi).
info "Wiring openobserve.service..."
ssh "$DEPLOY_HOST" "sudo bash -s" <<'REMOTE'
set -euo pipefail
O2_USER=openobserve
O2_DATA_DIR=/var/lib/openobserve
O2_ENV_FILE=/etc/openobserve/openobserve.env

if [ ! -f "$O2_ENV_FILE" ]; then
  echo "MISSING_ENV"
  exit 0
fi

if ! id -u "$O2_USER" >/dev/null 2>&1; then
  useradd -r -s /bin/false -d "$O2_DATA_DIR" "$O2_USER"
fi
mkdir -p "$O2_DATA_DIR"
chown "$O2_USER:$O2_USER" "$O2_DATA_DIR"

if [ ! -f /etc/systemd/system/openobserve.service ]; then
  cat > /etc/systemd/system/openobserve.service <<'UNIT'
[Unit]
Description=OpenObserve observability platform
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=openobserve
Group=openobserve
EnvironmentFile=/etc/openobserve/openobserve.env
ExecStart=/usr/local/bin/openobserve
Restart=on-failure
RestartSec=5s
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
UNIT
  systemctl daemon-reload
fi

echo "UNIT_OK"
REMOTE

# OTEL collector bridges app hosts (:4317/:4318) → OpenObserve (:5081).
info "Wiring otelcol-contrib (OTLP collector)..."
ssh "$DEPLOY_HOST" "sudo bash -s" <<'REMOTE'
set -euo pipefail
O2_ENV_FILE=/etc/openobserve/openobserve.env
OTEL_CONFIG=/etc/otelcol/config.yaml
OTELCOL_VERSION="${OTELCOL_VERSION:-0.120.0}"

if [ ! -f "$O2_ENV_FILE" ]; then
  echo "MISSING_ENV"
  exit 0
fi

email=$(grep '^ZO_ROOT_USER_EMAIL=' "$O2_ENV_FILE" | cut -d= -f2-)
pass=$(grep '^ZO_ROOT_USER_PASSWORD=' "$O2_ENV_FILE" | cut -d= -f2-)
[ -n "$email" ] && [ -n "$pass" ] || { echo "MISSING_CREDS"; exit 1; }

# Always publish into the built-in default org so the UI works out of the box.
org="default"
stream="syrinx"
for f in /home/*/scripts/telemetry.env /root/scripts/telemetry.env /home/*/telemetry/telemetry.env; do
  if [ -f "$f" ]; then
    # shellcheck disable=SC1090
    set -a; . "$f"; set +a
    # Ignore any saved ZO_ORG — keep a single default org.
    stream="${ZO_STREAM:-$stream}"
    break
  fi
done
# Persist default so a later on-box setup.sh cannot revive a custom org.
for f in /home/*/scripts/telemetry.env /root/scripts/telemetry.env; do
  if [ -f "$f" ]; then
    sed -i 's/^ZO_ORG=.*/ZO_ORG=default/' "$f" 2>/dev/null || true
  fi
done

arch=$(dpkg --print-architecture 2>/dev/null || uname -m)
case "$arch" in
  arm64|aarch64) tag=arm64 ;;
  amd64|x86_64)  tag=amd64 ;;
  *) echo "UNSUPPORTED_ARCH:$arch"; exit 1 ;;
esac

need_download=0
if [ ! -x /usr/local/bin/otelcol-contrib ]; then
  need_download=1
elif ! /usr/local/bin/otelcol-contrib --version 2>/dev/null | grep -q "$OTELCOL_VERSION"; then
  need_download=1
fi

if [ "$need_download" = "1" ]; then
  echo "Downloading otelcol-contrib v${OTELCOL_VERSION} (${tag})..."
  tmp=$(mktemp -d)
  url="https://github.com/open-telemetry/opentelemetry-collector-releases/releases/download/v${OTELCOL_VERSION}/otelcol-contrib_${OTELCOL_VERSION}_linux_${tag}.tar.gz"
  curl -fsSL "$url" -o "$tmp/otelcol.tar.gz"
  tar -xzf "$tmp/otelcol.tar.gz" -C "$tmp"
  install -o root -g root -m 755 "$tmp/otelcol-contrib" /usr/local/bin/otelcol-contrib
  rm -rf "$tmp"
fi

auth=$(printf '%s:%s' "$email" "$pass" | base64 -w0 2>/dev/null || printf '%s:%s' "$email" "$pass" | base64)
mkdir -p /etc/otelcol
cat > "$OTEL_CONFIG" <<CFG
# OTLP ingress for app hosts (4317/4318) → OpenObserve (5081), plus this
# telemetry host's own CPU/memory/disk/network usage.
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317
      http:
        endpoint: 0.0.0.0:4318

  # Host-level resource usage for the telemetry Pi itself — aggregate machine
  # stats only, no per-user data.
  hostmetrics:
    collection_interval: 30s
    scrapers:
      cpu:
        metrics:
          system.cpu.utilization:
            enabled: true
      memory:
        metrics:
          system.memory.utilization:
            enabled: true
      disk:
      filesystem:
        metrics:
          system.filesystem.utilization:
            enabled: true
      load:
      network:
      paging:

processors:
  batch:
    timeout: 1s
    send_batch_size: 1024
  # Tags host metrics with host.name so multiple hosts stay distinguishable.
  resourcedetection:
    detectors: [system]
    system:
      hostname_sources: [os]

exporters:
  otlp/openobserve:
    endpoint: localhost:5081
    headers:
      Authorization: "Basic ${auth}"
      organization: ${org}
      stream-name: ${stream}
    tls:
      insecure: true

service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [batch]
      exporters: [otlp/openobserve]
    # Forwarded from app hosts — already carries its own correct host.name;
    # must NOT go through this Pi's resourcedetection below, or every app
    # host's metrics get relabeled host.name=telemetry (this Pi's own name).
    metrics:
      receivers: [otlp]
      processors: [batch]
      exporters: [otlp/openobserve]
    # This Pi's own local hostmetrics — resourcedetection stamps host.name
    # for just this host, in its own pipeline so it can't leak onto the
    # forwarded metrics above.
    metrics/host:
      receivers: [hostmetrics]
      processors: [resourcedetection, batch]
      exporters: [otlp/openobserve]
    logs:
      receivers: [otlp]
      processors: [batch]
      exporters: [otlp/openobserve]
CFG
# Embeds the OpenObserve admin Basic-auth header (base64 is not encryption)
# — must not be world-readable like a plain collector config.
chmod 600 "$OTEL_CONFIG"

cat > /etc/systemd/system/otelcol-contrib.service <<'UNIT'
[Unit]
Description=OpenTelemetry Collector (OTLP bridge to OpenObserve)
After=network-online.target openobserve.service
Wants=network-online.target
Requires=openobserve.service

[Service]
Type=simple
User=root
ExecStart=/usr/local/bin/otelcol-contrib --config=/etc/otelcol/config.yaml
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
echo "OTEL_OK org=${org} stream=${stream}"
REMOTE

if ! ssh "$DEPLOY_HOST" "sudo test -f /etc/openobserve/openobserve.env"; then
    echo ""
    echo "No /etc/openobserve/openobserve.env on the Pi yet."
    echo "Run first-time setup on the Pi:"
    echo "  ssh $DEPLOY_HOST"
    echo "  cd ~/scripts && sudo ./setup.sh"
    echo "Cached binary is at $REMOTE_CACHE"
elif [ "$RESTART" = "1" ]; then
    info "Enabling and restarting openobserve.service..."
    if ssh "$DEPLOY_HOST" "sudo systemctl enable openobserve.service >/dev/null 2>&1 || true
        sudo systemctl restart openobserve.service
        for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
          code=\$(curl -sS -m 2 -o /dev/null -w '%{http_code}' http://127.0.0.1:5080/ 2>/dev/null || echo 000)
          case \$code in 200|301|302|303|307|308|401|404) echo active_http_\$code; exit 0 ;; esac
          sleep 1
        done
        systemctl is-active openobserve.service
        exit 1"; then
        ok "openobserve.service is up on port 5080"
    else
        echo "Warning: service did not become ready. Recent logs:" >&2
        ssh "$DEPLOY_HOST" "sudo journalctl -u openobserve -n 40 --no-pager" >&2 || true
        echo "If you see 'ZO_ROOT_USER_PASSWORD is too weak', set a stronger password in /etc/openobserve/openobserve.env" >&2
        exit 1
    fi

    info "Enabling and restarting otelcol-contrib.service..."
    if ssh "$DEPLOY_HOST" "sudo systemctl enable otelcol-contrib.service >/dev/null 2>&1 || true
        sudo systemctl restart otelcol-contrib.service
        sleep 2
        systemctl is-active otelcol-contrib.service
        ss -lntp | grep -E ':4317|:4318' || true"; then
        ok "otelcol-contrib is up on :4317 (gRPC) / :4318 (HTTP)"
    else
        echo "Warning: otelcol-contrib failed to start. Logs:" >&2
        ssh "$DEPLOY_HOST" "sudo journalctl -u otelcol-contrib -n 40 --no-pager" >&2 || true
        exit 1
    fi
else
    info "Skipping restart (RESTART=0)"
fi

echo ""
ok "Deploy complete."
echo "  UI:   http://${HOST_ONLY}:5080"
echo "  OTLP: ${HOST_ONLY}:4317 (gRPC) / ${HOST_ONLY}:4318 (HTTP)"
echo "  Saved host to $DEPLOY_ENV (used as default next run)"
echo "  On the Syrinx host, point OTEL at ${HOST_ONLY} (scripts/setup.sh)."

if ssh "$DEPLOY_HOST" "sudo test -f /etc/openobserve/openobserve.env"; then
    echo ""
    echo "Login (from /etc/openobserve/openobserve.env on the Pi):"
    # Password is withheld from stdout by default (shell history/scrollback/CI
    # logs are not a safe place for it). Pass --show-password to print it.
    ssh "$DEPLOY_HOST" "sudo awk -F= '
      \$1 == \"ZO_ROOT_USER_EMAIL\" { email = substr(\$0, index(\$0, \"=\") + 1) }
      \$1 == \"ZO_ROOT_USER_PASSWORD\" { pass = (\"${SHOW_PASSWORD}\" == \"1\") ? substr(\$0, index(\$0, \"=\") + 1) : \"(hidden — rerun with --show-password, or read the file on the Pi)\" }
      END {
        if (email != \"\") print \"  Email:    \" email
        if (pass != \"\")  print \"  Password: \" pass
      }' /etc/openobserve/openobserve.env"
fi
