#!/bin/bash
# ==============================================================================
# Print this server's current signing public key, armored, and nothing
# else. Requires setup.env from a prior ./setup.sh run.
#
# Usage:
#   sudo ./server-key.sh
# ==============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

KEY="$("$SCRIPT_DIR/psql.sh" -X -A -t -c "
    SELECT pk.armor
    FROM public_keys pk
    JOIN servers s ON s.signing_key = pk.id
    WHERE s.self = TRUE
")"

if [ -z "$KEY" ]; then
    echo "❌ Error: no signing key yet — has the server started since setup?" >&2
    exit 1
fi

printf '%s\n' "$KEY"
