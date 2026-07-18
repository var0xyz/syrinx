# Recovery 03 — `RECOVERY_MODE` boot, bookkeeping, import gate

## Status

Proposed.

## Depends on

[02](02_key_bundle_import_ops_cli.md)

## Context

Identity is already in the DB via **`ops import-identity`** (step 02). This
step wires the server process for recovery: require that identity when
`RECOVERY_MODE` is on, plus bookkeeping tables, client-visible `recoveryMode`,
and the mid-import API gate. See [README](README.md) *Phase 0*, *Import gate*,
*Who we authenticate*, Resolved.

## Scope

- Env: `RECOVERY_MODE`.
- Fold the recovery boot check into **`InitServer(recoveryMode)`**: it already
  loads `servers WHERE self = TRUE`. On `ErrNoRows`, mint as today when mode
  is off; when mode is on, **fatal** with a message to run
  `ops import-identity <bundle>` first. When the self row exists, resume:
  keep data, continue so `InitServerKey` loads the restored active key.
- Wire `main`: pass `cfg.RecoveryMode` into `InitServer`. Keep the step-01
  stale-backup warning on non-recovery boots.
- Create `unclaimed_accounts` and `ongoing_recoveries` in **`InitDB`**
  ([`db.go`](../../db.go)) with the rest of the schema.
- Import-gate middleware **installed when `RECOVERY_MODE` is on**: while the
  caller is in `ongoing_recoveries`, allow only `/api/recovery/*`,
  `/api/server/info`, `/api/server/keys/`; else `403`.
- At startup when `RECOVERY_MODE` is on, log the current `unclaimed_accounts`
  count (admin gauge).
- Extend `/server/info` with `recoveryMode`.
- Recovered users use the system normally once not in `ongoing_recoveries`.
  Username collisions remain newest-`server_signed_at` (step 04).
- Store helpers for insert/delete unclaimed/ongoing (used by later steps).
  `RegisterRoutes` lands in step 04.

## Non-goals

- Identity claim/peer/reed/follow handlers (steps 04–06).
- SPA (step 07).
- Bundle password / bundle file handling (stays in `ops` CLI).
- Identity **mismatch** handling (lives in `ops import-identity`).

## Design

Operator sequence: `ops import-identity` → start with `RECOVERY_MODE`. Resume
must not wipe already-restored user data. Server **name** comes from
`SERVER_NAME`.

Bookkeeping DDL lives in `InitDB`. Gate helpers (`IsOngoing`, path allowlist,
count/store) live in `syrinx/recovery`; `main` wires the flag, the
`InitServer` argument, the mode-on unclaimed log, and the mode-on middleware.
`ongoing_recoveries` rows are inserted once claim exists (step 04); this step
ships the table + gate logic.

## Test plan

- [ ] `RECOVERY_MODE` + empty DB (no self row) → process exits; message mentions
      `import-identity`
- [ ] After successful `ops import-identity`, `RECOVERY_MODE` boot resumes and
      loads the active signing key
- [ ] Interrupted recovery (identity already present) → resume; data preserved
- [ ] User forced into `ongoing_recoveries` → non-recovery API 403; recovery
      paths allowed (middleware present when mode on)
- [ ] `/server/info` reports `recoveryMode`
- [ ] Mode on → startup logs unclaimed count
