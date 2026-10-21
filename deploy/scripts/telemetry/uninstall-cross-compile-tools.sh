#!/bin/bash
# Undo what cross-compile-openobserve-pi.sh installed on this machine.
#
# Removes only the tools listed in .cross-build/installed-tools (the ones the
# cross-compile script installed itself; anything already present is left
# alone), then deletes .cross-build (source, build cache, Debian tarballs).
# Packages something else now depends on are skipped with a warning.
#
# Usage:
#   ./uninstall-cross-compile-tools.sh
#   ./uninstall-cross-compile-tools.sh --yes    # don't ask for confirmation
#   ./uninstall-cross-compile-tools.sh --dist   # also delete dist/ artifacts

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
OUT_DIR="${OUT_DIR:-$SCRIPT_DIR/dist}"
BUILD_ROOT="${BUILD_ROOT:-$SCRIPT_DIR/.cross-build}"
INSTALLED_TOOLS="$BUILD_ROOT/installed-tools"
ASSUME_YES=0
REMOVE_DIST=0

die() { echo "❌ $*" >&2; exit 1; }
info() { echo "🔹 $*"; }
ok() { echo "✅ $*"; }
warn() { echo "⚠️  $*" >&2; }

for arg in "$@"; do
    case "$arg" in
        --yes|-y) ASSUME_YES=1 ;;
        --dist) REMOVE_DIST=1 ;;
        *) die "Unknown argument: $arg" ;;
    esac
done

as_root() {
    if [ "$(id -u)" -eq 0 ]; then
        "$@"
    else
        command -v sudo >/dev/null 2>&1 || die "Need root or sudo to run: $*"
        sudo "$@"
    fi
}

export PATH="/opt/homebrew/bin:/usr/local/bin:${HOME}/.cargo/bin:${PATH}"

BREW=() APT=() PACMAN=() CARGO=() TOOLCHAINS=() RUSTUP_SELF=0
if [ -f "$INSTALLED_TOOLS" ]; then
    while read -r kind name; do
        case "$kind" in
            brew) BREW+=("$name") ;;
            apt) APT+=("$name") ;;
            pacman) PACMAN+=("$name") ;;
            cargo) CARGO+=("$name") ;;
            rustup-toolchain) TOOLCHAINS+=("$name") ;;
            rustup) RUSTUP_SELF=1 ;;
            *) warn "Ignoring unknown entry: $kind $name" ;;
        esac
    done < "$INSTALLED_TOOLS"
fi

echo "This will remove:"
[ "$RUSTUP_SELF" -eq 1 ] && echo "  rustup with ~/.rustup and ~/.cargo (rustup self uninstall)"
if [ "$RUSTUP_SELF" -eq 0 ]; then
    [ "${#CARGO[@]}" -gt 0 ] && echo "  cargo:  ${CARGO[*]}"
    [ "${#TOOLCHAINS[@]}" -gt 0 ] && echo "  rustup toolchains: ${TOOLCHAINS[*]}"
fi
[ "${#BREW[@]}" -gt 0 ] && echo "  brew:   ${BREW[*]}"
[ "${#APT[@]}" -gt 0 ] && echo "  apt:    ${APT[*]}"
[ "${#PACMAN[@]}" -gt 0 ] && echo "  pacman: ${PACMAN[*]}"
[ -d "$BUILD_ROOT" ] && echo "  $BUILD_ROOT"
[ "$REMOVE_DIST" -eq 1 ] && [ -d "$OUT_DIR" ] && echo "  $OUT_DIR"
if [ ! -f "$INSTALLED_TOOLS" ]; then
    echo "  (no $INSTALLED_TOOLS: no installed tools on record)"
fi

if [ "$ASSUME_YES" -ne 1 ]; then
    read -r -p "Continue? [y/N] " answer
    case "$answer" in
        y|Y|yes|YES) ;;
        *) echo "Aborted."; exit 0 ;;
    esac
fi

# rustup self uninstall takes its toolchains and cargo binaries with it.
if [ "$RUSTUP_SELF" -eq 1 ]; then
    if command -v rustup >/dev/null 2>&1; then
        info "rustup self uninstall..."
        rustup self uninstall -y || warn "rustup self uninstall failed"
    fi
else
    for name in ${CARGO[@]+"${CARGO[@]}"}; do
        info "cargo uninstall $name..."
        cargo uninstall "$name" || warn "Could not uninstall $name with cargo"
    done
    for name in ${TOOLCHAINS[@]+"${TOOLCHAINS[@]}"}; do
        info "rustup toolchain uninstall $name..."
        rustup toolchain uninstall "$name" || warn "Could not uninstall toolchain $name"
    done
fi

# brew refuses to uninstall a formula another formula depends on.
for name in ${BREW[@]+"${BREW[@]}"}; do
    info "brew uninstall $name..."
    brew uninstall "$name" || warn "Skipped $name (still needed, or already gone)"
done

# apt-get remove also removes reverse dependencies, so skip any package
# whose removal would take something else with it.
for name in ${APT[@]+"${APT[@]}"}; do
    dpkg -s "$name" >/dev/null 2>&1 || continue
    removals="$(apt-get -s remove "$name" 2>/dev/null | grep -c '^Remv ' || true)"
    if [ "$removals" -gt 1 ]; then
        warn "Skipped $name: removing it would also remove $((removals - 1)) other package(s)"
        continue
    fi
    info "apt-get remove $name..."
    as_root env DEBIAN_FRONTEND=noninteractive apt-get remove -y "$name" \
        || warn "Could not remove $name"
done

# pacman refuses to remove a package something else requires.
for name in ${PACMAN[@]+"${PACMAN[@]}"}; do
    pacman -Qi "$name" >/dev/null 2>&1 || continue
    info "pacman -Rns $name..."
    as_root pacman -Rns --noconfirm "$name" || warn "Skipped $name (required by another package)"
done

if [ -d "$BUILD_ROOT" ]; then
    info "Removing $BUILD_ROOT..."
    rm -rf "$BUILD_ROOT"
fi
if [ "$REMOVE_DIST" -eq 1 ] && [ -d "$OUT_DIR" ]; then
    info "Removing $OUT_DIR..."
    rm -rf "$OUT_DIR"
fi

ok "Cross-compile build tools removed"
