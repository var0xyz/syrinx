#!/bin/bash
# Cross-compile OpenObserve for Raspberry Pi (aarch64, no AES) from macOS.
#
# Produces the same artifact name the Pi install expects:
#   openobserve-vX.Y.Z-pi-arm64
#
# Installs missing deps via Homebrew (brew, zig, protobuf, node, cargo-zigbuild,
# rustup). Also ensures Xcode CLT / git / curl.
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
O2_BUILD_JOBS="${O2_BUILD_JOBS:-$(sysctl -n hw.ncpu 2>/dev/null || echo 4)}"
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

assert_macos() {
    [ "$(uname -s)" = "Darwin" ] || die "This script is for macOS (detected: $(uname -s))"
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
}

# node@20 is keg-only — put it ahead of any older system node.
link_node20() {
    local prefix
    prefix="$(brew --prefix node@20 2>/dev/null || true)"
    if [ -n "$prefix" ] && [ -x "$prefix/bin/node" ]; then
        export PATH="$prefix/bin:$PATH"
    fi
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

    info "Installing Node 20 via Homebrew..."
    brew_ensure node@20
    # Prefer linking if brew allows; otherwise PATH via brew --prefix is enough.
    brew link --overwrite --force node@20 2>/dev/null || true
    link_node20
    refresh_path
    link_node20

    command -v node >/dev/null 2>&1 || die "node still missing after brew install node@20"
    major="$(node -p "process.versions.node.split('.')[0]")"
    [ "$major" -ge 20 ] || die "Need Node 20+ (have $(node -v)). Try: brew link --overwrite --force node@20"
    ok "Node $(node -v)"
}

ensure_rust() {
    refresh_path

    if ! command -v rustup >/dev/null 2>&1; then
        info "Installing rustup via Homebrew..."
        brew_ensure rustup
        refresh_path
    fi

    if ! command -v rustup >/dev/null 2>&1; then
        info "Falling back to official rustup installer..."
        curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y --default-toolchain none
        refresh_path
    fi

    command -v rustup >/dev/null 2>&1 || die "rustup not available after install"
    prefer_rustup_bin

    # First-time rustup needs a default toolchain so cargo exists on PATH.
    if ! command -v cargo >/dev/null 2>&1; then
        info "Bootstrapping Rust toolchain via rustup..."
        rustup default stable
        refresh_path
    fi

    command -v cargo >/dev/null 2>&1 || die "cargo not available after rustup install"
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

    info "Installing cargo-zigbuild via Homebrew..."
    if brew install cargo-zigbuild; then
        refresh_path
    else
        info "brew install cargo-zigbuild failed — falling back to cargo install..."
    fi

    if ! command -v cargo-zigbuild >/dev/null 2>&1; then
        cargo install cargo-zigbuild
        refresh_path
    fi

    command -v cargo-zigbuild >/dev/null 2>&1 || die "cargo-zigbuild not available"
    ok "cargo-zigbuild $(cargo-zigbuild --version 2>/dev/null | head -1 || echo ok)"
}

ensure_deps() {
    info "Installing / verifying host dependencies (Homebrew)..."
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

    ensure_node
    ensure_rust
    ensure_cargo_zigbuild

    refresh_path
    link_node20

    command -v git >/dev/null 2>&1 || die "git missing"
    command -v curl >/dev/null 2>&1 || die "curl missing"
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
        # Preserve web/dist when SKIP_WEB=1 so npm build can be skipped.
        if [ "$SKIP_WEB" = "1" ]; then
            git -C "$SRC_DIR" clean -fdx -e web/dist
        else
            git -C "$SRC_DIR" clean -fdx
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

    # macOS/BSD sed needs '' after -i. Keep the program on one line so the
    # script file cannot be misread if edited while a long build is running.
    sed -i '' 's/default = \["gxhash"\]/default = []/' "$cargo_toml"
    sed -i '' 's/target-feature=+aes,+neon/target-feature=+neon/g' "$cargo_cfg"
    sed -i '' '/^\[target\.aarch64-unknown-linux-gnu\]/,/^\[/{/^linker = /d;}' "$cargo_cfg"

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

    info "Building web UI on Mac (embedded into the binary)..."
    (
        cd "$SRC_DIR/web"
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
    # Final link opens hundreds of .rlib inputs; macOS default fd limit
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
        cargo zigbuild --release --target "$TARGET_WITH_GLIBC"
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

main() {
    assert_macos
    echo "================================================================"
    echo "Cross-compile OpenObserve for Raspberry Pi (from macOS)"
    echo "================================================================"
    echo "Version:  ${VERSION_TAG}"
    echo "Target:   ${TARGET_WITH_GLIBC}"
    echo "Output:   ${ARTIFACT_PATH}"
    if [ -n "$DEPLOY_HOST" ]; then
        echo "Deploy:   ${DEPLOY_HOST}:${DEPLOY_PATH}/"
    fi
    echo ""

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
