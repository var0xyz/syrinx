# HA 02 — Serialize schema and server-identity init

## Status

Proposed.

## Depends on

[01](01_replica_mode.md)

## Context

Every boot runs, in order: `InitDB` (all DDL), `InitServer` (the `self`
server row), `InitServerKey` (decrypt, or **mint** on an empty DB),
`maybeExportRootKey` (mint the root user on first boot), then
`requireRootUser`. None of it expects company:

- Two replicas starting together on an empty DB each mint a server key.
  Whichever commits second either fails or, worse, overwrites
  `servers.signing_key`, and the first replica serves with a key the DB no
  longer names. Two identities for one server.
- Concurrent `CREATE TABLE IF NOT EXISTS` / `CREATE INDEX IF NOT EXISTS` in
  separate transactions can fail with a unique violation on `pg_type` or
  `pg_class`; the loser crashes on boot.
- `InitServerKey` re-encrypts and rewrites the key when `SERVER_NAME` adds
  an identity; two replicas doing it at once race on the same row.

## Change

Take a session-level advisory lock on a dedicated connection for the whole
init sequence and release it before serving:

```go
conn, _ := db.Conn(ctx)
conn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtext('syrinx:boot'))`)
defer conn.ExecContext(ctx, `SELECT pg_advisory_unlock(hashtext('syrinx:boot'))`)
```

Taken in **both** modes: it costs one round trip per boot, and it also
protects a single-mode operator who runs `make run` twice by accident.

The lock spans `InitDB` through `requireRootUser`. Realtime state isn't
touched at replica boot in `ha` (the gateway owns resets,
[07](07_server_wide_reset.md)), so nothing else needs to be inside it.

First boot in `ha`: the root mint (`ROOT_KEY_EXPORT_PASSPHRASE`) writes the
export file and exits, as today. The deploy script runs it on the first
app host only, with the others stopped ([11](11_deploy_scripts.md)).

## Tests

- Start two servers against an empty DB at the same instant (test helper
  calling the boot sequence in two goroutines on two pools): exactly one
  server key, one root user, both boots succeed, both load the same key
  fingerprint.
- Same with an existing DB and a changed `SERVER_NAME`: one rewrite.
