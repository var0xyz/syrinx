# Invites / signup modes

This directory is the **invite-only signup** feature proposal set. Numbered
files below are independently reviewable implementation steps. Land them in
order unless a step's "Depends on" says otherwise.

**Code organization:** all invite-specific server logic lives in the
`syrinx/invites` Go package. The main package only wires boot config, DDL
registration in `InitDB`, route mounting, and a thin call from `Signup` into
the invites consume helper. Shared identity payload builders stay in
`syrinx/identity` (extended with `invitedBy`).

| #                                    | Title                                              | Depends on |
|--------------------------------------|----------------------------------------------------|------------|
| [00](00_signup_mode.md)              | `SIGNUP_MODE` + `MAX_INVITES_PER_USER`, info gate  | —          |
| [01](01_schema_and_store.md)         | `invites` table, `users.invited_by`, store         | 00         |
| [02](02_lifecycle_api.md)            | Create / list / revoke / check APIs + quota        | 01         |
| [03](03_signup_consume.md)           | Consume at signup, identity, mutual follow         | 02         |
| [04](04_spa_signup_gating.md)        | Home CTA + invite-link signup path                 | 00, 03     |
| [05](05_spa_invite_management.md)    | Toolbar Invites tab + management UI                | 02, 03     |

---

## Status

**00–03 implemented.** **04–05 proposed.**

## Motivation

Operators need to control who can join a Syrinx instance: open registration,
invite-gated registration, or no new signups at all. When invites are in
play, every authenticated user can mint shareable single-use links, subject
to an optional per-user cap. Redeeming an invite records a durable,
**server-countersigned** `invitedBy` binding on the new user and establishes
a mutual follow between inviter and invitee.

Invites themselves are **operational bookkeeping**, not offline-first signed
resources. Losing the `invites` table after a DB wipe is acceptable; the
durable social fact that survives is `users.invited_by` inside the signed
identity record.

## Locked decisions

| Decision | Choice |
|----------|--------|
| Mode env | `SIGNUP_MODE=open\|invite\|closed` (default `open`) |
| Quota env | `MAX_INVITES_PER_USER`: `>= 1`, or `-1` / unset = infinite; else fatal. `.env.example` defaults to `3` with disable comment |
| Bootstrap | `invite` + empty `users` → first signup needs no invite |
| Token | Opaque ≥256-bit URL-safe secret; store SHA-256 only |
| Expiry | None |
| Revoke | Issuer may revoke unused invites |
| Attribution | `invites.used_by` + `users.invited_by`; both visible in UI |
| Mutual follow | Yes, both edges, same TX as signup redeem |
| `invitedBy` signed | Server identity header only (like `userID`) |
| Offline-first invites | No (v1) |
| Home “Sign Up” | Only when `open` |
| Toolbar Invites | Always visible; create disabled when `closed` |

## Actors

- **Operator** — sets `SIGNUP_MODE` and `MAX_INVITES_PER_USER` at deploy time.
- **Inviter** — authenticated user who creates / lists / revokes invites and
  sees who claimed them.
- **Invitee** — person holding an invite link; completes normal PGP signup
  with the token; sees `invitedBy` on their (and others') profile.

## Environment

```bash
# open | invite | closed  (default: open)
SIGNUP_MODE=invite

# Cap on invites each user may create (all-time, including used/revoked).
# Positive integer >= 1. To disable the cap: set to -1, or leave unset.
MAX_INVITES_PER_USER=3
```

[`.env.example`](../../../.env.example) ships with `MAX_INVITES_PER_USER=3` and
the disable comment above. That is the recommended operator default in the
example file only — the runtime parse rule for an **unset** variable remains
infinite (see below), so existing deploys that omit the var are uncapped
until they opt in.

Parse rules (fatal at boot on violation):

- `SIGNUP_MODE`: empty → `open`. Otherwise must be exactly one of
  `open`, `invite`, `closed`.
- `MAX_INVITES_PER_USER`: unset or `-1` → infinite. Otherwise must parse as
  an integer `>= 1`. `0`, negatives other than `-1`, non-integers → fatal.

`/api/server/info` exposes both so the SPA can gate without auth:

```json
{
  "id": "<serverID>",
  "name": "<SERVER_NAME>",
  "signupMode": "open" | "invite" | "closed",
  "maxInvitesPerUser": -1
}
```

`maxInvitesPerUser` is always a JSON number; `-1` means infinite.

## Mode matrix

| Mode | Home “Sign Up” | `POST /users/signup` | `POST /check-username` | `POST /api/invites` | Bootstrap (0 users) |
|------|----------------|----------------------|------------------------|---------------------|---------------------|
| `open` | Visible | Allowed; invite **optional** (if present → must be valid, consume, set `invited_by`, mutual follow) | Allowed | Allowed (quota) | N/A |
| `invite` | Hidden | Valid unused invite required | Allowed | Allowed (quota) | Allowed without invite |
| `closed` | Hidden | 403 | 403 | 403 on **create** (list/revoke still ok) | No |

Import / backup restore is **not** signup and remains allowed in all modes.

## Invite model

```
invites
  id            VARCHAR  PK          -- server-scoped random id (same alphabet as user IDs)
  token_hash    BYTEA    UNIQUE      -- SHA-256 of raw token
  created_by    VARCHAR  NOT NULL REFERENCES users(id)
  created_at    TIMESTAMPTZ NOT NULL
  used_at       TIMESTAMPTZ NULL
  used_by       VARCHAR  NULL REFERENCES users(id)
  revoked_at    TIMESTAMPTZ NULL
```

- **Valid** = `revoked_at IS NULL AND used_at IS NULL`.
- **Raw token**: 32 cryptographically random bytes, base64url, no padding.
  Returned **once** at create; never logged; never stored plaintext.
- **Quota**: `COUNT(*) FROM invites WHERE created_by = $user` (all-time,
  including used and revoked) must be `< max` before insert when max ≠ −1.
  All-time counting prevents revoke-and-recreate abuse.
- **Share URL** (client-built): `{origin}/signup?invite={token}`.

### HTTP API catalog

| Method | Path | Auth | Purpose |
|--------|------|------|---------|
| `POST` | `/api/invites` | Signed | Create invite; returns `{ id, token, createdAt }` |
| `GET` | `/api/invites` | Signed | List caller's invites (status + `usedBy` when set) |
| `DELETE` | `/api/invites/{id}` | Signed | Revoke unused invite owned by caller |
| `GET` | `/api/invites/check?token=` | None | `{ valid: bool }` for pre-signup UX |

Unauthenticated allowlist gains `/api/invites/check` (exact path; query string
ignored for matching, same pattern as other allowlisted routes).

### List item wire shape

```json
{
  "id": "...",
  "createdAt": "2026-07-18T20:00:00Z",
  "status": "pending" | "used" | "revoked",
  "usedAt": null,
  "usedBy": null,
  "revokedAt": null
}
```

When `status === "used"`:

```json
"usedAt": "2026-07-18T21:00:00Z",
"usedBy": { "id": "...", "username": "..." }
```

The raw `token` is **never** returned on list (only on create).

## `users.invited_by` and signed identity

Column: `invited_by VARCHAR(255) NULL REFERENCES users(id)`.

Wire on `User`:

```json
"invitedBy": null
```

or

```json
"invitedBy": { "id": "<inviterUserID>", "username": "<inviterUsername>" }
```

The **signed** server identity header carries only the id string:

```
invitedBy: <inviterUserID>
```

Omitted entirely when null (envelope absent==empty rule). The username in
the JSON object is a read-time join for display; it is **not** part of the
signed bytes (usernames can change; the binding is to the stable user id).

User identity payload is unchanged — `invitedBy` is server-authored, same
trust class as `userID` / `memberSince`. Profile updates must re-emit the
same `invitedBy` header value (immutable for the life of the account).

## Signup consume (overview)

Inside the existing signup transaction, after crypto checks:

1. Gate on `SIGNUP_MODE` (`closed` → 403).
2. Decide whether a token is required / optional / forbidden-to-skip
   (`invite` + non-empty users → required; bootstrap → skip; `open` →
   optional).
3. If a token is present: hash → conditional update marking used → must
   affect exactly one row.
4. `INSERT users` with `invited_by` set (or NULL).
5. Countersign profile payload including `invitedBy` when set.
6. If redeemed: insert mutual follow edges (both directions), mirroring
   `FollowUser`.
7. Commit.

Failed signup never burns an invite. Two concurrent redeems of one token
cannot both succeed.

## SPA summary

- **Landing**: “Sign Up” only if `signupMode === "open"`. Import backup
  always available.
- **Invite link**: `/signup?invite=TOKEN` works for `invite` and (optionally)
  `open`. Preamble may be skipped or carried through with the query param
  preserved — see step 04.
- **Toolbar**: Invites tab always present. Create disabled with explanation
  when `closed`; allowed when `open` or `invite` (quota messaging when
  capped). List shows claimants; profile shows `invitedBy`.

## Non-goals

- Offline-first / signed / recoverable invite rows.
- Email-bound invites or outbound email.
- Invite expiry timestamps.
- Rate limits beyond `MAX_INVITES_PER_USER`.
- Changing the PGP signup crypto beyond adding `invitedBy` to the **server**
  identity headers.
- Public “invite graph” analytics beyond per-user list + profile field.

## Parallelism

- **00** can land alone (mode/quota config + `closed` gate; `invite` behaves
  like `open` until 03).
- **01 → 02 → 03** are sequential.
- **04** needs 00 + 03; **05** needs 02 + 03; 04 and 05 can proceed in
  parallel after 03.

## Shared conventions

Reuse root proposal conventions where relevant
([`../README.md`](../README.md)): `BytesToSign`, detached PGP, omit-empty
headers. Invite **tokens** are not signed envelopes — only `invitedBy` on
the identity record uses that machinery.
