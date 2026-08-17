# Analysis — `identities` indirection for federated user refs

Not a numbered proposal step. Notes from a design discussion about a
problem discovered while thinking through server recovery
([`specs/recovery/`](../recovery/README.md)) interacting with federation's
planned `(serverID, userID)` foreign refs ([04](04_runtime_verify_display.md),
[06](06_content_relay.md)).

## Problem

The federation design (04/06) has every table that references a remote
user carry both `serverID` and `userID`, with `serverID` resolving against
a `servers` row that only exists once a handshake completes (00–03).

That's fine in the happy path. It breaks during **server recovery**
(`RECOVERY_MODE`): a peer identity report or reed holding can name a
remote `(serverID, userID)` whose `servers` row doesn't exist yet, because
that federation handshake hasn't been (re-)performed against this fresh
instance. Hydration order becomes: *must federate every peer server before
any record referencing one of its users can be saved* — which inverts the
actual dependency (federation and content recovery are independent in
principle) and stalls DB reconstruction on an OOB ceremony (admin key
exchange) that has nothing to do with restoring local data.

## Options considered

1. **Bare string `userID@serverID`** in place of the FK pair. Rejected —
   throws away DB-level referential integrity; every reference becomes
   unverifiable-by-schema and dependent on app-level parsing/validation at
   every call site.

2. **`identities` indirection table.** Store `(remoteUserID,
   remoteServerID, publicKeyFingerprint)` behind a single surrogate key
   that other tables FK to, with `remoteServerID` nullable until handshake
   resolves it, and a `verified` state. Content tables always have a real
   FK; only the row *behind* the FK is provisional until federation
   catches up.

Recommendation: **option 2**, with caveats below.

## Why this is the right shape

This is the same pattern the recovery spec already validated for the
analogous problem one level down — a **user** identity arriving before its
owner has "shown up" to verify it:

- `unclaimed_accounts` — a peer reports a user's identity; the `users` row
  is created for real (FK-safe for everything that references it) before
  the owner ever claims. See [recovery README §What is
  reconstructed](../recovery/README.md#what-is-reconstructed) and
  [05](../recovery/05_peer_identity_report.md).
- The row is **updated in place** on claim (not replaced), and the
  bookkeeping row is deleted — the `userID` referenced by every other
  table never changes.

`identities` should do the same thing one level up: a real row and a real
FK from day one; `remoteServerID` starts NULL/unverified and gets filled
in when the handshake resolves it, updating the *same* row rather than
inserting a new one.

## Problems this analysis surfaces

### 1. Verification must be a single choke point, not a per-caller check

Nothing about a nullable-until-handshake FK stops downstream code from
reading and displaying an unverified identity's claims before the
handshake completes. Federation [04's display
table](04_runtime_verify_display.md#display-03-cross-instance-display-merged-here)
already specifies the rule ("established & not revoked" vs. "not
connected" / opaque ref) — `identities` must be the mechanical join target
for that exact rule, enforced in one resolver function
(`ResolveIdentity`-style), not re-implemented at every read call site. A
missed call site is the likely failure mode (the class of bug flagged
previously re: duplicate query implementations across transports) —
centralize it.

### 2. Unverified and revoked must collapse to the same behavior

Federation [05](05_revoke_established.md) already requires "revoked peer
→ same UX as unpeered" (fail closed, opaque ref, 401 on incoming). If
`verified` only encodes "a handshake once succeeded" and doesn't also
fold in "and `federation_established.revoked = false`", the two states
will drift into two different code paths and eventually leak revoked-peer
data through the `identities` join even though display rules forbid it
for the direct case.

### 3. Provisional-row mechanics

- **Update in place on handshake completion.** When `remoteServerID`
  resolves, update the existing `identities` row (stable `id`/PK) rather
  than inserting a new one — every table that already FK'd to the
  provisional row keeps working with no rewrite. Mirrors the
  `unclaimed_accounts` → claimed transition.
- **Pre-handshake uniqueness is weaker.** A `UNIQUE (remoteUserID,
  remoteServerID)` constraint doesn't dedup rows where
  `remoteServerID IS NULL` (Postgres treats NULLs as distinct). Multiple
  peers relaying claims about the same not-yet-verified remote user can
  produce multiple provisional rows. Decide whether dedup by claimed
  fingerprint (or accepting the duplication until handshake, then
  merging) is needed before this fans out.
- **Conflicting provisional claims.** Two different reporting peers can
  claim different things about who's behind an unverified `remoteUserID`
  before there's a handshake to arbitrate. There's no invite-style "who we
  actually addressed this to" check available pre-handshake (unlike
  federation 02's fingerprint-matches-invitation check) — needs an
  explicit resolution rule (e.g., last-write-wins is *not* safe here per
  the recovery threat model's timestamp reasoning; likely needs the same
  "only resolve identity via a verified channel" rule extended to mean
  provisional rows are never treated as authoritative for anything beyond
  "a placeholder exists to FK against").

### Sketch

```sql
CREATE TABLE identities (
    id VARCHAR(255) PRIMARY KEY,           -- surrogate; everything else FKs here
    remote_user_id VARCHAR(255) NOT NULL,
    remote_server_id VARCHAR(16) REFERENCES servers(id),  -- NULL until handshake resolves it
    public_key_fingerprint VARCHAR(255) REFERENCES public_keys(fingerprint),
    verified BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (remote_user_id, remote_server_id)
);
```

Open question: whether local users also get an `identities` row (uniform
join surface for every table that currently FKs straight to `users`) or
`identities` is federation-only and local reads bypass it. Uniform is
simpler to reason about but touches more of the schema; federation-only is
a smaller change but means two join shapes depending on origin.

## On performance

Not the real risk. One additional join (`content table → identities →
servers`) is cheap with the PK index and an index on `(remote_user_id,
remote_server_id)` — same shape as the existing `users`/`servers` joins,
one hop longer. The actual hard part is keeping verification/revocation
state consistent across every read path, not join cost.

## Bottom line

Keep the `identities` indirection — it preserves real FK integrity and
mirrors the already-validated `unclaimed_accounts`/`ongoing_recoveries`
pattern from server recovery. Before treating it as a locked design:

1. Centralize verified/revoked-aware resolution in one function; don't
   let every read path re-check.
2. Transition provisional → verified by updating the same row/PK in
   place, never by insert-and-rewrite.
3. Make "unverified" and "revoked" produce identical display/serving
   behavior so the two states can't drift apart.
4. Decide the pre-handshake uniqueness/conflict story before this ships —
   it's the one piece with no existing precedent to crib from.
