#!/usr/bin/env bash
# Spin up two independent Syrinx instances (A and B) in tmux, sharing one
# postgres container but each with its own database, so the federation
# handshake can be exercised locally between two "real" servers.
#
# Usage: scripts/federation-dev.sh [up|down]

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SESSION="syrinx-federation"

# --- Instance A ---
A_DB_NAME="syrinx_a"
A_API_PORT=8081
A_SPA_PORT=5174
A_SERVER_NAME="fed-a.local"

# --- Instance B ---
B_DB_NAME="syrinx_b"
B_API_PORT=8082
B_SPA_PORT=5175
B_SERVER_NAME="fed-b.local"

# Shared postgres connection (from docker-compose.yml)
DB_HOST="localhost"
DB_PORT="5432"
DB_USER="syrinx"
DB_PASSWORD="syrinx"
DB_SSLMODE="disable"

# Dev passphrases/root export secrets — local only, never use these for
# anything real.
A_SERVER_KEY_PASSPHRASE="federation-dev-instance-a-pass"
B_SERVER_KEY_PASSPHRASE="federation-dev-instance-b-pass"
A_ROOT_EXPORT_PASSPHRASE="aaa"
B_ROOT_EXPORT_PASSPHRASE="bbb"

cmd="${1:-up}"

if [ "$cmd" = "down" ]; then
  exec "$(dirname "${BASH_SOURCE[0]}")/federation-dev-down.sh"
fi

if [ "$cmd" != "up" ]; then
  echo "Usage: $0 [up|down]"
  exit 2
fi

for port in "$A_API_PORT" "$A_SPA_PORT" "$B_API_PORT" "$B_SPA_PORT"; do
  if lsof -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
    echo "Error: port ${port} is already in use."
    exit 1
  fi
done

if tmux has-session -t "$SESSION" 2>/dev/null; then
  echo "tmux session '$SESSION' already exists. Attach with: tmux attach -t $SESSION"
  echo "Or tear it down first: $0 down"
  exit 1
fi

# 1. Start (or reuse) the shared postgres container in daemon mode.
echo "Starting shared postgres container..."
(cd "$ROOT_DIR" && docker-compose up -d db)

echo "Waiting for postgres to be healthy..."
until docker exec syrinx_db pg_isready -U "$DB_USER" >/dev/null 2>&1; do
  sleep 1
done

# 2. Create one database per instance inside the shared postgres server.
for db in "$A_DB_NAME" "$B_DB_NAME"; do
  echo "Ensuring database '$db' exists..."
  docker exec syrinx_db psql -U "$DB_USER" -tc "SELECT 1 FROM pg_database WHERE datname = '${db}'" | grep -q 1 \
    || docker exec syrinx_db psql -U "$DB_USER" -c "CREATE DATABASE ${db}"
done

# 3. Build the server binary once; both instances run the same binary with
# different env.
echo "Building syrinx binary..."
(cd "$ROOT_DIR" && mkdir -p bin && go build -o bin/syrinx .)

# common_env NAME DB_NAME API_PORT SERVER_NAME SERVER_KEY_PASSPHRASE ROOT_EXPORT_PASSPHRASE SPA_PORT
common_env() {
  local db_name="$1" api_port="$2" server_name="$3" key_pass="$4"
  cat <<EOF
export DB_HOST="$DB_HOST"
export DB_PORT="$DB_PORT"
export DB_USER="$DB_USER"
export DB_PASSWORD="$DB_PASSWORD"
export DB_NAME="$db_name"
export DB_SSLMODE="$DB_SSLMODE"
export SERVER_NAME="$server_name"
export PORT="$api_port"
export API_BASE_URL="http://localhost:${api_port}"
export ALLOWED_ORIGIN="http://localhost:${5}"
export SERVER_KEY_PASSPHRASE="$key_pass"
export SIGNUP_MODE="open"
export MAX_INVITES_PER_USER="17"
# Dev-only: federation baseUrls are plain http:// between local instances.
# Remove this once real TLS is in play — see main.go's AppConfig doc comment.
export FEDERATION_ALLOW_INSECURE_HTTP="true"
EOF
}

A_ENV="$(common_env "$A_DB_NAME" "$A_API_PORT" "$A_SERVER_NAME" "$A_SERVER_KEY_PASSPHRASE" "$A_SPA_PORT")"
B_ENV="$(common_env "$B_DB_NAME" "$B_API_PORT" "$B_SERVER_NAME" "$B_SERVER_KEY_PASSPHRASE" "$B_SPA_PORT")"

# 4. One-shot root mint per instance: run once with ROOT_KEY_EXPORT_PASSPHRASE
# set, which writes syrinx-1-<ts>.sxi.gpg and exits; then import it and start
# the server for real. Idempotent: skipped if root already exists.
mint_root_if_needed() {
  local label="$1" db_name="$2" env_block="$3" root_pass="$4"
  local has_root
  has_root=$(docker exec syrinx_db psql -U "$DB_USER" -d "$db_name" -tAc \
    "SELECT 1 FROM information_schema.tables WHERE table_name='users'" 2>/dev/null || true)
  if [ "$has_root" = "1" ]; then
    local root_count
    root_count=$(docker exec syrinx_db psql -U "$DB_USER" -d "$db_name" -tAc "SELECT count(*) FROM users WHERE id='1'" 2>/dev/null || echo 0)
    if [ "${root_count:-0}" -ge 1 ]; then
      echo "[$label] root user already present, skipping mint."
      return
    fi
  fi

  echo "[$label] minting root identity (server will exit after writing the .sxi.gpg export)..."
  ( cd "$ROOT_DIR" && eval "$env_block"; export ROOT_KEY_EXPORT_PASSPHRASE="$root_pass"; ./bin/syrinx ) || true

  local bundle
  bundle=$(ls -t "$ROOT_DIR"/syrinx-1-*.sxi.gpg 2>/dev/null | head -1)
  if [ -z "$bundle" ]; then
    echo "[$label] ERROR: expected syrinx-1-*.sxi.gpg after mint run, none found." >&2
    exit 1
  fi
  local dest="$ROOT_DIR/scripts/syrinx-$(echo "$label" | tr '[:upper:]' '[:lower:]')-$(basename "$bundle" | sed 's/^syrinx-//')"
  mv "$bundle" "$dest"
  echo "[$label] minted root, bundle: $dest"
  echo "[$label] NOTE: root export left on disk — import it yourself via the SPA /import flow"
  echo "         using the export passphrase, then continue. This script only performs the"
  echo "         one-shot mint + restart; it does not import the identity into a browser profile."
}

mint_root_if_needed "A" "$A_DB_NAME" "$A_ENV" "$A_ROOT_EXPORT_PASSPHRASE"
mint_root_if_needed "B" "$B_DB_NAME" "$B_ENV" "$B_ROOT_EXPORT_PASSPHRASE"

# 5. Launch tmux session: one window per instance, split into api + spa panes.
tmux new-session -d -s "$SESSION" -n "instance-a"
tmux send-keys -t "$SESSION:instance-a" "cd '$ROOT_DIR'; $A_ENV
./bin/syrinx" Enter
tmux split-window -t "$SESSION:instance-a" -h
tmux send-keys -t "$SESSION:instance-a.1" "cd '$ROOT_DIR/spa'; API_HOST=localhost:${A_API_PORT} npm run dev -- --host --port ${A_SPA_PORT}" Enter

tmux new-window -t "$SESSION" -n "instance-b"
tmux send-keys -t "$SESSION:instance-b" "cd '$ROOT_DIR'; $B_ENV
./bin/syrinx" Enter
tmux split-window -t "$SESSION:instance-b" -h
tmux send-keys -t "$SESSION:instance-b.1" "cd '$ROOT_DIR/spa'; API_HOST=localhost:${B_API_PORT} npm run dev -- --host --port ${B_SPA_PORT}" Enter

tmux new-window -t "$SESSION" -n "db"
tmux send-keys -t "$SESSION:db" "docker exec -it syrinx_db psql -U $DB_USER" Enter

cat <<EOF

Instance A: API http://localhost:${A_API_PORT}  SPA http://localhost:${A_SPA_PORT}
Instance B: API http://localhost:${B_API_PORT}  SPA http://localhost:${B_SPA_PORT}

Root identity bundles (if minted this run) are in scripts/syrinx-a-1-*.sxi.gpg
and scripts/syrinx-b-1-*.sxi.gpg — import each into its respective SPA via
/import using the matching export passphrase
(A: ${A_ROOT_EXPORT_PASSPHRASE}, B: ${B_ROOT_EXPORT_PASSPHRASE}).

Server key passphrases (unwrap each instance's signing key):
  A: ${A_SERVER_KEY_PASSPHRASE}
  B: ${B_SERVER_KEY_PASSPHRASE}

(These passwords are also saved in this script's source, if you need them later.)

To attach to the tmux session: tmux attach -t $SESSION
EOF
