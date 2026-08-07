# Recovery 08 — SPA recovery landing UX

## Status

**Superseded** by [10](10_spa_unified_restore.md).

Implemented as a recovery-mode banner + “Recover your account” CTA assuming
data might already live on this origin. That model is wrong after a domain
move: the new origin starts empty; users bring an encrypted backup. Keep this
file for history only.

## Depends on

[07](07_spa_server_info.md)

## Context

When the operator runs with `RECOVERY_MODE`, clients learn that from
`/api/server/info` (`recoveryMode`). The landing page must explain the situation
and offer a path to report local data back. A device that already holds a
normal logged-in session must not be offered recovery on that device (forfeit
rule). See [README](README.md) *Client responsibilities*.

> **Superseded note:** Do not implement further against this doc. Use
> [10](10_spa_unified_restore.md) (backup-first unified restore) and the revised
> client state model there.

## Scope

- When `recoveryMode` is true and the user is **not** logged in: adjust homepage
  copy (banner / subtitle) to explain the server is in recovery and the user
  should report their data.
- Show a primary CTA (e.g. “Recover your account”) linking to `/recover`.
- **Forfeit rule:** if `authService.isLoggedIn()` is true, do **not** show the
  recovery CTA or recovery-specific homepage messaging (user is using a live
  session on this device; recovery is for restoring onto a wiped server, not
  for multi-device use).
- While `recoveryMode` is false: no recovery banner or CTA.

## Non-goals

- `/recover` page implementation beyond a stub route if needed for the link
  (full page lands in 09).
- Import backup flow (`/import`) — out of scope for recovery slices.
- Ledger, API calls, or import-gate handling (09+).
- Playwright coverage (deferred).

## Design

Recovery messaging and signup gating are independent: signup follows
`signupMode` only (07). During recovery the homepage may show both “Sign Up”
(open mode) and “Recover your account” when not logged in.

> **Stale even for its own time:** this independence claim was reversed —
> `recoveryMode` now overrides `signupMode` and hides “Sign Up” outright (see
> the current [07](07_spa_server_info.md)). Noted here only so history isn't
> misread as still-accurate; do not implement against this doc regardless.

Logged-in users visiting `/` continue to redirect to `/reeds` as today.

## Test plan

- [ ] `recoveryMode: true`, logged out → banner + recover CTA visible
- [ ] `recoveryMode: true`, logged in → no recover CTA; redirect to app
- [ ] `recoveryMode: false` → no recovery messaging
