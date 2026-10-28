# HA 08 — In-process locks and cron jobs, made replica-safe

## Status

Proposed.

## Depends on

[01](01_replica_mode.md)

## Context

Three things serialize work inside one process today:

| Where | What it protects |
|---|---|
| `blockNoticeLocks` (`blocks.go`) | Block and unblock notices to one peer go out in order, so a block and its lift can't cross |
| `deploy/jobs/ripples-cleanup.cron` | Expired ripples swept by one job |
| `deploy/jobs/challenges-cleanup.cron` | Expired challenges swept by one job |

Peer event queues already use a DB lease (`server_mesh.delivery_claimed_at`)
and are fine.

## Change

### Per-peer block notices

Single mode keeps the `sync.Mutex`. HA uses a lease row, the same shape as
the peer-stream claim:

```sql
CREATE TABLE IF NOT EXISTS peer_notice_claims (
    peer_server_id TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    kind           TEXT NOT NULL,      -- 'block'
    claimed_at     TIMESTAMP,
    PRIMARY KEY (peer_server_id, kind)
);
```

Claim with `UPDATE … SET claimed_at = now() WHERE … AND (claimed_at IS NULL
OR claimed_at < now() - interval '1 minute')`; release by setting it NULL.
A lease, not an advisory lock, because the holder makes network calls for
up to 30 s and must not pin a pooled connection that long. A replica that
dies holding it delays the next send by at most a minute.

Behind one small interface (`peerNoticeLock`) with a mutex and a lease
implementation, chosen from `replicaMode`.

### Cron jobs

Installed on every app host in HA, so losing a host doesn't stop them.
Each job takes `pg_try_advisory_lock(hashtext('syrinx:job:<name>'))` at
start and exits quietly if it doesn't get it. The sweeps are idempotent
deletes, so even a lost lock (connection drop mid-run) is harmless.

Single mode: same binary, same lock, always granted. No branch needed.

### Left alone

`peerKeyUpdateAttempts` throttles peer key refetches per process. With N
replicas a peer may see N refetches per window instead of one. Accepted.

## Tests

- Two `Handlers` sharing a DB in HA mode: concurrent `sendOwedBlockNotices`
  for the same peer send each notice once, in order.
- Run a cleanup job twice concurrently: one does the work, the other exits
  0 without touching rows.
