#!/bin/bash
# Build OpenObserve from source for Raspberry Pi (no AES CPU feature).
# Normally invoked by telemetry/setup.sh or telemetry/update.sh — not run directly.
# https://github.com/openobserve/openobserve/issues/3910

set -euo pipefail

if [ "$EUID" -ne 0 ]; then
    echo "❌ Error: run as root (sudo ./build-openobserve-pi.sh)"
    exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=common.sh
. "$SCRIPT_DIR/common.sh"

TELEMETRY_ENV="$SCRIPT_DIR/telemetry.env"
export TELEMETRY_ENV
if [ -f "$TELEMETRY_ENV" ]; then
    # shellcheck disable=SC1090
    set -a
    # shellcheck source=/dev/null
    . "$TELEMETRY_ENV"
    set +a
fi

telemetry_run_o2_build_in_tmux
echo "✅ Pi build finished: $(telemetry_o2_pi_cached_binary)"
