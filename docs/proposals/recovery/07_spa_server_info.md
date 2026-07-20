# Recovery 07 — SPA server info, signup gating, server-unreachable notice

## Status

Implemented.

## Depends on

[04](04_own_identity_claim.md)–[06](06_reeds_follows_complete.md) (recovery API
exists). Signup gating additionally depends on
[invites 00](../invites/00_signup_mode.md) (`signupMode` on `/api/server/info`).

## Context

The SPA today fetches `/api/server/info` ad hoc (e.g. `authService.getServerName`)
and always shows “Sign Up” on the landing page. Recovery and signup policy both
need a shared, cached view of server state. Separately, `navigator.onLine` only
detects device connectivity — a reachable network with an unreachable or
compromised Syrinx server looks “online” until writes pile up in pending stores.

## Scope

- Add a `serverInfo` service (or equivalent) that fetches `GET /api/server/info`,
  caches the result (`id`, `name`, `recoveryMode`, `signupMode`, …), and exposes
  it to the UI (writable store / subscribe).
- Persist `serverId` in `localStorage` when info is fetched (same as today).
- Landing page: show “Sign Up” **only** when `signupMode === "open"`. Hide the
  button while info is loading (no flicker). `recoveryMode` does **not** affect
  signup visibility — operators who want closed signup during recovery set
  `SIGNUP_MODE=closed` themselves.
- Server-unreachable banner: when `navigator.onLine` is true but
  `GET /api/server/info` fails (network error, non-2xx, timeout), show a persistent
  notice distinct from the offline banner (e.g. “Cannot reach this server”).
  Clear it when a subsequent fetch succeeds. Reuse the same polling / refresh hook
  that loads server info (this endpoint is the canary).
- Mount the unreachable notice in the app shell (alongside the existing offline
  indicator).

## Non-goals

- Recovery landing copy, `/recover` route, or ledger (08+).
- Signup page invite-token handling ([invites 04](../invites/04_spa_signup_gating.md)).
- Playwright coverage (deferred).

## Design

Prefer one fetch path: layout or a top-level initializer calls
`serverInfo.refresh()` on load and on `window` `online` events. The unreachable
state is `isOnline && lastInfoFetchFailed`, not a separate health endpoint.

Signup gating copy on the landing page when hidden: none required (button absent).
Do not block “Import backup” or other CTAs.

## Test plan

- [ ] `signupMode: "open"` → Sign Up visible after info loads
- [ ] `signupMode: "closed"` → Sign Up hidden; no flash while loading
- [ ] `recoveryMode: true` with `signupMode: "open"` → Sign Up still visible
- [ ] Device online, server down → unreachable banner shown
- [ ] Device offline → existing offline banner only (not both claiming “server”)
- [ ] Info fetch succeeds again → unreachable banner clears
