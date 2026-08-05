# Recovery 10 — SPA unified restore (backup-first)

## Status

Implemented.

## Depends on

[07](07_spa_server_info.md), [09](09_user_status.md).

## Supersedes

[08](08_spa_recovery_landing.md) (implemented recovery-only landing / “report
cached data” CTA). The old forfeit rule (“logged in ⇒ hide recovery”) is
replaced by the state model below.

## Context

Recovery and ordinary device restore share the same backup file and decrypt
path, but the client treats **import** and **recovery** as two resumable
phases with separate local markers. The new origin has **no** IndexedDB /
localStorage from the old domain. The user opens the **old** app, creates an
encrypted backup, then on this origin provides that file and the backup password
at **`/import`**. The client decrypts, probes `POST /api/users/status`, writes
storage when allowed, and either finishes import or hands off to **`/recovery`**.
The user must not be asked to choose “import” vs “recover”.

## Scope

### Homepage

- When the user is **not** in a usable local session (see state model), show a
  primary CTA such as **“Already a user”** (wording flexible) that starts the
  import flow at `/import` — available whether or not `recoveryMode` is on.
- When `recoveryMode` is on, keep a short informational banner that this server
  is rebuilding; do not imply data is already on this device.
- Signup visibility still follows `signupMode` only ([07](07_spa_server_info.md)).
- On load, resume an interrupted restore: mid-import → `/import`; import done
  but recovery pending → `/recovery`.

### Import flow (`/import`)

1. Persist **`importRun`** marker (`started` + timestamp) when import begins.
2. Select backup file; verify expected extension / naming (e.g. `syrinx-….sxb.gpg`).
3. Ask for the backup passphrase; decrypt **in memory** (do not write storage yet).
4. Extract the countersigned profile (and key material needed later) from the
   backup payload.
5. `POST /api/users/status` with that profile ([09](09_user_status.md)).
6. Branch on the response (and `recoveryMode` / local recovery state):

| Probe              | Local recovery started? | Client action                                                                       |
|--------------------|-------------------------|-------------------------------------------------------------------------------------|
| **400**            | —                       | Error; write nothing; import stays incomplete                                       |
| **200** `complete` | no                      | Write backup; clear any stale recovery marker; **complete import** → normal session |
| **200** `complete` | yes                     | Clear stale recovery marker; write backup; **complete import** → normal session     |
| **404** `unknown`  | —                       | If **not** `recoveryMode`: write nothing, explain unrecognized account. If `recoveryMode`: write backup → hand off to recovery (below) |
| **409** `ongoing`  | no                      | Write backup → hand off to recovery (below)                                         |
| **409** `ongoing`  | yes                     | Write backup if needed; resume local recovery marker → hand off to recovery (below) |

**Hand off to recovery** (404 + `recoveryMode`, or 409 `ongoing`):

1. Write backup to local storage.
2. **Start recovery** — persist `recoveryRun` marker (`started` + timestamp)
   and initialize the progress ledger ([11](11_spa_recovery_progress.md)).
3. **Then complete import** — mark `importRun` completed. Ordering matters: if
   the process is interrupted after backup write, local state must show import
   finished and recovery pending — not a false “all done” session.
4. Redirect to **`/recovery`**.

7. When import finishes without recovery (`200`), mark **`importRun`** completed
   and show import success (links to normal app routes).

Wrong passphrase / corrupt file → fail before the status call; no writes; import
marker remains incomplete so the user can retry.

### Recovery flow (`/recovery`)

- Assumes **import is complete** (backup already on device). If not, redirect
  to `/import`.
- Runs claim / peer / reed / follow / complete work ([12](12_spa_own_identity_claim.md)–[14](14_spa_reeds_follows_complete.md)) using the progress ledger ([11](11_spa_recovery_progress.md)).
- On successful recovery: mark **`recoveryRun`** completed, then show recovery
  success and allow normal app routes.
- `/recover` redirects to `/recovery` for old links.

### Client state model

Do not treat “`userId` in localStorage” alone as “logged in”. Derive UX from
**local markers only** (no network for session checks):

| Marker | Meaning |
|--------|---------|
| `importRun` started, not completed | Mid-import — resume at `/import` |
| `importRun` completed | Backup restore phase finished |
| `recoveryRun` started, not completed | Mid-recovery — resume at `/recovery` |
| `recoveryRun` completed | Recovery phase finished |

**Usable session** (`isLoggedIn`): identity material present, import not
mid-run, recovery not mid-run. Legacy sessions with `userId` but no markers
still count as logged in.

Mid-recovery: show recovery UI at `/recovery`, not the normal app shell routes
([15](15_spa_import_gate_mirror.md)).

## Non-goals

- Implementing claim / peer / reed / follow HTTP calls (12–14).
- Progress table shape beyond initializing it (11).
- Device binding (17).
- Playwright (deferred).

## Design

Two routes: **`/import`** (backup restore + status probe + handoff) and
**`/recovery`** (server-side recovery work). Copy talks about restoring from a
backup, never about “cached data on this device.”

## Test plan

- [ ] Backup + passphrase → status called with profile from backup
- [ ] Import start persists `importRun` marker
- [ ] 200 → storage written; import completed; lands in normal app use
- [ ] 200 with stale recovery marker → marker cleared; import completed
- [ ] 404 + `recoveryMode` false → nothing written; import incomplete
- [ ] 404 + `recoveryMode` true → storage written; recovery started; import
  completed; redirect `/recovery`
- [ ] 409 without local recovery → write; recovery started; import completed;
  redirect `/recovery`
- [ ] 409 with local recovery → write if needed; resume; import completed;
  redirect `/recovery`
- [ ] Interrupted mid-import → resume at `/import`
- [ ] Import complete + recovery started → resume at `/recovery`
- [ ] Recovery complete → usable session; `/reeds` accessible
- [ ] 400 → nothing written; error shown
