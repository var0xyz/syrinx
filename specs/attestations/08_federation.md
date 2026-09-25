# Attestations 08 — Federated vouches: delivery and cross-server reads

## Status

Proposed.

## Depends on

[03](03_api.md), [05](05_trust_paths.md), [07](07_spa_trust_display.md);
[federation 04](../federation/04_runtime_verify_display.md) (remote identity
verify), [federation 06](../federation/06_content_relay.md) (peer-HTTP
conventions)

## Context

[03](03_api.md#federation) deferred this and named the shape it expected: "a
client fetches vouches from the server that hosts the subject, which it
already knows from the canonical id." Half of that is right — the subject's
server is where a reader *starts* — but it is not where the cert lives.

Creating a vouch for a foreign subject already works today: `resolvePublicKey`
pull-fetches and caches the subject's key from its owning peer over
`GET /api/keys/{id}` before the signature is verified (`CreateVouch`,
`fetchAndCachePeerUserKey`). What does not work is **reading** them.
`ListVouchesForUser` and `GetVouch` query the local table unconditionally, so
a foreign subject returns an empty list, while `/users/{userID}/info` *does*
proxy and returns the peer's `vouchIDs` — ids this server cannot serve. The id
list and the certs disagree, and no mark ever appears for anyone off-server.

**Blank slate — no migration, no backwards compatibility.**

## Scope

- Where the cert lives and where the reference lives.
- Confirmed delivery, why this leg breaks the fire-and-forget norm, and where
  the retry lives.
- Routing the vouch reads across servers.
- The peering requirement for a third-party voucher.
- The pending state in the voucher's UI, and the subject's silence until
  delivery completes.
- The relay refusal across servers, which is where this earns its keep.

## Non-goals

- Serving a cert from anywhere but the voucher's server
  ([§ The cert never leaves V](#the-cert-never-leaves-v)).
- A server-side delivery outbox, queue table, or background retry worker. The
  retry is the client's
  ([§ Retry is the client's, not the server's](#retry-is-the-clients-not-the-servers)).
- Telling the subject about a vouch before it is delivered
  ([§ The subject learns nothing until it completes](#the-subject-learns-nothing-until-it-completes)).
- Third-party vouches where the reader is not peered with the voucher's
  server ([§ The reader must be peered with V](#the-reader-must-be-peered-with-v)).
- Multi-hop trust paths, federated or not ([05](05_trust_paths.md#why-not-deeper)).
- Cross-server root sharing. Roots never leave the device
  ([05](05_trust_paths.md#trust-roots-are-local)).
- Vouches *about* a server or its key. A vouch binds a user key
  ([00](00_design.md#a-vouch-binds-a-key)); server keys are pinned out of band
  (`serverKeyTrust.ts`).

## Design

Three servers appear throughout, named once here:

- **V** — the **voucher's** home server. `POST /vouches` lands here; the cert
  is stored here and served from here, always.
- **S** — the **subject's** home server. Holds a *reference* to the vouch, and
  nothing more.
- **R** — the **reader's** home server, when a third party is reading. May be
  V, S, or neither.

### The cert never leaves V

S stores the vouch **id** and V's `serverID`. It does not store the
signatures, the note, or the payload. A reader gets the id list from S, then
fetches the cert from V and verifies it there.

The reason is that a server holding a cert it serves is a server that can be
asked to lie about it, and the whole feature exists because the server is the
adversary ([00](00_design.md#threat-model)). S never holds the cert, so S
cannot mint, alter, or selectively rewrite one — the strongest statement S can
make is a reference that resolves to nothing, which a reader detects
immediately by fetching from V and finding nothing there.

S retains exactly one power: **omission**. It can leave a vouch out of the
list it serves. That is the suppression [00](00_design.md#what-this-does-not-defend-against)
already names as undefended and unavoidable, and no storage arrangement fixes
it, because the server doing the omitting is the one being asked. The design
goal here is narrower and achievable: ensure S can only ever *subtract*, never
*add* or *change*.

This is why copies are references rather than certs. A cert copy on S would
buy one fetch fewer per vouch and would hand S the ability to serve material
the voucher never signed — paying for latency with the exact property the
feature is built to protect.

### Delivery is confirmed, not fire-and-forget

Every other cross-server notify in `federation_relay.go` is fire-and-forget: a
signed HTTP call at event time, short timeout, silently dropped if the peer
does not answer. [federation 07](../federation/07_presence_delivery.md) locks
that in as permanent v1 behavior, not a gap — no backlog, no replay.

**This leg deviates, deliberately.** A vouch that never reaches S is a vouch
nobody can discover: the reader starts at S, so an undelivered reference means
the attestation exists only in the voucher's own audit list and colours no
mark for anyone. Silently dropping it does not degrade the feature, it cancels
it. Verification happened in a room between two people, and losing it to a
transient 502 wastes the one step that cannot be automated.

So V does not complete the vouch until S has confirmed:

1. V verifies the voucher's signature and countersigns as today
   ([02](02_payload.md#verification-order-server-on-create)).
2. V calls S with the reference and **waits**. S verifies before accepting
   ([§ What S checks](#what-s-checks)).
3. On S's confirmation, V stores the row and returns the cert — **200**, as
   today.
4. Without confirmation, V stores nothing and returns a status saying the
   subject's server could not be reached. The client keeps the vouch queued
   and tries again.

Step 4 is the whole difference from a local vouch. A foreign vouch is not
half-created: either it is stored on V and discoverable on S, or it is not
stored at all and the client still holds the signature. There is no
acknowledged/unacknowledged column on the row, because a row only ever exists
in the delivered state.

This slots into `CreateVouch` without reordering it. The vouch id is minted and
the full cert — including the countersignature — is built in memory *before*
`InsertVouch` is called, so the notify goes between those two steps: build the
cert, deliver it to S, then insert. The id exists to be delivered because it
always did; nothing has to be assigned early or reserved.

The subject's realtime notification (`realtimeVouchCreated`, broadcast after
insert today) therefore stays where it is and needs no condition: for a foreign
subject the insert only happens post-delivery, so the notification cannot fire
before S has the reference. It is the same broadcast a local vouch uses
([§ The subject learns nothing until it completes](#the-subject-learns-nothing-until-it-completes)).

### Retry is the client's, not the server's

The retry lives on the **client**, in the `pendingVouches` IndexedDB store
that already exists for exactly this problem. Its doc comment states the
reason: verification happens in a room, often without signal, so the signature
must outlive the moment it was made rather than the user having to meet the
person again. A peer being unreachable is the same failure one hop further
along, and it takes the same remedy.

`syncPending` already does the work: it loops over queued records, re-`POST`s
each one, and dequeues only on a cert it verified — a record whose `POST`
throws stays queued. So the federated case needs **no new retry mechanism**.
It needs the `POST` to fail honestly when S is unreachable, which step 4 above
provides, and the record then survives to be retried like any other.

The flush is already triggered where it should be: `+layout.svelte` calls
`pendingVouchesRepository.syncPending()` on login and again on regaining
connectivity, alongside the other pending stores. A vouch blocked on an
unreachable peer retries the next time the app has a connection, with no timer
and no coordination.

This is deliberately **not** a server-side outbox. There is no retry machinery
in the relay layer, no durable queue, and no general background job runner —
the only tickers in the codebase are the realtime service's ws ping and client
reaper, and `pending_events` is `UNLOGGED` and so not durable anyway. Putting
the retry on the client avoids building all of that, and it puts the queue
where the evidence already is: the client holds the signature, so the client
can always reconstruct the request. V holding a half-created vouch it must
chase would be new infrastructure owning state the client can own for free.

The `POST` is already idempotent ([03](03_api.md#post-vouches)), so replaying
it is safe however many attempts it takes.

### What S checks

S does not trust V's word on any of it. Before storing a reference S:

1. Confirms the caller is an established peer — `signatureAuthMiddleware`
   already does this, resolving a peer server-key signature into
   `peerServerID` after `VerifyFederationPeer`.
2. Confirms the **subject** named is a local user of S. S stores references
   about its own users only.
3. Confirms the **voucher** named belongs to the calling server, the
   `peerRequestIDMatchesPeer` guard the relay legs already apply. A peer may
   only announce vouches authored by its own users.
4. Runs the full client-side verification order over the cert
   ([02](02_payload.md#verification-order-client-on-display)): resolves the
   voucher's key from V, verifies the detached signature over the rebuilt user
   payload, and verifies V's countersignature against V's own key.

The **cert travels in the notify body** for step 4, rather than S fetching it
back by id. It has to: V has not stored the row yet at that point — it stores
only once S confirms — so there is nothing for S to fetch. The cert is in the
body to be *verified*, not to be stored; S keeps only the id and V's
`serverID` once the signatures check out, and throws the rest away.

That is what makes the notify unforgeable by a peer that has the subject right
and the signatures wrong, and it means S's reference list only ever names
vouches that really were signed by the voucher they claim. A notify failing any
step is rejected, and S distinguishes "wrong, never retry" from "try again
later" in the status so the client is not left retrying a vouch S will never
accept.

### Withdrawal delivers the same way

A withdrawal is confirmed on the same terms, for the same reason: an
undelivered withdrawal leaves a retracted vouch discoverable, which is worse
than an undelivered vouch. S verifies the withdrawal signature against the
voucher's current key on V, then drops the reference from the list it serves.

The retry is the client's here too. A `DELETE` that cannot reach S fails, and
the withdrawal stays queued client-side and is replayed on the next
connection — the same shape as `pendingUnlike` and the other pending stores,
and the reason withdrawal is a signed cert rather than a bare delete
([01](01_schema.md#withdrawal-is-a-state-not-a-delete)) is what makes replaying
it safe.

The cert on V is retained with `withdrawn_at` set, as
[01](01_schema.md#withdrawal-is-a-state-not-a-delete) requires — S dropping a
reference is not the record of the retraction, V's signed withdrawal is. A
reader holding the vouch locally sees the id vanish from S's list and fetches
it once from V to obtain the signed withdrawal, which is exactly the flow
[07](07_spa_trust_display.md#verifying-what-the-server-reports) already
specifies for a local withdrawal.

### Routing the reads

The reads clients need are already peer-callable. Every `/api/*` route
authenticates by signature, which is how `fetchAndCachePeerUserKey` reaches
`GET /api/keys/{id}` today without a bespoke relay endpoint. So the vouch
reads need routing, not new surface:

- **`GET /users/{userID}/info`** — already proxies, and therefore already
  returns S's `vouchIDs`. Unchanged; the fix is that the vouch reads now agree
  with it.
- **`GET /users/{userID}/vouches/{vouchID}`** — the cert read. Routes to **V**,
  the server in the **vouch id**, because that is where the cert lives. This is
  the one read that keys off the vouch id.
- **`GET /users/{userID}/vouches`** — the list read for a foreign subject.
  Returns S's references; the caller then fetches each cert. It does not
  assemble certs on the subject's behalf, because S does not have them.

Two notify endpoints are new, for the reference and the withdrawal, following
`relayLeg`/`callPeerRelayEndpoint` — but awaited, with the outcome propagated
to the caller rather than swallowed on failure.

### Routing the cert read on the vouch id is not an exception

Everywhere else in syrinx an id is used whole and routed on the thing it
names, and that is precisely what happens here. The vouch id is
`voucherUserID@serverID/uuidv7` — the voucher authored the statement, so the
id names V, and a cert read routes to V. The `{userID}` param is the subject,
used as the subject check and nothing else. Two ids, two different purposes,
neither derived from the other and neither reassembled from fragments — the
discipline [03](03_api.md#get-usersuseridvouchesvouchid) already states.

`isVouchIDWellFormed` is already server-agnostic: it checks the form and the
UUIDv7 only, unlike `validateVouchID`, which pins the minting server and is
correctly used on the create path alone. So a foreign vouch id passes shape
validation on the read path and is passed to V whole.

The subject check still runs on V, which holds the row, and V returns its own
**404** when the vouch does not name that subject. A proxying server adds
nothing and verifies nothing about the pair; it is transport.

### The reader must be peered with V

A reader on R reading a vouch by a voucher on V needs the voucher's key from V
to verify the signature. If R is not peered with V, it cannot fetch the key
and cannot verify the cert — and an unverified vouch is never counted
([07](07_spa_trust_display.md#verifying-what-the-server-reports)).

v1 therefore **does not count** a vouch whose V the reader's server is not
peered with. S may list the reference; R cannot resolve it, and the client
treats it as it treats any cert that fails to verify — not counted, not
retried on a loop, and not rendered as suspicion.

The alternative — R asking S to relay the cert and vouch for its validity —
reintroduces exactly the trusted intermediary
[§ The cert never leaves V](#the-cert-never-leaves-v) removes. A vouch R
cannot check itself is worth nothing to R, so v1 shows fewer marks and every
mark it shows is backed by signatures that device verified.

Deferred rather than rejected. The honest fix is transitive peering or a
reader-side fetch that does not require peering, and both are their own
design.

### The local row for a foreign counterparty

[01](01_schema.md) already permits a foreign `subject_user_id`: no FK,
precisely so a vouch may name a user on another server. This leg adds the
mirror case, and the two are different shapes because S stores a reference,
not a cert:

- **Authored row on V** — full cert, `voucher_user_id` local, subject possibly
  foreign. Exactly the row [01](01_schema.md) already specifies, with no extra
  delivery columns: it is written only once delivery has succeeded.
  `/vouches` serves it.
- **Reference row on S** — vouch id, voucher's `serverID`, subject local. No
  signatures. Feeds `vouchIDs` and the list read.

Because a reference is not a cert, it does not belong in `user_vouches` — it
shares almost no columns with it, and putting it there would mean a table
where the signature FKs are sometimes null and the row is sometimes not a
vouch. It gets its own table, keyed by vouch id, indexed on the local subject.
The `voucher_user_id` FK on `user_vouches` therefore **stays**: every row in
that table still has a local voucher.

### Account removal across servers

[README](README.md#resolved) records this: a vouch naming a foreign subject
would otherwise outlive that account's removal, because this server never
learns the peer deleted them.

Federation already notifies — account removal fans out to peers
(`notifyForeignAccountRemovalToPeers` / `AccountRemovalNotifyFromPeer`) — so
the cleanup rides that notify rather than adding a vouch-shaped fan-out:

- A removed **subject** on S, seen by V → the stored vouch naming them is now
  stale, since the key it names belongs to an account nobody can present. The
  row is retained: it is the voucher's own audit record, and
  [04](04_revocation.md) already retains stale rows rather than deleting them.
  A vouch for that subject still sitting in a client's pending queue will now
  be refused outright by S, which is the "never retry" status above, so the
  client drops it from the queue and tells the voucher why.
- A removed **voucher** on V, seen by S → drop the reference rows naming them.
  The certs are gone with the account, so the references resolve to nothing.

A local voucher's own removal still cascades their authored rows
([README](README.md#resolved)).

### Key revocation stays pull-based

A vouch goes stale when the subject key is revoked or rotated
([04](04_revocation.md)), and nothing is pushed: keys are never broadcast, and
staleness is derived on read ([01](01_schema.md#void-and-stale-are-derived-not-stored)),
so there is nothing to propagate. A client resolves the subject's key through
S and the voucher's through V, then applies [04](04_revocation.md)'s rules
itself. Cross-server changes nothing, because the rules never depended on
locality.

### Relay refusal across servers

This is where the step produces security benefit rather than a badge, and it
is the reason the read path has to work at all.

Cross-server reed relay encrypts to whatever key the **peer** reports as the
requester's active key ([federation 06](../federation/06_content_relay.md)).
That is H1 with one more server in it: either the requester's home server or
the relaying peer can substitute the key, and the encrypting client's own
server is not necessarily the liar.

The rule is unchanged from [07](07_spa_trust_display.md#relay-refusal) and
needs no federation-specific logic: if the client holds a live, non-void vouch
naming a **different** key for that user, it refuses to encrypt and surfaces
the mismatch. A contradiction is a contradiction regardless of which server
served the key. Absence of a vouch is still not grounds to refuse.

What federation changes is that the rule was previously *unreachable* for
foreign users, because the client could never obtain their vouches.

### What the client does differently

**Reads are unchanged in shape.** `vouchVerify.ts`, the reconcile loop, and
mark computation all address users by canonical id and call the client's own
home server, which resolves foreign ids by proxying. The client keeps one
origin, one pinned server key, one auth path — a client fetching from
arbitrary peers would have to pin every peer's key and would leak which
servers its user browses.

**Creation gains a pending state**, because the `POST` can now fail on a hop
the voucher has no control over. The vouch is already queued in
`pendingVouches` before the `POST` is attempted (`createVouch` in
`vouches.ts` writes the record, then deletes it on success), so the state
already exists — this leg makes it visible and gives it a cause.

Marks, roots, and depth-1 reachability are unchanged. A root on another server
is a root; the set intersection does not care where the voucher lives.

### The voucher's UI shows the attempt is pending

The voucher is the only person who knows anything happened, so they are the
only person to tell.

After signing a vouch for a foreign subject that has not yet been confirmed,
the voucher sees it as **pending** — an attempt in progress, not a completed
verification:

- On the subject's profile, where the blue check would go: the vouch is
  queued, not live. No blue check, because no vouch exists anywhere yet.
- In the audit list ([07](07_spa_trust_display.md#your-vouches-chronologically)),
  which is where the voucher checks what their key signed. A pending vouch is
  listed with its own state, distinct from live, stale and withdrawn.
- With a cause, not just a spinner: the subject's server could not be reached.
  Retrying happens on its own and needs no button, but the reason should not be
  a mystery, and a vouch S has refused outright must say so rather than
  appearing to retry forever.

The copy has to be careful in the one way this whole spec is careful: pending
means *not yet verified by anyone*, and it must not read as a weaker checkmark.
Nothing is displayed to anyone else, so there is no risk of a mark that later
downgrades — the risk is the voucher believing they are done when they are not,
and coming away thinking they verified someone when no evidence of it is
discoverable.

The pending entry is per-key like the store itself (`compositeKey` is the
`subjectKeyID`), so a pending vouch for a rotated-away key is naturally
distinct from one for the current key.

### The subject learns nothing until it completes

No notification, no hint, nothing on the subject's side while a vouch is
pending. The subject's server does not have the reference yet — that is the
definition of pending — and there is nothing to tell them about.

This is not a limitation to work around; it is the correct behavior. A vouch
that has not been delivered is a vouch that does not exist for anyone but its
author, and telling Bob that Alice *might have* vouched for him would be
reporting an event that may never happen, sourced from a server that cannot
prove it. When delivery completes, the subject learns of the vouch through the
existing notification path ([README](README.md#protocol-sketch) step 5), which
is the same path a local vouch uses and needs no federated variant.

So the ordering is: the voucher sees pending, then delivered; the subject sees
nothing, then a vouch. Neither ever sees a half-state.

## Testing

**Delivery**

- Create a vouch for a foreign subject → S confirms, V stores the row, the
  client receives the cert.
- S unreachable → V stores **nothing**, the `POST` fails, and the vouch stays
  queued in `pendingVouches`.
- The queued vouch is replayed on the next connection by the existing
  `syncPending` flush, with no new timer or worker.
- A replayed `POST` that succeeds is idempotent: one row on V, one reference on
  S, one cert.
- A vouch S refuses outright is dropped from the client queue rather than
  retried forever.
- No row on V is ever in a stored-but-undelivered state.

**What S accepts**

- A notify naming a subject who is not local to S is rejected.
- A notify naming a voucher that does not belong to the calling server is
  rejected.
- A notify carrying no cert, or a cert whose id does not match the reference,
  is rejected and no reference is stored.
- S stores the id and V's `serverID` only; the cert from the notify body is
  verified and then discarded.
- A notify whose voucher signature fails verification is rejected.
- A notify whose countersignature fails against V's key is rejected.
- A notify from a server that is not an established peer is rejected by the
  auth middleware.

**Reads**

- `GET /users/{userID}/vouches` for a foreign subject returns S's references,
  not an empty local list.
- `GET /users/{userID}/vouches/{vouchID}` routes to the server named in the
  **vouch id**, not the subject's.
- V's subject-mismatch **404** surfaces unchanged through the proxy.
- S serves no signatures on the list read; a client that asks S for a cert
  gets nothing it could count.

**Withdrawal**

- Withdrawal confirmed → reference dropped from S, row retained on V with
  `withdrawn_at`.
- S unreachable on withdrawal → the `DELETE` fails, the withdrawal stays queued
  client-side, and the vouch stays discoverable until a replay succeeds.
- A replayed withdrawal is idempotent.
- A reader holding the vouch sees the id vanish and fetches the signed
  withdrawal from V rather than inferring it from the omission.

**Peering and third parties**

- A vouch whose V the reader's server is not peered with is not counted, and
  renders no mark and no warning.
- A vouch whose voucher is on the reader's own server, or on S, is counted
  normally.

**Removal and revocation**

- Peer account-removal notify for a subject leaves the voucher's stored row in
  their audit list, marked stale rather than deleted.
- A queued vouch for a subject whose account was removed is refused by S and
  dropped from the client queue with a reason shown.
- Peer account-removal notify for a voucher drops S's reference rows for them.
- A local voucher's own account removal still cascades their authored rows.
- A foreign subject's key revocation makes the vouch stale on the client with
  no push involved.

**Client**

- Cross-server relay refuses to encrypt on a contradicting vouch for a foreign
  user, and proceeds when the client holds no vouch.
- The audit list shows a pending vouch in its own state, distinct from live,
  stale and withdrawn, with the reason delivery has not completed.
- A pending vouch renders no blue check on the subject's profile.
- The subject is told nothing while a vouch is pending, and learns of it
  through the existing notification path once delivered.
- The SPA issues no request to any origin but its own while rendering a
  foreign profile's marks.
- A feed mixing local and foreign authors issues no vouch requests.
