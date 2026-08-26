# Federation 02 — Connect handshake

## Status

**Implemented**, with deviations from the original design below (see
[Implementation notes](#implementation-notes) for the reasoning behind
each). **Correction to a claim previously made here:** point 1 below
used to say no `federation_attempt` table exists — that was wrong.
`federation_attempt` (`db.go`) is real and load-bearing: both sides of
a handshake get their own row (the initiator's has `invitation_id` set;
the responder's, which never created an invitation, has it `NULL`), and
it is specifically what [03](03_approval_established.md)'s manual
approval step operates on — see that doc's Status for the full
approval-gate picture, since it turned out to be implemented too, just
not exactly as 03 originally designed it.

1. **`federation_invitation` still exists and is real** (`created_by`,
   `secret_hash`, `status` machine below) — `federation_attempt` doesn't
   replace it, the two coexist: `federation_invitation` is the
   initiator's "I sent an invite" record, `federation_attempt` is each
   side's own "a handshake happened" record, linked by
   `federation_attempt.invitation_id` on the initiator's row only.
2. **`servers` gained `base_url` and `connected`, but neither is written
   at connect-callback time.** Both the responder's `OutgoingFederationAttempt`
   and the initiator's `IncomingFederationAttempt` handlers only touch
   `federation_attempt`/`federation_invitation` — no `servers` row for the
   peer exists yet at that point. The peer's `servers` row (and
   `connected = TRUE`) is only created later, at
   [03](03_approval_established.md)'s manual approval step.
3. **The responder's public key lives in `public_keys`**, keyed by
   fingerprint, not duplicated onto `federation_invitation`.
   `federation_invitation.fingerprint` FKs into it. No `remote_` prefix on
   any column — everything on this table describes the other side of the
   handshake by definition.
4. **No `respondedByUserId`/similar field anywhere.** The initiator has no
   way to verify a remote user id, so it never asks for or stores one.
5. **Added `federation_log`** (not in the original design at all): the
   handshake spans two servers and happens asynchronously, so both sides
   write append-only log lines an admin can read to see what actually
   happened instead of a link silently sitting in `new`/unconnected with
   no explanation.
6. **Status vocabulary is more granular than `new`/`accepted`.** See
   [01](01_invitation_create.md) for the full state machine
   (`new → accepted → approved|rejected`, `new → canceled`,
   `approved → revoked`) — `canceled` (never redeemed) and `revoked` (was
   working, torn down) are now distinct, where the original design used
   one `revoked` value for both. Note this is `federation_invitation`'s
   status machine; `federation_attempt.status` is separately
   `pending`/`approved`/`rejected`.

## Depends on

[01](01_invitation_create.md)

## Context

Server A holds invitations in **`new`**; Server B must decrypt, verify, and
callback without user session auth on A's connect route.

## Scope

- Responder decrypt + verify path (admin API)
- **`POST /api/federation/connect/{inviteId}`** on initiator (allowlisted)
- Peer bookkeeping on **`servers`** (`base_url`, `connected`)
- Invitation **`new` → `accepted`** (row retained)

## Non-goals

- Second-admin approval (03)
- Runtime peer requests (04)

## Design

### Responder signature

Server B signs canonical bytes binding response to invite:

```
inviteId: …
serverId: …
baseUrl: …
fingerprint: …
```

No `secret` in this payload — B proves possession of the invite separately,
via the `secret` field on the connect request body (taken from what it
decrypted), not by signing over it.

### `POST /api/federation/connect/{inviteId}` (initiator, A)

- **Auth:** none (signature + secret prove legitimacy); allowlisted in
  `signatureAuthMiddleware`
- **Body:**

```json
{
  "serverId": "...",
  "baseUrl": "https://b.example",
  "fingerprint": "...",
  "signature": "...",
  "secret": "..."
}
```

Initiator (`IncomingFederationAttempt`) validates, in order:

1. Invitation exists and **`status = new`** — 404 / 409 otherwise
2. `secret` matches the invitation's stored hash (constant-time compare) — 403
3. `fingerprint` matches the invitation's stored `fingerprint` (the key A's
   admin originally pasted) — rejects a self-reported fingerprint that
   doesn't match who A actually addressed the invite to — 403
4. B's signature verifies against `public_keys.armor` for that fingerprint
   — 400
5. `baseUrl` has an `https://` scheme — 400

Each rejection writes a `federation_log` line linked to the invitation (see
[`federation_log`](#federation_log)) before responding, so A's admin can
see why a callback was rejected even though they never see the HTTP
response directly.

**200** `{ "status": "accepted", "serverId": "..." }`.

Side effect on **A**, atomically (`DataService.MarkFederationInvitationAccepted`):

1. Upsert a **`servers`** row for the peer (`self = FALSE`, `connected =
   TRUE` immediately — A's own verification of the callback *is* the
   confirmation, no further round trip needed on this side)
2. Invitation → **`status = accepted`**, set `accepted_at`, clear
   **`connection_ciphertext`**, set **`server_id`** to the peer's id

### Responder API — `POST /api/federation/attempt` (Server B)

Named "attempt", not "accept": pasting the connection string only *starts*
an attempt at redeeming the invitation — nothing is confirmed until the
initiator's `/connect` callback verifies it.

| Method | Path | Auth | Purpose |
|--------|------|------|---------|
| `POST` | `/api/federation/attempt` | Admin | Paste connection string |

**Body:**

```json
{
  "connectionString": "-----BEGIN PGP MESSAGE-----…"
}
```

Server B (`OutgoingFederationAttempt`):

1. Decrypt `connectionString` with the local server private key
2. Read `publicKeyArmor` from the decrypted payload; verify the initiator's
   `signature` with that key (must match the payload's claimed
   `fingerprint`) — 400 on failure, nothing written yet
3. **Record the peer** (`DataService.RedeemFederationInvitation`): upsert
   `public_keys` with A's armor, upsert a `servers` row for A
   (`self = FALSE`, `connected = FALSE`) — *before* attempting the network
   call, so there's somewhere to log against even if the next step fails
4. POST connect to `{baseUrl}/api/federation/connect/{inviteId}`
5. On success (200), call `DataService.AcceptFederationInvitation` to flip
   that `servers` row to `connected = TRUE`
6. On failure to reach A, or a non-200 from A, log an error against the
   peer server row and surface the failure synchronously in the HTTP
   response to the admin (who's actively watching, unlike A's admin who is
   passively waiting for a callback that may never come)

**200** `{ "status": "accepted", "serverId": "..." }`.

### `federation_log`

Generic append-only log line, not itself tied to an invitation or server —
two junction tables link a line to whichever it's about. One log row per
event; each junction is a plain FK pair, not a many-to-many relationship in
practice.

```sql
CREATE TABLE federation_log (
    id VARCHAR(255) PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    level VARCHAR(16) NOT NULL CHECK (level IN ('info', 'error')),
    message TEXT NOT NULL
);

-- The INITIATOR logs against its invitation from the moment a connect
-- callback arrives (known immediately, regardless of accept/reject
-- outcome) — the invitation's server_id isn't set until AFTER acceptance
-- succeeds, so pre-acceptance rejections (bad secret, wrong status, bad
-- signature) have nothing else to log against yet.
CREATE TABLE federation_invitation_log (
    invitation_id VARCHAR(255) NOT NULL REFERENCES federation_invitation(id) ON DELETE CASCADE,
    log_id VARCHAR(255) NOT NULL REFERENCES federation_log(id) ON DELETE CASCADE,
    PRIMARY KEY (invitation_id, log_id)
);

-- Server-scoped log lines. The RESPONDER always uses this (it never has a
-- local invitation row to log against) from the moment it records the
-- peer server row, before attempting the handshake. The initiator also
-- logs here for server-level events after server_id exists
-- (post-acceptance).
CREATE TABLE federation_server_log (
    server_id VARCHAR(16) NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    log_id VARCHAR(255) NOT NULL REFERENCES federation_log(id) ON DELETE CASCADE,
    PRIMARY KEY (server_id, log_id)
);
```

**Why:** the handshake is asynchronous across two independently-operated
servers — a connect callback can arrive with a bad secret, a mismatched
fingerprint, an invalid signature, or the outbound POST from B to A can
simply fail to connect. Without a record of these, an admin watching an
invitation sit in `new` (or a peer sit unconnected) has no way to tell
"nothing has happened yet" from "something failed three times" —
`writeResponse`'s HTTP-level error only ever reaches whichever side
triggered *that specific* request, never the other admin waiting on the
other server.

**Writes** (`DataService.logFederationInvitation` / `.logFederationServer`,
called via `Handlers.logFederationInvitationAsync` /
`.logFederationServerAsync` — fire-and-forget goroutines so a logging
failure can never turn an otherwise-successful handshake step into a 500):

- `IncomingFederationAttempt` (initiator, A) — logs to the **invitation**
  at every step: info on receipt of a callback, error on each rejection
  (wrong status, bad secret, fingerprint mismatch, bad signature), info on
  acceptance.
- `OutgoingFederationAttempt` (responder, B) — logs to the **peer server
  row** from the moment it's recorded (before the outbound POST), then
  again on reach/reject failure or final success.

**Reading these logs (SPA/API) is not yet built** — this pass is schema +
write path only. A `GET .../log` endpoint and a Mesh UI display are a
follow-up.

### SPA — Admin → Mesh → Accept connection

- Field: connection string (pasted, base64-encoded for clipboard safety)

### Middleware

`/api/federation/connect/` is allowlisted in `signatureAuthMiddleware` (like
the invite-check route) — the initiator's callback endpoint has no user
session, since the caller is another server, not a logged-in admin.

### Tests

- Full A→B handshake over real HTTP/TLS: invitation `new` → `accepted`;
  both sides record a connected peer server independently
  (`TestFederationHandshake_FullRoundTrip`)
- Wrong secret → 403; invitation stays `new`
  (`TestIncomingFederationAttempt_WrongSecret`)
- Replay connect when invitation not `new` → 409
  (`TestIncomingFederationAttempt_ReplayNotNew`)
- Invalid initiator signature on B → 400 before the outbound POST, no peer
  row created (`TestOutgoingFederationAttempt_InvalidInitiatorSignature`)

### Implementation notes

**Why no `federation_attempt` table:** the original design's `attempt`
table only ever existed because the handshake state was being tracked
separately from the invitation. But every field on it was either a
duplicate of something already on `federation_invitation`
(`fingerprint` ≡ the invitation's own fingerprint once verified, `initiated_by`
≡ `created_by` on the initiator side) or something that belongs on the
*server* being connected to, not on a transient handshake record
(`server_id`, `base_url` → moved to `servers`). Once those are removed,
what's left of "the attempt" is just a few more values `status` can take
on the invitation itself: `accepted` (was: attempt `pending`),
`approved`/`rejected` (was: attempt `approved`/`rejected`), plus `canceled`
and `revoked` split out from a single ambiguous `revoked`. There's no
remaining reason to have two rows tracking one handshake.

**Why peer identity moved to `servers`:** `servers` already exists, already
has exactly one purpose ("a server, self or federated"; see
`MentionTargetValid`'s doc comment, which already anticipated "self or a
federated peer" rows before this table was ever written to for peers).
Recording a peer there — rather than inventing a parallel `base_url`/
`fingerprint` pair on the invitation/attempt — means every other part of
the codebase that already resolves a `serverID` against `servers` (e.g.
mention validation) automatically works for federated peers too, with no
separate lookup path.

**Why the responder's key lives in `public_keys`, not on
`federation_invitation`:** A receives the responder's full public key
armor when creating the invitation (admin pastes it), and needs it again
at connect time to verify the responder's signature — without trusting a
self-reported fingerprint or making the responder resend its key. Storing
the armor is required either way; `public_keys` (fingerprint → armor)
already exists and is exactly this shape, so `federation_invitation.fingerprint`
FKs into it rather than duplicating the armor on the invitation row.

**Why no `respondedByUserId`-style field:** the original design had A store
a plain string the connect request supplied, naming the admin on B who
pasted the string. A has no way to verify this: it's not covered by B's
signature, doesn't correspond to any row A can join against, and could be
any string B's server sends. A only ever records real, locally-verifiable
facts about who acted — `federation_invitation.created_by` on the
initiator, and (once step 03 exists) whichever local admin approves or
rejects — never an unverifiable claim about who did what on the other
server.
