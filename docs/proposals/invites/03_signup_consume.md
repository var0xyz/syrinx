# Invites 03 — Signup consume, `invitedBy` identity, mutual follow

## Status

Proposed.

## Depends on

[02](02_lifecycle_api.md)

## Context

Invites can be created and listed, but `POST /api/users/signup` still ignores
them. This step enforces `SIGNUP_MODE=invite`, optionally honors invites in
`open`, persists `users.invited_by`, extends the server identity payload, and
creates mutual follows on successful redeem.

## Scope

- Gate signup for real `invite` mode (plus existing `closed` from 00).
- Accept form field `invite` (raw token) on `POST /api/users/signup`.
- Atomic consume inside the signup transaction.
- Bootstrap: `invite` mode + zero users → allow signup without token;
  `invited_by` NULL.
- Extend [`identity.BuildProfilePayload`](../../../identity/identity.go) (and
  TS mirror in `spa/src/lib/services/signing.ts`) with optional `invitedBy`
  header.
- Persist `invited_by`; expose on `User` JSON as
  `invitedBy: { id, username } | null`.
- On redeem: mutual follow (both directions) in the same TX.
- Profile update path must preserve `invitedBy` when re-countersigning
  (immutable).

## Non-goals

- SPA gating / toolbar (04–05).
- Putting `invitedBy` on the **user** identity payload.
- Signing the invite row itself.
- Changing follow APIs beyond inserting edges during signup.

## Design

### Form field

`invite` — optional string. Empty / absent means “no token”.

### Policy

```
closed          → 403 (already from 00)
invite && n>0   → require non-empty invite; invalid → 403
invite && n==0  → ignore invite requirement (bootstrap);
                  if invite still provided and valid, may consume it
                  (prefer: if provided, must be valid OR allow ignore —
                  pick: **if provided during bootstrap, still consume if
                  valid; if invalid, 403** — keeps behavior predictable)
open            → invite optional; if provided must be valid and is consumed
```

User count `n` must be observed **inside** the signup transaction so two
parallel bootstraps cannot both skip the invite.

### Transaction order

Extend `DataService.Signup` (or move orchestration into `invites.ConsumeAndSignup`
called from the handler) to run in one `BEGIN … COMMIT`:

1. `LOCK TABLE users IN SHARE ROW EXCLUSIVE MODE` (or equivalent that
   serializes inserts + counts — document the chosen lock; SHARE ROW
   EXCLUSIVE blocks concurrent schema changes and conflicts with other
   writers enough to serialize signup inserts).
2. `SELECT COUNT(*) FROM users`.
3. Apply policy above; if a token is required or provided:
   - `hash = HashToken(raw)`
   - load invite by hash; if not pending → **403** `"Invalid or claimed invite"`
     (same string for claimed/revoked/unknown to match check's opacity, or
     slightly more helpful on the authenticated-to-signup path — prefer
     one stable string: `"Invalid or claimed invite"`).
   - remember `inviterID = invite.CreatedBy`, `inviteID = invite.ID`.
4. Allocate `userID`, verify signatures, build profile payload **with**
   `invitedBy: inviterID` when set (else omit header).
5. `INSERT users (…, invited_by)`.
6. `INSERT user_keys (…)`.
7. If inviter set:
   - `MarkClaimed(tx, inviteID, newUserID, now)` — must affect 1 row; else
     abort (race lost).
   - Mutual follow:
     - following: `(inviter → invitee)` and `(invitee → inviter)`
     - followers: mirror as `FollowUser` does today
     - use `ON CONFLICT DO NOTHING`.
8. Commit.
9. Return `GetUser` (includes joined `invitedBy`).

If any step fails, rollback — invite remains pending.

### Identity payload change

`profileHeaders` / `BuildProfilePayload` gain an `invitedBy string`
parameter. When non-empty, set header `"invitedBy": invitedBy`. When empty,
omit (do not write an empty header line).

Update all call sites:

- Signup countersign
- Profile update countersign (load `invited_by` from row; pass through
  unchanged)
- Any recovery / test helpers that rebuild profile payloads
- TS `buildProfilePayload` / server-verify mirrors in the SPA

Add identity package tests: with and without `invitedBy`; omit-empty
behavior; profile-update fixture carries the same value.

Package comment at top of `identity.go` should mention `invitedBy` in the
server-authored field list.

### `User` wire

```go
type InvitedBy struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

// on User:
InvitedBy *InvitedBy `json:"invitedBy"` // null if none
```

`GetUser` / list-user queries LEFT JOIN inviter on `users.invited_by`.

Clients displaying a profile show “Invited by @username” when non-null.

### Mutual follow details

Same two-table write as [`FollowUser`](../../../services.go):

```sql
INSERT INTO user_following (user_id, following_user_id) VALUES
  ($inviter, $invitee), ($invitee, $inviter)
ON CONFLICT DO NOTHING;

INSERT INTO user_followers (user_id, follower_user_id) VALUES
  ($invitee, $inviter), ($inviter, $invitee)
ON CONFLICT DO NOTHING;
```

No realtime follow notification required for v1 unless an existing broadcast
already fires on follow — do not invent a new event type here.

Either party may unfollow later through the normal unfollow API.

### Error status codes

| Case | Status | Message |
|------|--------|---------|
| Mode closed | 403 | `Signups are closed on this server` |
| Invite required but missing | 403 | `Invite required` |
| Invite invalid / claimed / revoked | 403 | `Invalid or claimed invite` |
| Username taken | existing behavior | unchanged |
| Crypto failure | existing behavior | unchanged |

### Handler wiring

Keep crypto verification in `Handlers.Signup`. Pass mode, optional raw
token, and a consume callback / invites store into the persistence layer.
Avoid duplicating signature logic inside `invites/`.

## Test plan

- [ ] `closed` → still 403
- [ ] `invite`, empty DB, no token → 201; `invitedBy` null; no follows
- [ ] `invite`, empty DB, two parallel signups without token → exactly one
      succeeds without invite **or** both serialize such that the second
      requires an invite (assert invariant: never two users both without
      invite when mode is `invite` and an invite never existed — i.e. at
      most one bootstrap user)
- [ ] `invite`, one user exists, no token → 403 `Invite required`
- [ ] `invite`, valid token → 201; `invitedBy.id` = inviter; invite
      `status=claimed`; mutual follows present both ways
- [ ] Same token twice → second 403; only one user
- [ ] Revoked / claimed token → 403
- [ ] `open`, no token → 201; `invitedBy` null
- [ ] `open`, valid token → 201; `invitedBy` set; mutual follows
- [ ] `open`, invalid token → 403 (does not create user)
- [ ] Profile payload bytes include `invitedBy:` when set; omit when null
- [ ] `PUT /users/me` after invited signup still verifies with same
      `invitedBy` header
- [ ] Identity unit tests updated in Go + TS mirrors stay in lockstep
