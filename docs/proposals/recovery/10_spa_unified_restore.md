# Recovery 10 — SPA unified restore (backup-first)

## Status

Proposed.

## Depends on

[07](07_spa_server_info.md), [09](09_user_status.md).

## Supersedes

[08](08_spa_recovery_landing.md) (implemented recovery-only landing / “report
cached data” CTA). The old forfeit rule (“logged in ⇒ hide recovery”) is
replaced by the state model below.

## Context

Recovery and ordinary device restore share the same user-visible path: the new
origin has **no** IndexedDB / localStorage from the old domain. The user must
open the **old** app, create an encrypted backup, then on this origin provide
that file and the backup password. The client decrypts, probes
`POST /api/users/status`, and only then decides import vs recovery. The user
must not be asked to choose “import” vs “recover”.

## Scope

### Homepage

- When the user is **not** in a usable local session (see state model), show a
  primary CTA such as **“Already a user”** (wording flexible) that starts the
  restore flow — available whether or not `recoveryMode` is on.
- When `recoveryMode` is on, keep a short informational banner that this server
  is rebuilding; do not imply data is already on this device.
- Signup visibility still follows `signupMode` only ([07](07_spa_server_info.md)).

### Restore flow (reuse backup decrypt / write helpers from `/import`)

1. Select backup file; verify expected extension / naming (e.g. `.sxb.gz.gpg`).
2. Ask for the backup passphrase; decrypt **in memory** (do not write storage yet).
3. Extract the countersigned profile (and key material needed later) from the
   backup payload.
4. `POST /api/users/status` with that profile ([09](09_user_status.md)).
5. Branch on the response (and `recoveryMode` / local recovery state):

| Probe                         | Local recovery in progress? | Client action |
|-------------------------------|-----------------------------|---------------|
| **400**                       | —                           | Error; write nothing |
| **200** `complete`            | no                          | Write backup → normal session (import) |
| **200** `complete`            | yes                         | Clear stale local recovery state; write backup → normal session |
| **404** `unknown`             | —                           | If **not** `recoveryMode`: write nothing, explain unrecognized account. If `recoveryMode`: write backup, init recovery progress ([11](11_spa_recovery_progress.md)), start recovery ([12](12_spa_own_identity_claim.md)+) |
| **409** `ongoing`             | no                          | Write backup; init progress from scratch; start recovery (handlers are idempotent) |
| **409** `ongoing`             | yes                         | Write backup if needed; **resume** local progress ([11](11_spa_recovery_progress.md)) |

6. On recovery start, persist a small recovery run marker (e.g. `started` +
   timestamp) in `localStorage` in addition to the per-entity progress table
   in [11](11_spa_recovery_progress.md).

### Client state model

Do not treat “`userId` in localStorage” alone as “logged in / forfeit recovery”.
Derive UX from:

1. Whether identity material is present locally (after a successful write).
2. Whether a **local recovery run** is in progress (marker + incomplete progress
   table).
3. Server `recoveryMode` and the last probe status when relevant.

Mid-recovery: show recovery progress UI, not the normal app shell routes
([15](15_spa_import_gate_mirror.md)).

## Non-goals

- Implementing claim / peer / reed / follow HTTP calls (12–14).
- Progress table shape beyond initializing it (11).
- Device binding (16).
- Playwright (deferred).

## Design

One route/flow (extend or replace `/import`) shared by both outcomes. Copy talks
about restoring from a backup, never about “cached data on this device.”

Wrong passphrase / corrupt file → fail before the status call; no writes.

## Test plan

- [ ] Backup + passphrase → status called with profile from backup
- [ ] 200 → storage written; lands in normal app use; no recovery UI
- [ ] 200 with stale local recovery marker → marker cleared; import path
- [ ] 404 + `recoveryMode` false → nothing written
- [ ] 404 + `recoveryMode` true → storage written; recovery UI starts
- [ ] 409 without local progress → write + recovery from scratch
- [ ] 409 with local progress → resume
- [ ] 400 → nothing written; error shown
