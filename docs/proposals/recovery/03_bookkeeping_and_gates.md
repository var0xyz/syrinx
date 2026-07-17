# Recovery 03 — `RECOVERY_MODE` boot, bookkeeping, import gate, flags

## Status

Proposed.

## Depends on

[02](02_key_bundle_import_ops_cli.md)

## Context

Identity is already in the DB via **`ops import-identity`** (step 02). This
step wires the server process for recovery: require that identity when
`RECOVERY_MODE` is on (no bundle file, no password prompt), plus bookkeeping
tables, client-visible flags, and the mid-import API gate. See
[README](README.md) *Phase 0*, *Import gate*, *Who we authenticate*, Resolved.

## Scope

- Env: `RECOVERY_MODE` only (no `RECOVERY_KEY_BUNDLE`).
- When `RECOVERY_MODE` is on:
  - **Self identity present** → resume: keep data, ensure recovery tables,
    continue so `InitServerKey` loads the restored active key.
  - **No self identity** → **fatal** exit with a message to run
    `ops import-identity <bundle>` first.
- Wire `main`: when `RECOVERY_MODE`, skip naive “mint new identity”
  `InitServer`; require the imported self row. Keep the step-01 stale-backup
  warning on non-recovery boots.
- Create `unclaimed_accounts` and `ongoing_recoveries` (on entering recovery /
  via `EnsureRecoveryTables`).
- `recovery.IsOngoing` + middleware: while `RECOVERY_MODE` **and** user in
  `ongoing_recoveries`, allow only `/api/recovery/*`, `/api/server/info`,
  `/api/server/keys/`; else `403`.
- Extend `/server/info`: `recoveryMode`, `signupsEnabled`.
- Env `SIGNUPS_ENABLED` (default true); `POST /users/signup` returns `403`
  when false. **No global write freeze** — recovered users use the system
  normally once not in `ongoing_recoveries`.
- Startup warning when `!RECOVERY_MODE` and `unclaimed_accounts` non-empty.
- Store helpers for insert/delete unclaimed/ongoing (used by later steps);
  no claim HTTP yet. Optionally register an empty `RegisterRoutes` stub or
  wait until step 04.

## Non-goals

- No identity claim/peer/reed/follow handlers.
- No SPA.
- No bundle password prompt and no bundle file path in the server process.
- Identity **mismatch** handling lives in `ops import-identity`, not here.
- No `recovery_started_at` column (removed from the design).

## Design

Operator sequence: `ops import-identity` → start with `RECOVERY_MODE`. Resume
must not wipe already-restored user data. Server **name** comes from
`SERVER_NAME`.

All SQL and `IsOngoing` / path helpers in `syrinx/recovery`. Main middleware
calls into the package. `ongoing_recoveries` rows are only inserted once claim
exists (step 04); this step can still ship the table + gate logic.

## Test plan

- [ ] `RECOVERY_MODE` + empty DB (no self row) → process exits; message mentions
      `import-identity`
- [ ] After successful `ops import-identity`, `RECOVERY_MODE` boot resumes and
      loads the active signing key
- [ ] Interrupted recovery (identity already present) → resume; data preserved
- [ ] Server process never requires a bundle path or password env
- [ ] `SIGNUPS_ENABLED=false` → signup 403; authenticated profile/reed writes still work
- [ ] User forced into `ongoing_recoveries` → non-recovery API 403; recovery paths allowed
- [ ] `/server/info` reflects flags
- [ ] Mode off + unclaimed rows → non-fatal startup warning
