#!/usr/bin/env bash
# Tear down the two-instance federation dev setup started by
# scripts/federation-dev.sh: kills the tmux session, any stray
# syrinx/vite processes left on the federation ports, drops the
# per-instance databases, and deletes the minted root key backups —
# a clean slate for the next run.
#
# The shared syrinx_db postgres container is left running — it's the same
# container normal dev.sh uses.
#
# Usage: scripts/federation-dev-down.sh

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SESSION="syrinx-federation"

A_DB_NAME="syrinx_a"
B_DB_NAME="syrinx_b"
A_API_PORT=8081
A_SPA_PORT=5174
B_API_PORT=8082
B_SPA_PORT=5175

DB_USER="syrinx"

echo "Killing tmux session '$SESSION'..."
tmux kill-session -t "$SESSION" 2>/dev/null || echo "  (no such session)"

# tmux kill-session takes down the pane process groups, but if panes were
# ever detached from tmux (or the session died uncleanly) a stray
# ./bin/syrinx or vite dev process can be left bound to a federation port.
for port in "$A_API_PORT" "$A_SPA_PORT" "$B_API_PORT" "$B_SPA_PORT"; do
  pids=$(lsof -tiTCP:"$port" -sTCP:LISTEN 2>/dev/null || true)
  if [ -n "$pids" ]; then
    echo "Killing stray process(es) on port ${port}: $pids"
    kill $pids 2>/dev/null || true
  fi
done

if docker exec syrinx_db pg_isready -U "$DB_USER" >/dev/null 2>&1; then
  for db in "$A_DB_NAME" "$B_DB_NAME"; do
    echo "Dropping database '$db' (if it exists)..."
    docker exec syrinx_db psql -U "$DB_USER" -c \
      "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '${db}' AND pid <> pg_backend_pid()" \
      >/dev/null 2>&1 || true
    docker exec syrinx_db psql -U "$DB_USER" -c "DROP DATABASE IF EXISTS ${db}" >/dev/null
  done
else
  echo "syrinx_db not reachable — skipping database drop (nothing to clean up there)."
fi

# Root export bundles minted by federation-dev-up.sh (syrinx-a-1-*.sxi.gpg /
# syrinx-b-1-*.sxi.gpg), plus any unlabeled syrinx-1-*.sxi.gpg left over from
# a run before instance-labeled filenames existed.
shopt -s nullglob
bundles=("$ROOT_DIR"/scripts/syrinx-a-1-*.sxi.gpg "$ROOT_DIR"/scripts/syrinx-b-1-*.sxi.gpg "$ROOT_DIR"/scripts/syrinx-1-*.sxi.gpg "$ROOT_DIR"/syrinx-1-*.sxi.gpg)
shopt -u nullglob
if [ "${#bundles[@]}" -gt 0 ]; then
  echo "Deleting root key backups: ${bundles[*]}"
  rm -f "${bundles[@]}"
else
  echo "No root key backups found to delete."
fi

echo "Done. syrinx_db container left running for normal dev use."
