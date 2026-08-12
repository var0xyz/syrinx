#!/bin/bash
# Shared helpers for OpenObserve + OTEL Collector on the telemetry host.
# https://openobserve.ai/docs/getting-started/

set -euo pipefail

O2_VERSION="${O2_VERSION:-0.91.5}"
OTELCOL_VERSION="${OTELCOL_VERSION:-0.120.0}"
ZO_ORG="default"
ZO_STREAM="${ZO_STREAM:-syrinx}"
# opensource | o2-enterprise — mirrors https://github.com/openobserve/openobserve/blob/main/downloadO2.sh
O2_RELEASE_TYPE="${O2_RELEASE_TYPE:-opensource}"

O2_USER="openobserve"
O2_DATA_DIR="/var/lib/openobserve"
O2_ENV_DIR="/etc/openobserve"
O2_ENV_FILE="$O2_ENV_DIR/openobserve.env"
OTEL_CONFIG="/etc/otelcol/config.yaml"
O2_INSTALL_MODE="binary"
O2_PI_BUILD_DIR="/var/lib/openobserve/build"
O2_SOURCE_DIR="$O2_PI_BUILD_DIR/openobserve-src"
O2_BUILD_JOBS="${O2_BUILD_JOBS:-2}"
# Node heap for OpenObserve web UI build (MiB). Pi 4 needs swap + raised limit.
O2_NODE_MAX_OLD_SPACE_SIZE="${O2_NODE_MAX_OLD_SPACE_SIZE:-}"
O2_BUILD_TMUX_SESSION="openobserve-build"
O2_BUILD_LOG="$O2_PI_BUILD_DIR/build.log"
O2_BUILD_EXIT_FILE="$O2_PI_BUILD_DIR/build.exit"

# --- Architecture detection (kernel + dpkg) -----------------------------------
# Supported platforms: arm64 | amd64 (native OpenObserve binaries).
# Re-detected on every setup/update so migrating to a new Pi picks the right binary.

telemetry_kernel_arch() {
    uname -m
}

telemetry_deb_arch() {
    dpkg --print-architecture 2>/dev/null || echo "unknown"
}

# Map kernel + Debian arch → install platform.
telemetry_detect_platform() {
    local kernel deb platform
    kernel="$(telemetry_kernel_arch)"
    deb="$(telemetry_deb_arch)"

    case "$kernel" in
        aarch64|arm64) platform="arm64" ;;
        x86_64|amd64)  platform="amd64" ;;
        armv7l|armv6l) platform="armhf" ;;
        i686|i386)     platform="amd64" ;;
        *)
            case "$deb" in
                arm64) platform="arm64" ;;
                amd64) platform="amd64" ;;
                armhf|armel) platform="armhf" ;;
            esac
            ;;
    esac

    if [ -z "${platform:-}" ]; then
        echo "❌ Unsupported architecture (kernel=$kernel dpkg=$deb)" >&2
        exit 1
    fi

    # 32-bit userspace cannot run 64-bit binaries — trust dpkg for install artifacts.
    case "$deb" in
        armhf|armel) platform="armhf" ;;
        arm64)       platform="arm64" ;;
        amd64)       platform="amd64" ;;
    esac

    echo "$platform"
}

telemetry_platform_info() {
    local platform pi_note=""
    platform="$(telemetry_detect_platform)"
    if telemetry_is_raspberry_pi; then
        pi_note=" pi=yes"
    fi
    echo "kernel=$(telemetry_kernel_arch) dpkg=$(telemetry_deb_arch) platform=$platform${pi_note} otel=$(telemetry_otelcol_binary_tag "$platform")"
}

# Raspberry Pi lacks AES — standard OpenObserve arm64 binaries SIGILL (see GH #3910).
telemetry_is_raspberry_pi() {
    if [ -f /proc/device-tree/model ]; then
        tr -d '\0' </proc/device-tree/model | grep -qi 'raspberry pi' && return 0
    fi
    if grep -qiE 'raspberry|bcm28|bcm27' /proc/cpuinfo 2>/dev/null; then
        return 0
    fi
    return 1
}

# Fail fast on unsupported platforms (OpenObserve ships arm64/amd64 binaries only).
telemetry_assert_supported_platform() {
    local platform kernel
    platform="$(telemetry_detect_platform)"
    kernel="$(telemetry_kernel_arch)"

    case "$platform" in
        arm64|amd64) return 0 ;;
    esac

    echo "❌ OpenObserve is not available on 32-bit ARM (armhf / armv7)." >&2
    echo "   OpenObserve publishes native binaries for arm64 and amd64 only." >&2
    echo "" >&2
    echo "   Detected: kernel=$kernel dpkg=$(telemetry_deb_arch)" >&2
    echo "" >&2
    if [ "$kernel" = "aarch64" ]; then
        echo "   Your CPU is 64-bit capable but userspace is 32-bit." >&2
        echo "   → Reinstall with Raspberry Pi OS / Debian arm64 (64-bit)." >&2
    else
        echo "   → Run telemetry on an arm64 host (e.g. Raspberry Pi 4/5 with 64-bit OS)." >&2
    fi
    echo "   → Copy /var/lib/openobserve to the new host, then run telemetry/setup.sh." >&2
    exit 1
}

# OTEL Collector release artifact suffix.
telemetry_otelcol_binary_tag() {
    local platform="${1:-$(telemetry_detect_platform)}"
    case "$platform" in
        arm64) echo "arm64" ;;
        amd64) echo "amd64" ;;
        armhf) echo "armv7" ;;
        *)
            echo "❌ No OTEL Collector binary for platform: $platform" >&2
            exit 1
            ;;
    esac
}

# OpenObserve tarball suffix (before optional musl fallback).
telemetry_o2_binary_tag() {
    local platform="${1:-$(telemetry_detect_platform)}"
    case "$platform" in
        arm64) echo "arm64" ;;
        amd64) echo "amd64" ;;
        *)
            echo "❌ No native OpenObserve binary for platform: $platform" >&2
            exit 1
            ;;
    esac
}

telemetry_generate_secret() {
    openssl rand -base64 18 | tr -dc 'a-zA-Z0-9'
}

# Persist interactive answers (call after prompts, and again after install completes).
telemetry_persist_env() {
    local env_file="$1"
    umask 077
    {
        printf "TELEMETRY_HOST=%q\n" "${TELEMETRY_HOST:-telemetry}"
        printf "ZO_ROOT_USER_EMAIL=%q\n" "${ZO_ROOT_USER_EMAIL:-}"
        printf "ZO_ROOT_USER_PASSWORD=%q\n" "${ZO_ROOT_USER_PASSWORD:-}"
        printf "ZO_ORG=%q\n" "${ZO_ORG:-default}"
        printf "ZO_STREAM=%q\n" "${ZO_STREAM:-syrinx}"
        printf "O2_VERSION=%q\n" "${O2_VERSION:-0.91.5}"
        printf "OTELCOL_VERSION=%q\n" "${OTELCOL_VERSION:-0.120.0}"
        printf "O2_RELEASE_TYPE=%q\n" "${O2_RELEASE_TYPE:-opensource}"
        printf "O2_BUILD_JOBS=%q\n" "${O2_BUILD_JOBS:-2}"
        printf "O2_INSTALL_MODE=%q\n" "${O2_INSTALL_MODE:-binary}"
        printf "TELEMETRY_DOMAIN=%q\n" "${TELEMETRY_DOMAIN:-}"
        printf "CF_TOKEN=%q\n" "${CF_TOKEN:-}"
        # Space-separated CIDRs allowed to reach OTLP (:4317/:4318) and the UI (:5080).
        printf "TELEMETRY_LAN_CIDRS=%q\n" "${TELEMETRY_LAN_CIDRS:-192.168.0.0/16 10.0.0.0/8 172.16.0.0/12}"
    } > "$env_file"
    chmod 600 "$env_file"
}

telemetry_auth_header() {
    local email="$1" password="$2"
    printf 'Basic %s' "$(printf '%s:%s' "$email" "$password" | base64 -w0 2>/dev/null || printf '%s:%s' "$email" "$password" | base64)"
}

telemetry_write_o2_env() {
    local email="$1" password="$2"
    mkdir -p "$O2_ENV_DIR"
    umask 077
    cat > "$O2_ENV_FILE" <<EOF
ZO_ROOT_USER_EMAIL=$email
ZO_ROOT_USER_PASSWORD=$password
ZO_DATA_DIR=$O2_DATA_DIR
ZO_HTTP_ADDR=0.0.0.0
ZO_HTTP_PORT=5080
ZO_GRPC_PORT=5081
EOF
    chmod 600 "$O2_ENV_FILE"
}

telemetry_write_otel_config() {
    local auth_header="$1"
    mkdir -p "$(dirname "$OTEL_CONFIG")"
    cat > "$OTEL_CONFIG" <<EOF
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
      Authorization: "$auth_header"
      organization: $ZO_ORG
      stream-name: $ZO_STREAM
    tls:
      insecure: true

service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [batch]
      exporters: [otlp/openobserve]
    # Forwarded from app hosts — already carries its own correct host.name
    # (stamped by that host's own resourcedetection); must NOT be touched by
    # this Pi's resourcedetection below, or every app host's metrics get
    # relabeled host.name=telemetry (this Pi's own hostname).
    metrics:
      receivers: [otlp]
      processors: [batch]
      exporters: [otlp/openobserve]
    # This Pi's own local hostmetrics — resourcedetection here correctly
    # stamps host.name for just this host, in its own pipeline so it can
    # never leak onto forwarded metrics above.
    metrics/host:
      receivers: [hostmetrics]
      processors: [resourcedetection, batch]
      exporters: [otlp/openobserve]
    logs:
      receivers: [otlp]
      processors: [batch]
      exporters: [otlp/openobserve]
EOF
    # Embeds the OpenObserve admin Basic-auth header (base64 is not
    # encryption) — must not be world-readable like a plain collector config.
    chmod 600 "$OTEL_CONFIG"
}

telemetry_teardown_previous_mode() {
    systemctl stop openobserve.service 2>/dev/null || true
}

# Quick runtime check — catches SIGILL (4/ILL) from CPU-incompatible native builds.
telemetry_openobserve_smoke_test() {
    local tmpdir pid i
    if [ ! -x /usr/local/bin/openobserve ]; then
        return 1
    fi
    tmpdir="$(mktemp -d)"
    chmod 700 "$tmpdir"

    # Password must satisfy OpenObserve complexity checks (v0.91+).
    env \
        ZO_DATA_DIR="$tmpdir" \
        ZO_ROOT_USER_EMAIL="smoke@test.local" \
        ZO_ROOT_USER_PASSWORD="Aa1!smoke-Test-Pass-OK" \
        ZO_HTTP_ADDR=127.0.0.1 \
        ZO_HTTP_PORT=15080 \
        ZO_GRPC_PORT=15081 \
        /usr/local/bin/openobserve >/dev/null 2>&1 &
    pid=$!

    for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
        if ! kill -0 "$pid" 2>/dev/null; then
            wait "$pid" 2>/dev/null || true
            rm -rf "$tmpdir"
            return 1
        fi
        # Ready enough once HTTP answers (migrations can take several seconds).
        if curl -sS -m 1 -o /dev/null -w '' "http://127.0.0.1:15080/" 2>/dev/null; then
            break
        fi
        sleep 1
    done

    if ! kill -0 "$pid" 2>/dev/null; then
        wait "$pid" 2>/dev/null || true
        rm -rf "$tmpdir"
        return 1
    fi

    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
    rm -rf "$tmpdir"
    return 0
}

telemetry_o2_version_tag() {
    local v="${1:-$O2_VERSION}"
    case "$v" in
        v*) printf '%s' "$v" ;;
        *) printf 'v%s' "$v" ;;
    esac
}

telemetry_o2_download_names() {
    # Same layout as downloadO2.sh
    case "${O2_RELEASE_TYPE:-opensource}" in
        opensource|oss)
            O2_DOWNLOAD_BASE="https://downloads.openobserve.ai/releases/openobserve"
            O2_BINARY_FILENAME="openobserve"
            ;;
        o2-enterprise|enterprise|ee)
            O2_DOWNLOAD_BASE="https://downloads.openobserve.ai/releases/o2-enterprise"
            O2_BINARY_FILENAME="openobserve-ee"
            ;;
        *)
            echo "❌ Unknown O2_RELEASE_TYPE: ${O2_RELEASE_TYPE} (use opensource or o2-enterprise)" >&2
            exit 1
            ;;
    esac
}

# Official CDN download (downloadO2.sh). Optional arch suffix e.g. arm64-musl.
telemetry_download_o2_official() {
    local arch="${1:-$(telemetry_o2_binary_tag "$(telemetry_detect_platform)")}"
    local version_tag file_name url tmp bin_name
    version_tag="$(telemetry_o2_version_tag)"
    telemetry_o2_download_names
    file_name="${O2_BINARY_FILENAME}-${version_tag}-linux-${arch}.tar.gz"
    url="${O2_DOWNLOAD_BASE}/${version_tag}/${file_name}"
    tmp="/tmp/openobserve-${O2_VERSION}-${arch}"
    rm -rf "$tmp"
    mkdir -p "$tmp"

    echo "⬇️  Downloading ${file_name}..."
    echo "    (same URL as: curl -L .../downloadO2.sh | sh -s ${O2_RELEASE_TYPE} $(telemetry_o2_version_tag))"
    curl -fsSL "$url" -o "$tmp/openobserve.tar.gz" || return 1
    tar -xzf "$tmp/openobserve.tar.gz" -C "$tmp"
    bin_name="$(find "$tmp" -maxdepth 1 -type f -executable | head -1)"
    [ -z "$bin_name" ] && bin_name="$tmp/openobserve"
    [ ! -f "$bin_name" ] && bin_name="$tmp/${O2_BINARY_FILENAME}"
    install -o root -g root -m 755 "$bin_name" /usr/local/bin/openobserve
    rm -rf "$tmp"
}

telemetry_o2_pi_cached_binary() {
    printf '%s/openobserve-%s-pi-arm64' "$O2_PI_BUILD_DIR" "$(telemetry_o2_version_tag)"
}

telemetry_o2_pi_cached_origin() {
    printf '%s.origin' "$(telemetry_o2_pi_cached_binary)"
}

# cross | source | unknown — how the cached Pi binary was produced.
telemetry_o2_pi_install_origin() {
    local origin_file cached
    cached="$(telemetry_o2_pi_cached_binary)"
    origin_file="$(telemetry_o2_pi_cached_origin)"
    if [ -f "$origin_file" ]; then
        tr -d '[:space:]' < "$origin_file"
        return 0
    fi
    # Deploy from Mac writes .version next to the artifact; treat that as cross.
    if [ -f "${cached}.version" ]; then
        echo "cross"
        return 0
    fi
    case "${O2_INSTALL_MODE:-}" in
        cross|source) echo "$O2_INSTALL_MODE" ;;
        *) echo "unknown" ;;
    esac
}

telemetry_o2_mark_pi_origin() {
    local origin="$1"
    mkdir -p "$O2_PI_BUILD_DIR"
    printf '%s\n' "$origin" > "$(telemetry_o2_pi_cached_origin)"
}

telemetry_script_dir() {
    cd "$(dirname "${BASH_SOURCE[0]}")" && pwd
}

telemetry_should_wrap_build_in_tmux() {
    [ "${TELEMETRY_IN_TMUX:-}" = "1" ] && return 1
    [ "${TELEMETRY_SKIP_TMUX:-}" = "1" ] && return 1
    [ -n "${TMUX:-}" ] && return 1
    return 0
}

telemetry_write_o2_build_wrapper() {
    local script_dir env_file wrapper
    script_dir="$(telemetry_script_dir)"
    env_file="${TELEMETRY_ENV:-$script_dir/telemetry.env}"
    wrapper="$O2_PI_BUILD_DIR/build-wrapper.sh"
    cat > "$wrapper" <<EOF
#!/bin/bash
set -euo pipefail
export TELEMETRY_IN_TMUX=1
. $(printf '%q' "$script_dir/common.sh")
if [ -f $(printf '%q' "$env_file") ]; then
    set -a
    . $(printf '%q' "$env_file")
    set +a
fi
telemetry_build_openobserve_from_source
EOF
    chmod 755 "$wrapper"
}

telemetry_wait_for_o2_tmux_build() {
    local session exit_code
    session="$O2_BUILD_TMUX_SESSION"

    echo ""
    echo "🖥️  OpenObserve build running in tmux session: $session"
    echo "   Attach (safe to disconnect SSH after attaching): tmux attach -t $session"
    echo "   Detach without stopping build: Ctrl-b then d"
    echo "   Log: $O2_BUILD_LOG"
    echo ""

    while tmux has-session -t "$session" 2>/dev/null; do
        sleep 15
    done

    if [ ! -f "$O2_BUILD_EXIT_FILE" ]; then
        echo "❌ Build session ended without exit status — check $O2_BUILD_LOG" >&2
        exit 1
    fi

    exit_code="$(cat "$O2_BUILD_EXIT_FILE")"
    rm -f "$O2_BUILD_EXIT_FILE"

    if [ "$exit_code" != "0" ]; then
        echo "❌ OpenObserve build failed (exit $exit_code). Last 30 log lines:" >&2
        tail -n 30 "$O2_BUILD_LOG" >&2 || true
        exit 1
    fi
}

telemetry_run_o2_build_in_tmux() {
    local session wrapper
    session="$O2_BUILD_TMUX_SESSION"

    if ! telemetry_should_wrap_build_in_tmux; then
        telemetry_build_openobserve_from_source
        return 0
    fi

    if tmux has-session -t "$session" 2>/dev/null; then
        echo "ℹ️  OpenObserve build already running in tmux ($session)."
        telemetry_wait_for_o2_tmux_build
        return 0
    fi

    echo "🖥️  Starting OpenObserve build in tmux (survives SSH disconnect)..."
    DEBIAN_FRONTEND=noninteractive apt-get install -y tmux
    mkdir -p "$O2_PI_BUILD_DIR"
    rm -f "$O2_BUILD_EXIT_FILE"
    : > "$O2_BUILD_LOG"

    telemetry_write_o2_build_wrapper
    wrapper="$O2_PI_BUILD_DIR/build-wrapper.sh"

    tmux new-session -d -s "$session" \
        "bash -lc $(printf '%q' "$wrapper") 2>&1 | tee -a $(printf '%q' "$O2_BUILD_LOG"); echo \$? > $(printf '%q' "$O2_BUILD_EXIT_FILE")"

    telemetry_wait_for_o2_tmux_build
}

telemetry_cargo_env() {
    if [ -f /root/.cargo/env ]; then
        # shellcheck disable=SC1091
        . /root/.cargo/env
    elif [ -f "${HOME}/.cargo/env" ]; then
        # shellcheck disable=SC1091
        . "${HOME}/.cargo/env"
    fi
}

telemetry_install_o2_build_deps() {
    echo "📦 Installing OpenObserve build dependencies..."
    DEBIAN_FRONTEND=noninteractive apt-get install -y \
        git curl ca-certificates build-essential protobuf-compiler \
        pkg-config libssl-dev

    if ! command -v node >/dev/null 2>&1 || [ "$(node -p "process.versions.node.split('.')[0]")" -lt 20 ]; then
        echo "⬇️  Installing Node.js 20 (required for OpenObserve UI build)..."
        curl -fsSL https://deb.nodesource.com/setup_20.x | bash -
        DEBIAN_FRONTEND=noninteractive apt-get install -y nodejs
    fi

    if ! command -v cargo >/dev/null 2>&1; then
        echo "⬇️  Installing Rust (rustup)..."
        curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y --default-toolchain none
    fi
    telemetry_cargo_env
}

telemetry_ensure_build_swap() {
    local mem_kb swap_kb swapfile=/swapfile-o2-build swap_gb=4

    mem_kb="$(awk '/MemTotal:/ {print $2}' /proc/meminfo)"
    swap_kb="$(awk '/SwapTotal:/ {print $2}' /proc/meminfo)"

    # Pi 4 (≤4GB RAM): npm run build needs ~3GB Node heap; ensure enough swap.
    if [ "$mem_kb" -ge 6000000 ]; then
        return 0
    fi

    if [ "$swap_kb" -ge $((swap_gb * 1024 * 1024 - 200000)) ]; then
        return 0
    fi

    if swapon --show 2>/dev/null | grep -qF "$swapfile"; then
        echo "💾 Resizing build swap to ${swap_gb}G..."
        swapoff "$swapfile" 2>/dev/null || true
    fi

    echo "💾 Adding ${swap_gb}G swap for source build (Pi RAM is tight)..."
    rm -f "$swapfile"
    if ! fallocate -l "${swap_gb}G" "$swapfile" 2>/dev/null; then
        dd if=/dev/zero of="$swapfile" bs=1M count=$((swap_gb * 1024)) status=progress
    fi
    chmod 600 "$swapfile"
    mkswap "$swapfile" >/dev/null
    swapon "$swapfile"
}

telemetry_o2_node_heap_mb() {
    if [ -n "${O2_NODE_MAX_OLD_SPACE_SIZE:-}" ]; then
        echo "$O2_NODE_MAX_OLD_SPACE_SIZE"
        return 0
    fi
    local mem_kb
    mem_kb="$(awk '/MemTotal:/ {print $2}' /proc/meminfo)"
    if [ "$mem_kb" -lt 4500000 ]; then
        echo 3072
    elif [ "$mem_kb" -lt 8000000 ]; then
        echo 4096
    else
        echo 8192
    fi
}

telemetry_patch_openobserve_for_pi() {
    local src_dir="$1"
    sed -i 's/default = \["gxhash"\]/default = []/' "$src_dir/src/config/Cargo.toml"
    sed -i 's/target-feature=+aes,+neon/target-feature=+neon/g' "$src_dir/.cargo/config.toml"
}

telemetry_fetch_openobserve_source() {
    local version_tag
    version_tag="$(telemetry_o2_version_tag)"
    mkdir -p "$O2_PI_BUILD_DIR"

    if [ -f "$O2_SOURCE_DIR/.o2-checked-out-version" ] && \
       [ "$(cat "$O2_SOURCE_DIR/.o2-checked-out-version")" != "$version_tag" ]; then
        echo "🔄 OpenObserve version changed — removing old source tree..."
        rm -rf "$O2_SOURCE_DIR"
    fi

    if [ -d "$O2_SOURCE_DIR/.git" ]; then
        echo "🔄 Updating OpenObserve source at $O2_SOURCE_DIR..."
        git -C "$O2_SOURCE_DIR" fetch --tags origin
        git -C "$O2_SOURCE_DIR" checkout -f "$version_tag"
        git -C "$O2_SOURCE_DIR" clean -fdx
    else
        echo "⬇️  Cloning OpenObserve ${version_tag}..."
        rm -rf "$O2_SOURCE_DIR"
        git clone --depth 1 --branch "$version_tag" \
            https://github.com/openobserve/openobserve.git "$O2_SOURCE_DIR"
    fi
    printf '%s\n' "$version_tag" > "$O2_SOURCE_DIR/.o2-checked-out-version"
}

telemetry_build_openobserve_from_source() {
    local cached jobs
    cached="$(telemetry_o2_pi_cached_binary)"
    jobs="${O2_BUILD_JOBS:-2}"

    case "${O2_RELEASE_TYPE:-opensource}" in
        opensource|oss) ;;
        *)
            echo "❌ Pi source builds only support opensource (not ${O2_RELEASE_TYPE})." >&2
            exit 1
            ;;
    esac

    telemetry_install_o2_build_deps
    telemetry_ensure_build_swap
    telemetry_fetch_openobserve_source
    telemetry_patch_openobserve_for_pi "$O2_SOURCE_DIR"

    echo "🌐 Building OpenObserve web UI (embedded in binary)..."
    local node_heap
    node_heap="$(telemetry_o2_node_heap_mb)"
    echo "   Node heap limit: ${node_heap} MiB (set O2_NODE_MAX_OLD_SPACE_SIZE to override)"
    (
        cd "$O2_SOURCE_DIR/web"
        export NODE_OPTIONS="--max-old-space-size=${node_heap}"
        npm ci
        npx --yes update-browserslist-db@latest
        npm run build
    )

    echo "🔨 Compiling OpenObserve server (jobs=${jobs}) — expect 1–3+ hours on Pi 4..."
    # Web build is done — drop Node from memory before rustc.
    sync
    telemetry_cargo_env
    (
        cd "$O2_SOURCE_DIR"
        CARGO_BUILD_JOBS="$jobs" cargo build --release
    )

    install -o root -g root -m 755 "$O2_SOURCE_DIR/target/release/openobserve" "$cached"
    install -o root -g root -m 755 "$cached" /usr/local/bin/openobserve
}

telemetry_install_openobserve_for_pi() {
    local cached origin

    cached="$(telemetry_o2_pi_cached_binary)"
    origin="$(telemetry_o2_pi_install_origin)"

    # Prefer an existing Pi binary (usually Mac cross-compile). Never rebuild
    # just because a smoke test was flaky — only rebuild when forced.
    if [ "${O2_FORCE_REBUILD:-}" != "1" ] && [ -x "$cached" ]; then
        echo "ℹ️  Found cached Pi binary: $cached (origin=${origin})"
        install -o root -g root -m 755 "$cached" /usr/local/bin/openobserve
        if telemetry_openobserve_smoke_test; then
            if [ "$origin" = "cross" ]; then
                O2_INSTALL_MODE="cross"
                O2_BINARY_TAG="pi-cross"
            else
                O2_INSTALL_MODE="${O2_INSTALL_MODE:-source}"
                [ "$O2_INSTALL_MODE" = "binary" ] && O2_INSTALL_MODE="source"
                O2_BINARY_TAG="${O2_BINARY_TAG:-pi-source}"
            fi
            echo "✅ OpenObserve Pi binary OK (${O2_INSTALL_MODE}, $(telemetry_o2_version_tag))"
            return 0
        fi
        # Smoke failed but binary is present — keep it for cross installs.
        if [ "$origin" = "cross" ] || [ "${O2_INSTALL_MODE:-}" = "cross" ]; then
            echo "⚠️  Smoke test failed; keeping cross-compiled binary (set O2_FORCE_REBUILD=1 to rebuild on-Pi)."
            O2_INSTALL_MODE="cross"
            O2_BINARY_TAG="pi-cross"
            telemetry_o2_mark_pi_origin "cross"
            return 0
        fi
        echo "⚠️  Cached binary failed smoke test — will rebuild only if source builds are allowed."
    fi

    if [ "${O2_FORCE_REBUILD:-}" = "1" ]; then
        echo "🔹 O2_FORCE_REBUILD=1 — building OpenObserve from source on the Pi."
    elif [ -x "$cached" ]; then
        :
    elif [ "$origin" = "cross" ] || [ "${O2_INSTALL_MODE:-}" = "cross" ]; then
        echo "❌ No cached OpenObserve binary for $(telemetry_o2_version_tag)." >&2
        echo "   This host is configured for Mac cross-compile deploys." >&2
        echo "   → On your Mac: ./telemetry/cross-compile-openobserve-pi.sh && ./telemetry/deploy-openobserve-pi.sh" >&2
        echo "   → Or force an on-Pi build: O2_ALLOW_SOURCE_BUILD=1 sudo ./update.sh" >&2
        exit 1
    elif [ "${O2_ALLOW_SOURCE_BUILD:-0}" != "1" ] && [ "${O2_INSTALL_MODE:-}" != "source" ]; then
        echo "❌ No OpenObserve binary at $cached" >&2
        echo "   Preferred path: cross-compile on Mac, then deploy-openobserve-pi.sh" >&2
        echo "   On-Pi source builds: O2_ALLOW_SOURCE_BUILD=1 sudo ./setup.sh" >&2
        exit 1
    fi

    echo "🔨 Building OpenObserve from source on the Pi ($(telemetry_o2_version_tag))."
    echo "   Pre-built upstream arm64 binaries require AES and SIGILL on Pi."
    telemetry_run_o2_build_in_tmux
    telemetry_o2_mark_pi_origin "source"

    if ! telemetry_openobserve_smoke_test; then
        echo "❌ Source build failed smoke test on this Pi." >&2
        exit 1
    fi

    O2_INSTALL_MODE="source"
    O2_BINARY_TAG="pi-source"
    echo "✅ OpenObserve Pi binary OK (source, $(telemetry_o2_version_tag))"
}

telemetry_install_openobserve() {
    local platform tag tags

    if telemetry_is_raspberry_pi; then
        telemetry_install_openobserve_for_pi
        return
    fi

    platform="$(telemetry_detect_platform)"

    O2_INSTALL_MODE="binary"
    if [ "$platform" = "arm64" ]; then
        tags="arm64 arm64-musl"
    else
        tags="amd64"
    fi

    for tag in $tags; do
        if ! telemetry_download_o2_official "$tag"; then
            echo "⚠️  Download failed for linux-${tag}"
            continue
        fi
        if telemetry_openobserve_smoke_test; then
            O2_BINARY_TAG="$tag"
            echo "✅ OpenObserve binary OK (linux-${tag}, ${O2_RELEASE_TYPE})"
            return 0
        fi
        echo "⚠️  linux-${tag} crashed on startup — trying next..."
    done

    echo "❌ Could not install a working OpenObserve build on this host." >&2
    exit 1
}

telemetry_install_otelcol() {
    local tag url tmp ver_major
    tag="$(telemetry_otelcol_binary_tag)"
    ver_major="${OTELCOL_VERSION%.*}"

    if [ -x /usr/local/bin/otelcol-contrib ] && \
       /usr/local/bin/otelcol-contrib --version 2>/dev/null | grep -qE "${ver_major}\.|${OTELCOL_VERSION}"; then
        echo "ℹ️  otelcol-contrib v${OTELCOL_VERSION} already installed — skipping download"
        OTELCOL_BINARY_TAG="$tag"
        return 0
    fi

    url="https://github.com/open-telemetry/opentelemetry-collector-releases/releases/download/v${OTELCOL_VERSION}/otelcol-contrib_${OTELCOL_VERSION}_linux_${tag}.tar.gz"
    tmp="/tmp/otelcol-${OTELCOL_VERSION}-${tag}"
    rm -rf "$tmp"
    mkdir -p "$tmp"

    echo "⬇️  Downloading OTEL Collector v${OTELCOL_VERSION} (linux-${tag})..."
    curl -fsSL "$url" -o "$tmp/otelcol.tar.gz"
    tar -xzf "$tmp/otelcol.tar.gz" -C "$tmp"
    install -o root -g root -m 755 "$tmp/otelcol-contrib" /usr/local/bin/otelcol-contrib
    rm -rf "$tmp"
    OTELCOL_BINARY_TAG="$tag"
}

telemetry_write_systemd_units() {
    mkdir -p "$O2_DATA_DIR"

    if ! id -u "$O2_USER" >/dev/null 2>&1; then
        useradd -r -s /bin/false -d "$O2_DATA_DIR" "$O2_USER"
    fi
    chown "$O2_USER:$O2_USER" "$O2_DATA_DIR"
    cat > /etc/systemd/system/openobserve.service <<EOF
[Unit]
Description=OpenObserve observability platform
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$O2_USER
Group=$O2_USER
EnvironmentFile=$O2_ENV_FILE
ExecStart=/usr/local/bin/openobserve
Restart=on-failure
RestartSec=5s
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
EOF

    cat > /etc/systemd/system/otelcol-contrib.service <<EOF
[Unit]
Description=OpenTelemetry Collector (OTLP bridge to OpenObserve)
After=network-online.target openobserve.service
Wants=network-online.target
Requires=openobserve.service

[Service]
Type=simple
User=root
ExecStart=/usr/local/bin/otelcol-contrib --config=$OTEL_CONFIG
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
EOF
}

telemetry_save_platform_state() {
    local env_file="$1"
    local platform
    platform="$(telemetry_detect_platform)"

    if [ ! -f "$env_file" ]; then
        telemetry_persist_env "$env_file"
    fi

    grep -v '^TELEMETRY_PLATFORM=' "$env_file" | \
    grep -v '^O2_INSTALL_MODE=' | \
    grep -v '^O2_BINARY_TAG=' | \
    grep -v '^OTELCOL_BINARY_TAG=' | \
    grep -v '^O2_DOCKER_IMAGE=' > "${env_file}.tmp" || true
    {
        printf "TELEMETRY_PLATFORM=%q\n" "$platform"
        printf "O2_INSTALL_MODE=%q\n" "${O2_INSTALL_MODE:-binary}"
        printf "O2_BINARY_TAG=%q\n" "${O2_BINARY_TAG:-}"
        printf "OTELCOL_BINARY_TAG=%q\n" "${OTELCOL_BINARY_TAG:-$(telemetry_otelcol_binary_tag "$platform")}"
    } >> "${env_file}.tmp"
    mv "${env_file}.tmp" "$env_file"
    chmod 600 "$env_file"
}

telemetry_install_stack() {
    local platform prev_platform prev_mode
    telemetry_assert_supported_platform
    platform="$(telemetry_detect_platform)"
    prev_platform="${TELEMETRY_PLATFORM:-}"
    prev_mode="${O2_INSTALL_MODE:-}"

    echo "🔹 $(telemetry_platform_info)"

    if [ -n "$prev_platform" ] && [ "$prev_platform" != "$platform" ]; then
        echo "🔄 Platform changed ($prev_platform → $platform) — reinstalling..."
        telemetry_teardown_previous_mode
    fi

    telemetry_install_openobserve

    if [ -n "$prev_mode" ] && [ "$prev_mode" != "$O2_INSTALL_MODE" ]; then
        echo "🔄 Install mode changed ($prev_mode → $O2_INSTALL_MODE) — updating service unit..."
        telemetry_teardown_previous_mode
    fi

    telemetry_install_otelcol
    telemetry_write_systemd_units
}

# Open OTLP + OpenObserve UI to the configured LAN CIDRs only (never 0.0.0.0/0).
# Override with TELEMETRY_LAN_CIDRS (space-separated). Default: all RFC1918 private ranges.
telemetry_configure_firewall() {
    local cidrs="${TELEMETRY_LAN_CIDRS:-192.168.0.0/16 10.0.0.0/8 172.16.0.0/12}"
    local cidr
    local -a ports=(4317 4318 5080)
    local -a comments=('OTLP gRPC from LAN' 'OTLP HTTP from LAN' 'OpenObserve UI from LAN')
    local i

    if [ -z "${cidrs// }" ]; then
        echo "⚠️  TELEMETRY_LAN_CIDRS is empty — skipping UFW LAN rules (OTLP/UI stay localhost-only)"
        return 0
    fi

    echo "🔹 UFW: allowing OTLP/UI from: $cidrs"
    for cidr in $cidrs; do
        for i in 0 1 2; do
            ufw allow from "$cidr" to any port "${ports[$i]}" proto tcp comment "${comments[$i]}" 2>/dev/null || true
        done
    done
}

telemetry_enable_services() {
    systemctl daemon-reload
    systemctl enable openobserve.service otelcol-contrib.service --now
}

telemetry_restart_services() {
    systemctl daemon-reload
    systemctl restart openobserve.service
    systemctl restart otelcol-contrib.service
}

telemetry_verify() {
    local i
    systemctl is-active --quiet openobserve && echo "✅ OpenObserve: ONLINE" || echo "❌ OpenObserve: FAILED"
    systemctl is-active --quiet otelcol-contrib && echo "✅ OTEL Collector: ONLINE" || echo "❌ OTEL Collector: FAILED"

    # Retry — on a Pi the HTTP listener can take a beat longer than a flat
    # sleep to accept connections while WAL/compactor jobs are starting.
    for i in 1 2 3 4 5 6 7 8 9 10; do
        if curl -sf "http://127.0.0.1:5080" >/dev/null 2>&1; then
            echo "✅ OpenObserve UI: http://127.0.0.1:5080"
            return 0
        fi
        sleep 1
    done
    echo "⚠️  OpenObserve UI not responding yet — check: journalctl -u openobserve -n 50 --no-pager"
}

# Import the community "Host Metrics" dashboard (CPU/memory/disk/network/load)
# if it isn't already present. Idempotent; best-effort — needs outbound
# internet to GitHub, and failure here must never abort setup.
telemetry_ensure_host_metrics_dashboard() {
    local email="$1" password="$2" org="${3:-$ZO_ORG}"
    local dash_url="https://raw.githubusercontent.com/openobserve/dashboards/main/hostmetrics/Host%20Metrics.dashboard.json"
    local existing tmp

    existing="$(curl -sS -u "${email}:${password}" "http://127.0.0.1:5080/api/${org}/dashboards" 2>/dev/null || true)"
    if printf '%s' "$existing" | grep -q '"title"[[:space:]]*:[[:space:]]*"Host Metrics"'; then
        echo "ℹ️  'Host Metrics' dashboard already present — skipping import"
        return 0
    fi

    tmp="$(mktemp)"
    if ! curl -fsSL "$dash_url" -o "$tmp" 2>/dev/null; then
        echo "⚠️  Could not download the community Host Metrics dashboard (no internet?) — import manually via Dashboards → Import"
        rm -f "$tmp"
        return 1
    fi

    if curl -sS -u "${email}:${password}" -X POST \
        -H "Content-Type: application/json" \
        --data-binary "@$tmp" \
        "http://127.0.0.1:5080/api/${org}/dashboards?folder=default" >/dev/null 2>&1; then
        echo "✅ Imported 'Host Metrics' dashboard (CPU, memory, disk, network, load)"
    else
        echo "⚠️  Failed to import the Host Metrics dashboard — import manually via Dashboards → Import"
    fi
    rm -f "$tmp"
}

# Regenerate dashboards/syrinx-app.dashboard.json from the generator script.
telemetry_regenerate_syrinx_dashboard() {
    local common_dir gen
    common_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    gen="$common_dir/dashboards/generate_syrinx_dashboard.py"
    if [ ! -f "$gen" ]; then
        return 0
    fi
    if ! command -v python3 >/dev/null 2>&1; then
        echo "⚠️  python3 missing — cannot regenerate Syrinx dashboard JSON"
        return 1
    fi
    python3 "$gen"
}

# Warn (never fails the run) if the live "Syrinx" dashboard has content that
# differs from the bundled JSON beyond server-assigned metadata (dashboard_id,
# hash, owner, created). update.sh always overwrites the live dashboard with
# the bundled one right after this — if someone hand-edited it in the
# OpenObserve UI, that's the last chance to notice before it's clobbered.
telemetry_warn_syrinx_dashboard_drift() {
    local email="$1" password="$2" org="$3" dash_id="$4" dash_file="$5"
    local common_dir
    common_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

    curl -sS -u "${email}:${password}" \
        "http://127.0.0.1:5080/api/${org}/dashboards/${dash_id}" 2>/dev/null \
    | python3 "$common_dir/dashboards/check_dashboard_drift.py" "$dash_file" "Syrinx" || true
}

# Import or update the bundled "Syrinx" app dashboard (traces + pool + business
# metrics). Always syncs the bundled JSON — create when missing, PUT when the
# title already exists (so update.sh picks up new tabs/panels).
telemetry_ensure_syrinx_dashboard() {
    local email="$1" password="$2" org="${3:-$ZO_ORG}"
    local common_dir dash_file dash_id dash_hash http_code

    common_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    dash_file="$common_dir/dashboards/syrinx-app.dashboard.json"

    telemetry_regenerate_syrinx_dashboard || true

    if [ ! -f "$dash_file" ]; then
        echo "⚠️  Missing $dash_file — skip Syrinx dashboard import"
        return 1
    fi

    if ! grep -q '"tabId"[[:space:]]*:[[:space:]]*"users"' "$dash_file" \
        || ! grep -q '"tabId"[[:space:]]*:[[:space:]]*"reeds"' "$dash_file" \
        || ! grep -q '"tabId"[[:space:]]*:[[:space:]]*"websocket"' "$dash_file"; then
        echo "⚠️  Bundled Syrinx dashboard JSON is stale — run dashboards/generate_syrinx_dashboard.py"
        return 1
    fi

    read -r dash_id dash_hash < <(
        curl -sS -u "${email}:${password}" \
            "http://127.0.0.1:5080/api/${org}/dashboards?page_size=1000" 2>/dev/null \
        | python3 "$common_dir/dashboards/find_dashboard_by_title.py" "Syrinx" 2>/dev/null || true
    )

    if [ -n "$dash_id" ]; then
        telemetry_warn_syrinx_dashboard_drift "$email" "$password" "$org" "$dash_id" "$dash_file" || true

        http_code="$(curl -sS -o /dev/null -w '%{http_code}' \
            -u "${email}:${password}" -X PUT \
            -H "Content-Type: application/json" \
            --data-binary "@$dash_file" \
            "http://127.0.0.1:5080/api/${org}/dashboards/${dash_id}?folder=default&hash=${dash_hash}" \
            2>/dev/null || echo 000)"
        case "$http_code" in
            200|201)
                echo "✅ Updated 'Syrinx' dashboard (Overview, Requests, Database, Users, Reeds, WebSocket)"
                return 0
                ;;
            *)
                echo "⚠️  Failed to update Syrinx dashboard (HTTP ${http_code}) — delete it in the UI and re-run update"
                return 1
                ;;
        esac
    fi

    if curl -sS -u "${email}:${password}" -X POST \
        -H "Content-Type: application/json" \
        --data-binary "@$dash_file" \
        "http://127.0.0.1:5080/api/${org}/dashboards?folder=default" >/dev/null 2>&1; then
        echo "✅ Imported 'Syrinx' dashboard (Overview, Requests, Database, Users, Reeds, WebSocket)"
        return 0
    fi
    echo "⚠️  Failed to import the Syrinx dashboard — import manually via Dashboards → Import"
    return 1
}

# --- Cloudflare Tunnel (remote access to the OpenObserve UI) -----------------
# Same token-tunnel pattern scripts/setup.sh uses for the app host, pointed
# straight at OpenObserve (127.0.0.1:5080) since this host has no nginx. A
# plain ping/curl to localhost can't validate a Cloudflare tunnel, so health
# is checked over HTTPS against the public hostname instead.
telemetry_cloudflare_tunnel_healthy() {
    local domain="$1" token="$2"
    systemctl is-active --quiet cloudflared || return 1
    [ -f /etc/systemd/system/cloudflared.service ] || return 1
    grep -qF -- "$token" /etc/systemd/system/cloudflared.service || return 1
    local code
    code="$(curl -sS -m 12 -o /dev/null -w '%{http_code}' "https://${domain}/" 2>/dev/null || echo 000)"
    [ "$code" != "000" ]
}

# Expose OpenObserve at https://$domain via a Cloudflare Zero Trust tunnel —
# no inbound port opened. Idempotent; pass force=1 to rewrite a healthy
# tunnel (e.g. after rotating the token).
telemetry_setup_cloudflare_tunnel() {
    local domain="$1" token="$2" force="${3:-0}"

    if [ -z "$domain" ] || [ -z "$token" ]; then
        echo "❌ telemetry_setup_cloudflare_tunnel requires <domain> <token>" >&2
        return 1
    fi

    if [ "$force" != "1" ] && telemetry_cloudflare_tunnel_healthy "$domain" "$token"; then
        echo "✅ Cloudflare tunnel already up for https://${domain} — skipping reconfigure"
        echo "   (set FORCE_CF=1 to force rewrite of cloudflared unit/config)"
        return 0
    fi

    if [ "$force" = "1" ]; then
        echo "🔹 FORCE_CF=1 — reconfiguring cloudflared"
    else
        echo "🔹 Tunnel missing or unhealthy — configuring cloudflared"
    fi

    if ! command -v cloudflared >/dev/null 2>&1; then
        local arch
        arch="$(dpkg --print-architecture)"
        echo "⬇️  Installing cloudflared (${arch})..."
        curl -fsSL "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-${arch}.deb" -o /tmp/cf.deb
        dpkg -i /tmp/cf.deb
        rm -f /tmp/cf.deb
    fi

    mkdir -p /etc/cloudflared
    if systemctl is-active --quiet cloudflared; then
        systemctl stop cloudflared
    fi

    # Token-managed tunnels take routes from the Cloudflare dashboard.
    # Public Hostname → Service URL should be: http://127.0.0.1:5080
    cat > /etc/cloudflared/config.yml <<EOF
ingress:
  - hostname: "$domain"
    service: http://127.0.0.1:5080
  - service: http_status:404
EOF

    cat > /etc/systemd/system/cloudflared.service <<EOF
[Unit]
Description=Cloudflare Outbound Tunnel (telemetry host)
After=network-online.target openobserve.service
Wants=network-online.target

[Service]
Type=simple
User=root
ExecStart=/usr/local/bin/cloudflared --no-autoupdate tunnel run --token $token
Restart=always
RestartSec=5s

[Install]
WantedBy=multi-user.target
EOF
    # Unit files under /etc/systemd/system are world-readable (644) by
    # default, and this one embeds the tunnel token in ExecStart= — restrict
    # it explicitly rather than relying on the ambient umask.
    chmod 600 /etc/systemd/system/cloudflared.service

    systemctl daemon-reload
    systemctl enable cloudflared --now
    sleep 2

    if systemctl is-active --quiet cloudflared; then
        echo "✅ Cloudflare tunnel online → https://${domain} (127.0.0.1:5080)"
    else
        echo "❌ cloudflared failed to start" >&2
        journalctl -u cloudflared -n 30 --no-pager >&2 || true
        return 1
    fi
}

# Stop and disable a previously-configured tunnel. Safe no-op if cloudflared
# was never installed. Used when a user opts out of remote access after
# having had it enabled before — leaving no public domain set must actually
# take OpenObserve off the internet, not just stop reconfiguring it.
telemetry_disable_cloudflare_tunnel() {
    [ -f /etc/systemd/system/cloudflared.service ] || return 0
    if systemctl is-active --quiet cloudflared 2>/dev/null || systemctl is-enabled --quiet cloudflared 2>/dev/null; then
        echo "🔹 No public domain set — stopping Cloudflare tunnel (OpenObserve stays LAN-only)"
        systemctl disable --now cloudflared 2>/dev/null || true
    fi
}
