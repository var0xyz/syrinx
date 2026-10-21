#!/bin/bash
# Cross-compile OpenObserve for Raspberry Pi (aarch64, no AES) from macOS,
# a Debian-based Linux (apt) or an Arch-based Linux (pacman).
#
# Produces the same artifact name the Pi install expects:
#   openobserve-vX.Y.Z-pi-arm64
#
# Installs missing deps: on macOS via Homebrew (plus Xcode CLT); on Linux via
# apt/pacman (sudo). Rust comes from rustup and cargo-zigbuild from cargo.
# Debian gets Zig and, if its Node is older than 20, Node from upstream
# tarballs under .cross-build/toolchain. Everything it installs is listed in
# .cross-build/installed-tools so uninstall-cross-compile-tools.sh can undo it.
#
# Usage:
#   ./cross-compile-openobserve-pi.sh
#   O2_VERSION=0.91.5 ./cross-compile-openobserve-pi.sh
#   SKIP_WEB=1 ./cross-compile-openobserve-pi.sh
#   DEPLOY_HOST=pi@10.0.0.50 ./cross-compile-openobserve-pi.sh
# After a successful build (no DEPLOY_HOST):
#   ./deploy-openobserve-pi.sh
#   SSH_USER=pi ./deploy-openobserve-pi.sh   # if DEPLOY_HOST is only an IP#
# https://github.com/openobserve/openobserve/issues/3910

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
O2_VERSION="${O2_VERSION:-0.91.5}"
O2_BUILD_JOBS="${O2_BUILD_JOBS:-$(nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 4)}"
# The openobserve crate alone peaks at ~22 GB in one rustc; see preflight_memory.
O2_MIN_MEMORY_GB="${O2_MIN_MEMORY_GB:-24}"
# Room on disk for .cross-build (~6 GB) plus some slack, on top of swap.
O2_MIN_BUILD_DISK_GB="${O2_MIN_BUILD_DISK_GB:-6}"
# Bookworm/Pi OS 64-bit is glibc 2.36; 2.31 covers Bullseye too.
O2_GLIBC="${O2_GLIBC:-2.31}"
# Set SKIP_WEB=1 to reuse an existing web/dist (skips npm ci/build).
SKIP_WEB="${SKIP_WEB:-0}"
TARGET="aarch64-unknown-linux-gnu"
TARGET_WITH_GLIBC="${TARGET}.${O2_GLIBC}"
OUT_DIR="${OUT_DIR:-$SCRIPT_DIR/dist}"
BUILD_ROOT="${BUILD_ROOT:-$SCRIPT_DIR/.cross-build}"
SRC_DIR="$BUILD_ROOT/openobserve-src"
DEPLOY_HOST="${DEPLOY_HOST:-}"
DEPLOY_PATH="${DEPLOY_PATH:-/var/lib/openobserve/build}"
TOOLCHAIN_DIR="$BUILD_ROOT/toolchain"
INSTALLED_TOOLS="$BUILD_ROOT/installed-tools"
# Upstream tarball versions, only used on Debian-based hosts.
ZIG_VERSION="${ZIG_VERSION:-0.13.0}"
NODE_VERSION="${NODE_VERSION:-20.18.1}"
HOST_KIND=""

version_tag() {
    case "$O2_VERSION" in
        v*) printf '%s' "$O2_VERSION" ;;
        *) printf 'v%s' "$O2_VERSION" ;;
    esac
}

VERSION_TAG="$(version_tag)"
ARTIFACT_NAME="openobserve-${VERSION_TAG}-pi-arm64"
ARTIFACT_PATH="$OUT_DIR/$ARTIFACT_NAME"

die() { echo "❌ $*" >&2; exit 1; }
info() { echo "🔹 $*"; }
ok() { echo "✅ $*"; }

detect_host() {
    case "$(uname -s)" in
        Darwin) HOST_KIND="macos" ;;
        Linux)
            if command -v pacman >/dev/null 2>&1; then
                HOST_KIND="arch"
            elif command -v apt-get >/dev/null 2>&1; then
                HOST_KIND="debian"
            else
                die "Unsupported Linux distribution: need apt-get (Debian-based) or pacman (Arch-based)"
            fi
            ;;
        *) die "Unsupported host: $(uname -s) (need macOS, Debian-based or Arch-based Linux)" ;;
    esac
}

# BSD and GNU sed disagree on -i; a backup suffix works on both.
sed_inplace() {
    sed -i.bak "$1" "$2"
    rm -f "$2.bak"
}

# Notes "<kind> <name>" for uninstall-cross-compile-tools.sh.
record_installed() {
    mkdir -p "$BUILD_ROOT"
    grep -qxF "$1 $2" "$INSTALLED_TOOLS" 2>/dev/null || printf '%s %s\n' "$1" "$2" >> "$INSTALLED_TOOLS"
}

as_root() {
    if [ "$(id -u)" -eq 0 ]; then
        "$@"
    else
        command -v sudo >/dev/null 2>&1 || die "Need root or sudo to run: $*"
        sudo -p "[sudo] password for %u on this computer (%h), to install build tools: " "$@"
    fi
}

# Prefer rustup's cargo/rustc shims over Homebrew's `rust` formula.
# brew shellenv puts /opt/homebrew/bin first; that rustc ignores rust-toolchain.toml
# and then fails with E0463 for aarch64-unknown-linux-gnu even when rustup has the target.
prefer_rustup_bin() {
    local rustup_prefix=""
    if command -v brew >/dev/null 2>&1; then
        rustup_prefix="$(brew --prefix rustup 2>/dev/null || true)"
    fi
    if [ -n "$rustup_prefix" ] && [ -d "$rustup_prefix/bin" ]; then
        export PATH="$rustup_prefix/bin:$PATH"
    fi
    if [ -d "${HOME}/.cargo/bin" ]; then
        export PATH="${HOME}/.cargo/bin:$PATH"
    fi
}

refresh_path() {
    # Homebrew prefixes (Apple Silicon + Intel) and rustup.
    export PATH="/opt/homebrew/bin:/opt/homebrew/sbin:/usr/local/bin:/usr/local/sbin:${HOME}/.cargo/bin:${PATH}"
    if command -v brew >/dev/null 2>&1; then
        # Force bash form — plain `brew shellenv` emits zsh `fpath[...]` when SHELL=zsh.
        eval "$(brew shellenv bash 2>/dev/null || brew shellenv)"
    fi
    if [ -f "${HOME}/.cargo/env" ]; then
        # shellcheck disable=SC1091
        . "${HOME}/.cargo/env"
    fi
    prefer_rustup_bin
}

ensure_homebrew() {
    refresh_path
    if command -v brew >/dev/null 2>&1; then
        ok "Homebrew $(brew --version | head -1)"
        return 0
    fi

    info "Installing Homebrew..."
    NONINTERACTIVE=1 /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
    refresh_path
    command -v brew >/dev/null 2>&1 || die "Homebrew installed but 'brew' not on PATH. Open a new shell and re-run."
    ok "Homebrew installed"
}

brew_is_installed() {
    brew list --versions "$1" >/dev/null 2>&1
}

brew_ensure() {
    local formula="$1"
    if brew_is_installed "$formula"; then
        info "brew: $formula already installed"
        return 0
    fi
    info "brew install $formula..."
    brew install "$formula"
    record_installed brew "$formula"
}

# node@20 is keg-only — put it ahead of any older system node.
# Same for the upstream tarball on Debian.
link_node20() {
    local prefix=""
    if command -v brew >/dev/null 2>&1; then
        prefix="$(brew --prefix node@20 2>/dev/null || true)"
    fi
    if [ -n "$prefix" ] && [ -x "$prefix/bin/node" ]; then
        export PATH="$prefix/bin:$PATH"
    fi
    if [ -x "$TOOLCHAIN_DIR/node/bin/node" ]; then
        export PATH="$TOOLCHAIN_DIR/node/bin:$PATH"
    fi
}

linux_arch() {
    case "$(uname -m)" in
        x86_64|amd64) printf 'x86_64' ;;
        aarch64|arm64) printf 'aarch64' ;;
        *) die "Unsupported CPU: $(uname -m)" ;;
    esac
}

# Installs only the packages that are missing, so reruns skip sudo.
debian_ensure() {
    local missing=() pkg
    for pkg in "$@"; do
        dpkg -s "$pkg" >/dev/null 2>&1 || missing+=("$pkg")
    done
    [ "${#missing[@]}" -gt 0 ] || return 0
    info "apt-get install ${missing[*]}..."
    as_root apt-get update
    as_root env DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends "${missing[@]}"
    for pkg in "${missing[@]}"; do record_installed apt "$pkg"; done
}

arch_ensure() {
    local missing=() pkg
    for pkg in "$@"; do
        pacman -Qi "$pkg" >/dev/null 2>&1 || missing+=("$pkg")
    done
    [ "${#missing[@]}" -gt 0 ] || return 0
    # A stale sync db names versions mirrors no longer have (404s), and
    # Arch doesn't support -Sy without -u, so this asks to upgrade the system.
    info "pacman -Syu ${missing[*]}..."
    as_root pacman -Syu --needed "${missing[@]}"
    for pkg in "${missing[@]}"; do record_installed pacman "$pkg"; done
}

# Fetches an upstream tarball into $TOOLCHAIN_DIR/<name>.
install_tarball() {
    local name="$1" dest="$TOOLCHAIN_DIR/$1"
    shift
    local tmp="$TOOLCHAIN_DIR/$name.tar.xz" url
    mkdir -p "$TOOLCHAIN_DIR"
    rm -rf "$dest" "$tmp"
    for url in "$@"; do
        info "Downloading $url..."
        curl -fL --proto '=https' --tlsv1.2 -o "$tmp" "$url" && break
        rm -f "$tmp"
    done
    [ -f "$tmp" ] || die "Could not download $name"
    mkdir -p "$dest"
    tar -xJf "$tmp" -C "$dest" --strip-components=1
    rm -f "$tmp"
}

install_node_tarball() {
    local arch
    arch="$(linux_arch)"
    [ "$arch" = "x86_64" ] && arch="x64"
    [ "$arch" = "aarch64" ] && arch="arm64"
    install_tarball node \
        "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-${arch}.tar.xz"
}

# Zig renamed its tarballs (arch-os) in 0.14.1; try both spellings.
ensure_zig_debian() {
    if [ -x "$TOOLCHAIN_DIR/zig/zig" ] \
        && [ "$("$TOOLCHAIN_DIR/zig/zig" version)" = "$ZIG_VERSION" ]; then
        export PATH="$TOOLCHAIN_DIR/zig:$PATH"
        return 0
    fi
    local arch base="https://ziglang.org/download/${ZIG_VERSION}"
    arch="$(linux_arch)"
    install_tarball zig \
        "$base/zig-${arch}-linux-${ZIG_VERSION}.tar.xz" \
        "$base/zig-linux-${arch}-${ZIG_VERSION}.tar.xz"
    export PATH="$TOOLCHAIN_DIR/zig:$PATH"
}

ensure_node() {
    local major=0
    if command -v node >/dev/null 2>&1; then
        major="$(node -p "process.versions.node.split('.')[0]" 2>/dev/null || echo 0)"
    fi

    if [ "$major" -ge 20 ]; then
        ok "Node $(node -v)"
        return 0
    fi

    case "$HOST_KIND" in
        macos)
            info "Installing Node 20 via Homebrew..."
            brew_ensure node@20
            # Prefer linking if brew allows; otherwise PATH via brew --prefix is enough.
            brew link --overwrite --force node@20 2>/dev/null || true
            ;;
        arch)
            info "Installing Node via pacman..."
            arch_ensure nodejs npm
            ;;
        debian)
            info "Installing Node ${NODE_VERSION} into $TOOLCHAIN_DIR..."
            install_node_tarball
            ;;
    esac
    link_node20
    refresh_path
    link_node20

    command -v node >/dev/null 2>&1 || die "node still missing after installing Node 20"
    major="$(node -p "process.versions.node.split('.')[0]")"
    [ "$major" -ge 20 ] || die "Need Node 20+ (have $(node -v) at $(command -v node))"
    ok "Node $(node -v)"
}

ensure_rust() {
    refresh_path

    if [ "$HOST_KIND" = "macos" ] && ! command -v rustup >/dev/null 2>&1; then
        info "Installing rustup via Homebrew..."
        brew_ensure rustup
        refresh_path
    fi

    if ! command -v rustup >/dev/null 2>&1; then
        info "Installing rustup via the official installer..."
        curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y --default-toolchain none
        record_installed rustup self
        refresh_path
    fi

    command -v rustup >/dev/null 2>&1 || die "rustup not available after install"
    prefer_rustup_bin

    # A fresh rustup has a cargo shim but no default toolchain behind it,
    # so test that cargo actually runs, not just that it's on PATH.
    if ! cargo --version >/dev/null 2>&1; then
        info "Bootstrapping Rust toolchain via rustup..."
        rustup default stable
        record_installed rustup-toolchain stable
        refresh_path
    fi

    cargo --version >/dev/null 2>&1 || die "cargo not available after rustup install"
    prefer_rustup_bin

    # Guard against Homebrew's `rust` formula winning on PATH (E0463 for cross targets).
    case "$(command -v rustc)" in
        */Cellar/rust/*|*/opt/rust/bin/*)
            die "Homebrew rustc is on PATH ($(command -v rustc)). Unlink it so rustup is used: brew unlink rust"
            ;;
    esac
    if rustc --version 2>/dev/null | grep -q '(Homebrew)'; then
        die "rustc is Homebrew's formula, not rustup. Fix PATH or run: brew unlink rust"
    fi

    ok "Rust ready (cargo $(cargo --version | awk '{print $2}'), rustc=$(command -v rustc))"
}

ensure_cargo_zigbuild() {
    if command -v cargo-zigbuild >/dev/null 2>&1; then
        ok "cargo-zigbuild $(cargo-zigbuild --version 2>/dev/null | head -1 || echo ok)"
        return 0
    fi

    if [ "$HOST_KIND" = "macos" ]; then
        info "Installing cargo-zigbuild via Homebrew..."
        if brew install cargo-zigbuild; then
            record_installed brew cargo-zigbuild
            refresh_path
        else
            info "brew install cargo-zigbuild failed — falling back to cargo install..."
        fi
    fi

    if ! command -v cargo-zigbuild >/dev/null 2>&1; then
        info "Installing cargo-zigbuild via cargo..."
        cargo install --locked cargo-zigbuild
        record_installed cargo cargo-zigbuild
        refresh_path
    fi

    command -v cargo-zigbuild >/dev/null 2>&1 || die "cargo-zigbuild not available"
    ok "cargo-zigbuild $(cargo-zigbuild --version 2>/dev/null | head -1 || echo ok)"
}

ensure_deps_macos() {
    ensure_homebrew

    # Xcode CLT provides git/clang; brew git as fallback.
    if ! xcode-select -p >/dev/null 2>&1; then
        info "Installing Xcode Command Line Tools (may prompt)..."
        xcode-select --install 2>/dev/null || true
        die "Install Xcode CLT when the dialog appears, then re-run this script."
    fi

    brew_ensure git
    brew_ensure curl
    brew_ensure zig
    brew_ensure protobuf
}

# The host C toolchain builds the build.rs scripts; Zig only links the target.
ensure_deps_debian() {
    debian_ensure build-essential ca-certificates curl file git pkg-config \
        protobuf-compiler tar xz-utils
    ensure_zig_debian
}

ensure_deps_arch() {
    arch_ensure base-devel ca-certificates curl file git pkgconf protobuf tar xz zig
}

ensure_deps() {
    info "Installing / verifying host dependencies ($HOST_KIND)..."
    case "$HOST_KIND" in
        macos) ensure_deps_macos ;;
        debian) ensure_deps_debian ;;
        arch) ensure_deps_arch ;;
    esac

    ensure_node
    ensure_rust
    ensure_cargo_zigbuild

    refresh_path
    link_node20

    command -v git >/dev/null 2>&1 || die "git missing"
    command -v curl >/dev/null 2>&1 || die "curl missing"
    command -v file >/dev/null 2>&1 || die "file missing"
    command -v zig >/dev/null 2>&1 || die "zig missing"
    command -v protoc >/dev/null 2>&1 || die "protoc missing"
    command -v node >/dev/null 2>&1 || die "node missing"
    command -v npm >/dev/null 2>&1 || die "npm missing"
    command -v rustup >/dev/null 2>&1 || die "rustup missing"
    command -v cargo >/dev/null 2>&1 || die "cargo missing"
    command -v cargo-zigbuild >/dev/null 2>&1 || die "cargo-zigbuild missing"

    ok "Dependencies ready (zig=$(zig version), node=$(node -v), protoc=$(protoc --version 2>/dev/null | awk '{print $2}'))"
}

fetch_source() {
    mkdir -p "$BUILD_ROOT"
    if [ -d "$SRC_DIR/.git" ]; then
        info "Updating OpenObserve source → ${VERSION_TAG}..."
        git -C "$SRC_DIR" fetch --tags origin
        git -C "$SRC_DIR" checkout -f "$VERSION_TAG"
        # Keep target/ so a rerun reuses compiled crates (an hour on small
        # machines); keep web/dist when SKIP_WEB=1 so npm build can be skipped.
        if [ "$SKIP_WEB" = "1" ]; then
            git -C "$SRC_DIR" clean -fdx -e target -e web/dist
        else
            git -C "$SRC_DIR" clean -fdx -e target
        fi
    else
        info "Cloning OpenObserve ${VERSION_TAG}..."
        rm -rf "$SRC_DIR"
        git clone --depth 1 --branch "$VERSION_TAG" \
            https://github.com/openobserve/openobserve.git "$SRC_DIR"
    fi
}

# OpenObserve pins a nightly in rust-toolchain.toml. The cross target must be
# installed for THAT toolchain (not just the default), or rustc fails with E0463.
ensure_rust_target_for_source() {
    local toolchain_file="$SRC_DIR/rust-toolchain.toml"
    local channel=""

    [ -f "$toolchain_file" ] || die "Missing $toolchain_file"

    channel="$(sed -n 's/^channel *= *"\([^"]*\)".*/\1/p' "$toolchain_file" | head -1)"
    [ -n "$channel" ] || die "Could not parse channel from $toolchain_file"

    if ! rustup toolchain list | awk '{print $1}' | grep -q "^${channel}-"; then
        record_installed rustup-toolchain "$channel"
    fi
    info "Installing Rust toolchain ${channel} (from rust-toolchain.toml)..."
    rustup toolchain install "$channel"
    info "Adding target ${TARGET} to ${channel}..."
    rustup target add "$TARGET" --toolchain "$channel"
    # Also add to active default — harmless and helps cargo-zigbuild edge cases.
    rustup target add "$TARGET" 2>/dev/null || true

    (
        cd "$SRC_DIR"
        info "Active toolchain: $(rustup show active-toolchain 2>/dev/null || rustc --version)"
        rustup target list --installed --toolchain "$channel" | grep -qx "$TARGET" \
            || die "Target $TARGET not installed for toolchain $channel"
    )
    ok "Rust target ${TARGET} ready on ${channel}"
}

# Pi CPUs lack AES — same patches as telemetry/common.sh (GH #3910).
# Also drop the hardcoded aarch64-linux-gnu-gcc linker so cargo-zigbuild can link.
patch_for_pi() {
    info "Patching source for Raspberry Pi (no AES / no gxhash)..."
    local cargo_toml="$SRC_DIR/src/config/Cargo.toml"
    local cargo_cfg="$SRC_DIR/.cargo/config.toml"

    [ -f "$cargo_toml" ] || die "Missing $cargo_toml"
    [ -f "$cargo_cfg" ] || die "Missing $cargo_cfg"

    # Keep each program on one line so the script file cannot be misread
    # if edited while a long build is running.
    sed_inplace 's/default = \["gxhash"\]/default = []/' "$cargo_toml"
    sed_inplace 's/target-feature=+aes,+neon/target-feature=+neon/g' "$cargo_cfg"
    sed_inplace '/^\[target\.aarch64-unknown-linux-gnu\]/,/^\[/{/^linker = /d;}' "$cargo_cfg"

    # Ensure rustflags stay Pi-safe even if upstream layout changes.
    if ! grep -q 'target-feature=+neon' "$cargo_cfg"; then
        cat >> "$cargo_cfg" <<'EOF'

[target.aarch64-unknown-linux-gnu]
rustflags = ["-C", "target-feature=+neon"]
EOF
    fi

    ok "Patched Cargo.toml + .cargo/config.toml"
}

build_web() {
    if [ "$SKIP_WEB" = "1" ] && [ -f "$SRC_DIR/web/dist/index.html" ]; then
        ok "Skipping web UI build (SKIP_WEB=1, found $SRC_DIR/web/dist)"
        return 0
    fi
    if [ "$SKIP_WEB" = "1" ]; then
        die "SKIP_WEB=1 but $SRC_DIR/web/dist/index.html is missing — build once without SKIP_WEB"
    fi

    info "Building web UI locally (embedded into the binary)..."
    (
        cd "$SRC_DIR/web"
        # Upstream's bundle visualizer opens a browser tab on every build.
        sed_inplace 's/open: true,/open: false,/' vite.config.ts
        export NODE_OPTIONS="${NODE_OPTIONS:---max-old-space-size=8192}"
        npm ci
        npx --yes update-browserslist-db@latest
        npm run build
    )
    [ -f "$SRC_DIR/web/dist/index.html" ] || die "web/dist missing after npm run build"
    ok "Web UI built"
}

build_server() {
    info "Cross-compiling OpenObserve ${VERSION_TAG} → ${TARGET_WITH_GLIBC} (jobs=${O2_BUILD_JOBS})..."
    # Final link opens hundreds of .rlib inputs; the default fd limit
    # (often 256–1024) makes zig fail with ProcessFdQuotaExceeded.
    local soft
    soft="$(ulimit -n 2>/dev/null || echo 0)"
    if [ "${soft}" != "unlimited" ] && [ "${soft:-0}" -lt 10240 ] 2>/dev/null; then
        ulimit -n 10240 2>/dev/null \
            || ulimit -n 4096 2>/dev/null \
            || info "Could not raise ulimit -n (now ${soft}); link may hit ProcessFdQuotaExceeded"
    fi
    (
        cd "$SRC_DIR"
        export CARGO_BUILD_JOBS="$O2_BUILD_JOBS"
        # Do NOT set CC_/CXX_ to raw `zig cc` here. The `cc` crate appends
        # `--target=aarch64-unknown-linux-gnu`, which Zig rejects
        # (UnknownOperatingSystem). `cargo zigbuild` installs wrappers that
        # rewrite that triple (see cargo-zigbuild zig cc).
        unset CC_aarch64_unknown_linux_gnu CXX_aarch64_unknown_linux_gnu \
            CFLAGS_aarch64_unknown_linux_gnu CXXFLAGS_aarch64_unknown_linux_gnu \
            AR_aarch64_unknown_linux_gnu
        # Cargo keeps finished crates, so a rerun resumes where this stopped.
        cargo zigbuild --release --target "$TARGET_WITH_GLIBC" || die "Build failed. \
If rustc was killed (SIGKILL), it ran out of memory: the openobserve crate \
alone peaks at ~22 GB, so add swap until RAM+swap is ~${O2_MIN_MEMORY_GB} GB."
    )

    local bin="$SRC_DIR/target/${TARGET}/release/openobserve"
    [ -x "$bin" ] || die "Binary not found at $bin"
    ok "Server binary: $bin ($(du -h "$bin" | awk '{print $1}'))"
}

package_artifact() {
    local bin="$SRC_DIR/target/${TARGET}/release/openobserve"
    mkdir -p "$OUT_DIR"
    cp "$bin" "$ARTIFACT_PATH"
    chmod 755 "$ARTIFACT_PATH"
    # Also write a small sidecar so the Pi knows the version.
    printf '%s\n' "$VERSION_TAG" > "${ARTIFACT_PATH}.version"
    file "$ARTIFACT_PATH" || true
    ok "Artifact: $ARTIFACT_PATH"
}

deploy_to_pi() {
    [ -n "$DEPLOY_HOST" ] || return 0
    local deploy_script="$SCRIPT_DIR/deploy-openobserve-pi.sh"
    [ -x "$deploy_script" ] || die "Missing deploy script: $deploy_script"
    DEPLOY_HOST="$DEPLOY_HOST" \
        DEPLOY_PATH="$DEPLOY_PATH" \
        O2_VERSION="$O2_VERSION" \
        OUT_DIR="$OUT_DIR" \
        ARTIFACT="$ARTIFACT_PATH" \
        "$deploy_script"
}

# Too little memory gets rustc OOM-killed on Linux, and on macOS fills the
# disk with swap until a watchdog panic reboots the machine, so check first.
preflight_memory() {
    local ram_gb swap_gb=0 disk_gb have_gb need_gb what
    disk_gb="$(df -Pk "$SCRIPT_DIR" | awk 'NR == 2 {print int($4 / 1048576)}')"
    if [ "$HOST_KIND" = "macos" ]; then
        ram_gb="$(( $(sysctl -n hw.memsize) / 1073741824 ))"
        # macOS swaps to the same disk and grows swap on demand.
        have_gb=$((ram_gb + disk_gb))
        need_gb=$((O2_MIN_MEMORY_GB + O2_MIN_BUILD_DISK_GB))
        what="RAM ${ram_gb} GB + free disk ${disk_gb} GB = ${have_gb} GB"
    else
        ram_gb="$(awk '/^MemTotal:/ {print int($2 / 1048576)}' /proc/meminfo)"
        swap_gb="$(awk '/^SwapTotal:/ {print int($2 / 1048576)}' /proc/meminfo)"
        have_gb=$((ram_gb + swap_gb))
        need_gb="$O2_MIN_MEMORY_GB"
        what="RAM ${ram_gb} GB + swap ${swap_gb} GB = ${have_gb} GB"
        [ "$disk_gb" -ge "$O2_MIN_BUILD_DISK_GB" ] || {
            have_gb=0
            what="$what, but only ${disk_gb} GB of free disk (need ${O2_MIN_BUILD_DISK_GB} GB)"
        }
    fi

    if [ "$have_gb" -ge "$need_gb" ]; then
        ok "Memory preflight: $what (need ${need_gb} GB)"
        return 0
    fi

    echo "⚠️  Memory preflight: $what, need ${need_gb} GB." >&2
    echo "   One rustc peaks at ~22 GB building the openobserve crate." >&2
    if [ "$HOST_KIND" = "macos" ]; then
        echo "   macOS swaps to this disk; when it can't grow swap it panics and reboots." >&2
        echo "   Free up ~$((need_gb - have_gb)) GB of disk first." >&2
    else
        echo "   Linux's OOM killer will SIGKILL rustc. Add ~$((need_gb - have_gb)) GB of swap first." >&2
    fi
    if [ -t 0 ]; then
        local answer
        read -r -p "   Build anyway? [y/N] " answer
        case "$answer" in
            y|Y|yes|YES) return 0 ;;
        esac
    fi
    die "Not enough memory to build (a lower O2_MIN_MEMORY_GB, now ${O2_MIN_MEMORY_GB}, skips this check)"
}

main() {
    detect_host
    echo "================================================================"
    echo "Cross-compile OpenObserve for Raspberry Pi (from ${HOST_KIND})"
    echo "================================================================"
    echo "Version:  ${VERSION_TAG}"
    echo "Target:   ${TARGET_WITH_GLIBC}"
    echo "Output:   ${ARTIFACT_PATH}"
    if [ -n "$DEPLOY_HOST" ]; then
        echo "Deploy:   ${DEPLOY_HOST}:${DEPLOY_PATH}/"
    fi
    echo ""

    preflight_memory
    ensure_deps
    fetch_source
    ensure_rust_target_for_source
    patch_for_pi
    build_web
    build_server
    package_artifact
    deploy_to_pi

    echo ""
    echo "------------------------------------------------------------------"
    echo "Done. Artifact: $ARTIFACT_PATH"
    if [ -z "$DEPLOY_HOST" ]; then
        echo "Deploy to the Pi with:"
        echo "  $SCRIPT_DIR/deploy-openobserve-pi.sh"
        echo "(prompts for the Pi IP; skips scp if the binary is already there)"
    fi
    echo "------------------------------------------------------------------"
}

main "$@"
exit 0
