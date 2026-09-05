# Invites 06 — Stranded-signup reclaim

## Status

Proposed — tentative, not yet reviewed for implementation order.

## Depends on

02 (lifecycle API), 03 (signup consume)

## Context

An invite's username and secret are both burned the instant signup commits
(`invites.Store.MarkClaimed` runs in the same transaction as the `users`
INSERT — [services.go:605](../../services.go)). If the client never
completes its first authenticated round trip after that — closed tab,
crashed import, network death mid-flow — the result is a permanently
claimed invite and a permanently taken username with no user behind either
one able to do anything about it. The inviter sees "claimed" forever; the
invitee, if they retry, hits "username taken" on a account they can't get
back into.

This proposal adds a signed liveness attestation ("ready") the client sends
once it has actually reached a working, authenticated state, and a
reclaim path the *inviter* can use when that signal never arrives.

## Scope

- `ReadyCert`: a signed, server-countersigned attestation that a given user
  ID's active key is alive and authenticated, minted once per account.
- Server storage of the ready fact (durable — must survive the same DB wipe
  that seeds the rest of the identity graph).
- A reclaim mutation: inviter-initiated, only on invites they created,
  only when claimed and not-ready. Hard-deletes the stranded user and
  resets the invite to unclaimed with its original secret intact.
- SPA: profile-page-visit trigger for the ready send; invites page
  affordance for "claimed but not confirmed → reclaim".

## Non-goals

- Any change to the invitee's experience if they *do* eventually get
  through — this is purely a recovery path for the inviter when they don't.
- Automatic/timed reclaim. No cron, no TTL. The inviter decides when to act,
  same as revoking an unused invite today.
- Multi-device dedupe of the ready signal — syrinx has no multi-device
  sync (one active device per user), so "has this device sent ready" is
  the only state that matters.
- Reusing `AccountRemoval` — that path is self-initiated, soft (tombstone,
  keeps keys/reeds referenceable for peer fanout), and deliberately does
  **not** free the username. This is a different operation: third-party
  initiated, hard delete, username freed.

## Actors

- **Invitee** — the person completing signup. Their client sends the ready
  cert; they otherwise do nothing new.
- **Inviter** — sees "claimed, unconfirmed" on their own invites list,
  decides whether to reclaim.

## Design

### `ReadyCert`

Same `UserSignature` + `ServerSignature` nesting as every other signed
resource in this codebase (`db.go`'s `KeyRevocation`, `ReedRemoval`,
`AccountRemoval`):

```go
type ReadyCert struct {
	Type            string          `json:"type"` // "ready"
	ServerID        string          `json:"serverID"`
	UserID          string          `json:"userID"`
	UserSignature   UserSignature   `json:"userSignature"`
	ServerSignature ServerSignature `json:"serverSignature"`
}
```

Payload to sign is minimal on purpose (locked decision, see below):
`serverID`, `userID`, `signedAt` — nothing else. It asserts one fact only:
"this key, controlled by this user, is alive as of this time." It does
**not** bind the invite id; the row this confirms is found by `userID`
alone (a user has at most one active signup to confirm, ever).

Mirrors the existing pattern:

- TS: `buildReadyUserPayload(serverID, userID, signedAt)` /
  `buildReadyServerPayload(...)` in `signing.ts`, both delegating to
  `BytesToSign`.
- Go: `BuildReadyUserPayload` / `BuildReadyServerPayload` in
  `identity/identity.go`, same delegation.
- Verify: handler loads the caller's active `PublicKey`, rejects if
  revoked, calls `crypto.VerifySignature`, then countersigns — same shape
  as `AccountRemoval`'s handler.

### Storage

`users.ready_at TIMESTAMPTZ NULL`. Set once, on first successful `ReadyCert`
verify; never cleared, never updated again. This is the durable fact
(survives a DB wipe the same way any other `users` column does — it does
not need its own recovery-bundle treatment, since a stranded user with no
`ready_at` is by definition not a real account anyone needs to recover).

`POST /api/users/{id}/ready` (signed, authenticated as the user themself):

- If `users.ready_at` already set: idempotent no-op, return the existing
  cert's fields (same re-check-and-short-circuit shape as
  `AccountRemoval`'s handler uses for its own idempotency). The server
  does **not** mint a second countersignature for an already-ready user.
- Else: verify, set `ready_at = now()`, countersign, return the cert.

No separate `ready_certs` table — one row's worth of state per user, and
the countersignature only needs to be reproducible for the idempotent
re-check, not queried independently. If that turns out to be wrong (e.g. a
client wants to re-fetch its own ready cert later for some reason) this can
grow a table later without breaking the wire shape above.

### Reclaim mutation

`POST /api/invites/{id}/reclaim` (signed, authenticated as the invite's
`created_by`).

Preconditions, checked server-side inside one transaction:

1. Invite exists, `created_by == caller` (`ErrInviteNotOwner` otherwise —
   same check `Revoke` already makes).
2. `claimed_at IS NOT NULL` (`ErrInviteNotClaimed` if still pending —
   nothing to reclaim; use plain `Revoke` for that case instead).
3. `revoked_at IS NULL` (an already-revoked invite can't also be claimed
   per the existing `MarkClaimed` guard, so this should be unreachable, but
   check it anyway rather than assume).
4. The claimed user's `ready_at IS NULL`. If it's set: reject
   (`ErrInviteConfirmed`) — **the inviter never sees who the invitee is or
   any account detail in this case, only that reclaim is refused.** This
   matters: it means a malicious "inviter" probing a real, confirmed
   account learns nothing beyond "can't reclaim," not the target's
   existence or status. Locked decision: no grace period — not-ready is
   reclaimable the instant it's claimed, full stop. The inviter already
   knows their invitee is stuck (that's why they're here); the server
   doesn't second-guess the timing.

If all four hold, in one transaction:

1. Capture `claimed_by` (the stranded user's canonical id) before mutating
   anything.
2. `DELETE FROM identities WHERE id = $claimedUserID` — the real FK root,
   same pattern `claimUsername`'s loser-deletion already uses
   ([recovery/upsert.go](../../recovery/upsert.go)). Cascades through
   `users`, `public_keys`, signatures, and social bookkeeping in one shot.
   This is a genuine hard delete, unlike `AccountRemoval` — no tombstone,
   no cert for peers to fan out (there's nothing for a peer to have
   synced yet; this account was never confirmed live).
3. `UPDATE invites SET claimed_at = NULL, claimed_by = NULL WHERE id = $1`
   — reset to unclaimed. The original `token_hash` is untouched, so the
   inviter's already-shared link/QR/secret is valid again without minting
   a new invite (locked decision).
4. `coverage.BumpActiveUsers(ctx, tx, -1)` — mirrors
   `claimUsername`'s loser-deletion bookkeeping.
5. Commit.

Response: the invite's refreshed status (same shape as
`GET /api/invites/{id}`, now back to `"pending"`).

### Client: sending ready

The profile-page-visit trigger is new territory — no existing "send once
on first visit" convention in this codebase to reuse
(`spa/src/routes/profile/[userId]/+page.svelte` has no `onMount` today).

Dedupe is server-side, not client-side: the client can attempt the send on
every profile-page visit for the currently-authenticated user; the
idempotent no-op response makes repeated sends harmless and cheap. This is
deliberately more robust than a client-side "already sent" flag (e.g.
`localStorage`), which cannot survive the exact failure mode this feature
exists to catch — a tab that dies before the flag would ever get written.

### Client: reclaim affordance

On the invites page, a `"claimed"` row where the claimant turns out to be
gone (reclaim already happened, or — before the inviter acts — a row whose
status the inviter suspects is stale) needs a way to check. Concretely:

- Invites page shows the existing "Claimed by {username}" state as today.
- Add a "Can't reach them? Reclaim this invite" action, shown only on
  claimed, unrevoked invites the caller created.
- Clicking it calls `POST /api/invites/{id}/reclaim`.
  - Success → invite flips back to "Pending" locally (same
    `invitesRepository.putStatus` merge `refreshPendingInviteStatuses`
    already uses), share link reappears.
  - `ErrInviteConfirmed` (i.e. the account did go ready, possibly between
    page load and click) → surface as "This account is active — nothing to
    reclaim," refresh the row to `"claimed"` with no other detail exposed.

If the invitee's device later re-appears and tries to use the original
signup flow, it fails "username taken" only if someone else since claimed
the reset invite first — otherwise the same secret still works, since
nothing about the token changed.

## Locked decisions

| Decision | Choice |
|----------|--------|
| Ready payload | `serverID` + `userID` + `signedAt` only — no invite binding |
| Ready storage | `users.ready_at`, set once, no separate cert table |
| Ready dedupe | Server-side idempotent no-op, not a client-side sent-flag |
| Reclaim authority | Invite's `created_by` only — no admin override |
| Reclaim grace period | None — not-ready is reclaimable immediately |
| Invite after reclaim | Reset to unclaimed, same secret reusable — not re-minted |
| Stranded user deletion | Hard delete via `identities` cascade, not `AccountRemoval` tombstone |
| Confirmed-account leak | Reclaim on a ready account reveals nothing but refusal |

## Open questions

- Should `ErrInviteNotClaimed` (reclaim attempted on a still-pending
  invite) just redirect to the existing `Revoke` semantics instead of being
  a distinct error, since the end state ("invite unusable, issuer can
  create/reuse") is similar? Leaning toward keeping them distinct — revoke
  and reclaim have different preconditions and the caller-facing intent
  ("I don't want this invite used" vs "the person who claimed this is
  stuck") is different even if one edge case's outcome rhymes.
- Does `POST /api/users/{id}/ready` need rate limiting beyond the natural
  one-row-per-user ceiling? Given it's idempotent and gated by signed auth,
  probably not, but worth a second look before implementation.

## Test plan (tentative)

- [ ] `ReadyCert` sign/verify round trip, both TS and Go payload builders
  byte-identical (same convention as every other paired builder).
- [ ] Idempotent ready: second call for an already-ready user returns the
  same cert, does not mint a new server signature.
- [ ] Reclaim happy path: claimed + not-ready → user hard-deleted, username
  free, invite back to pending with original `token_hash`.
- [ ] Reclaim refused: claimed + ready → `ErrInviteConfirmed`, no user
  deletion, invite still `"claimed"`.
- [ ] Reclaim refused: not the invite's creator → `ErrInviteNotOwner`
  (existing check, just needs covering for this new route too).
- [ ] Reclaim refused: invite still pending → `ErrInviteNotClaimed`.
- [ ] Router-level test for `/api/invites/{id}/reclaim` and
  `/api/users/{id}/ready` with real (slash-containing) canonical ids —
  see [invites/handlers_test.go](../../invites/handlers_test.go)'s
  `TestRegisterRoutes_StatusAndRevokeSlashID` for why this matters; don't
  repeat the bug that test was added to catch.
