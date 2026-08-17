# Scope — make canonical `userID@serverID` apply everywhere (wire, URLs, signatures)

Companion to [`ANALYSIS_identity_indirection.md`](ANALYSIS_identity_indirection.md)
and [`PROGRESS_identity_indirection.md`](PROGRESS_identity_indirection.md).
Read both first — this doc assumes everything described there as "Done"
is actually done and committed (it is, as of commit `e068247` on
`canonical`, per `PROGRESS_identity_indirection.md`'s own record).

**This doc reverses one specific decision made during that prior work,
per explicit user correction — read the next section before anything
else.**

## Why this doc exists

During the `identities` table refactor, the implementation (this
assistant, mid-session, without checking) carved out an exception: while
`identities.id = "userID@serverID"` became the canonical FK target
everywhere in the database, `users.id` itself — and every wire-facing
(JSON/URL) `userID` field, and every cryptographically signed payload —
was kept as the **bare** userID string. The reasoning at the time was
"changing the signed byte format breaks existing signature verification."

The user corrected this directly: **"canonical" was never meant to apply
only at the DB/FK layer. It means everywhere** — the wire format, URLs,
and signed payloads must also use `"userID@serverID"`, not just
`identities.id`. The "don't break existing signatures" reasoning that
motivated the exception doesn't even apply on its own terms — Syrinx is
pre-launch blank slate, there are no production signatures to preserve
(see `specs/federation/ANALYSIS_identity_indirection.md` and the
project's own README language: "no migration, no backwards
compatibility").

**Locked decision recorded here:** `users.id` becomes
`"userID@serverID"`. Every wire-facing field that currently holds a bare
userID becomes the canonical form. Every signed payload signs over the
canonical form. There is no bare-userID exception left anywhere.

## Status: scoping only, no implementation yet

The inventory below was produced by a full-repo Explore-agent search
against the codebase as it stood after commit `e068247` (the point where
the prior, narrower refactor was declared "done"). **Nothing in this doc
has been implemented.** This is the "what needs to change" list the user
asked for before deciding how to sequence the actual work.

Do not trust line numbers below as exact once any implementation starts —
re-grep to confirm current locations, since this file will drift out of
sync with the code the moment edits begin.

## The inventory, by category

### (a) `users.id` treated as bare / unconverted — needs to become canonical

**Schema:**
- `db.go` — `CREATE TABLE users (id VARCHAR(255) PRIMARY KEY, identity_id VARCHAR(255) NOT NULL UNIQUE REFERENCES identities(id) ...)`. The doc comment directly above this DDL is the design note that needs reversing — it currently says `id` stays bare "because it's embedded verbatim in signed wire payloads." Once payloads sign the canonical form (category e), this reasoning goes away and `users.id` itself should just **be** `identities.id` (i.e., `users.id` and `identity_id` may become redundant — worth deciding at implementation time whether `users.id` is dropped in favor of `identity_id` as the PK, or kept equal to it. See "Open design question 1" below).

**Every query filtering/selecting bare `users.id` that will need to shift to the canonical form** (this list was accurate as of `e068247`; re-verify before editing):
- `services.go` — `UserServerSignedAt`, `GetUserProfile`, `GetUserInfo`, `GetActiveKeyFingerprint`, `GetUserRole`, `UpdateUser` (multiple statements), `DeleteUser`, `AddPublicKey`'s `UPDATE users SET user_fingerprint`, `MentionTargetValid`, `SearchUsers` (feeds `UserSearchResult.ID` directly, bare, no `.UserID()` — see the flag below, this one is easy to miss), `ListFederationInvitations`'s `creator.id`/`reviewer.id` selects.
- `recovery/upsert.go` — `claimUsername`'s `WHERE u.id <> $2` collision check.
- `deletion/account.go`, `deletion/store.go` — signature-lookup helpers joining through `users` by row id.

**Signup INSERT (mints the id in the first place):**
- `services.go`'s `Signup` — `INSERT INTO users (id, identity_id, ...)` currently inserts `in.UserID` (bare) as `id` and `selfIdentity` (canonical) as `identity_id`. Once `users.id` is canonical, decide whether `id` and `identity_id` both get the canonical value (redundant) or `identity_id` is dropped.
- `recovery/upsert.go`'s equivalent `insertUser`.

**Bare-userID minting/generation (the actual origin point):**
- `handlers.go`'s `GenerateUserID` — generates a fresh bare userID via `crypto.NewID()` (`crypto/ids.go`), returns `{"userID": userID}` on the wire, which the client then builds its OpenPGP identity around. **This is upstream of everything else** — if the client mints/receives a bare ID here and builds a keypair identity string around it before ever learning the serverID, the sequencing of "when does the client learn its own canonical id" needs explicit design (see Open design question 2).

### (b) `.UserID()` call sites — every one of these currently strips `@serverID` before a wire-facing field; all need to stop doing that (keep the canonical form instead)

Every one of these was added during the prior pass specifically to decode
canonical → bare for a wire-facing struct field. Reversing the exception
means **deleting** these `.UserID()` calls (or replacing them with
`.String()`/direct use of the `IdentityID`), not adding more.

**services.go** (struct field populated → HTTP endpoint):
- `Key.UserID` (`json:"userID"`) ← `GetPublicKey`, `AddPublicKey` — `GET /users/{userID}/keys/{fingerprint}`, `POST /keys`
- `KeyRevocation.UserID` ← `GetKeyRevocation` — `GET .../keys/{fingerprint}/revocation`
- `ReplyListItem.UserID` ← `ListReplies` — `GET /reeds/{userID}/{reedID}/replies`
- `FollowListItem.UserID` ← `listFollowEdge` — `GET /users/{userID}/following`/`/followers`
- `Reed.UserID` ← `insertReedCoreTx`, `GetReed` — reed-create response, `GET /reeds/{userID}/{reedID}`
- `EchoerListItem.UserID` ← `GetReedChorus` — echo-list endpoint
- `ReedRef.AuthorID` ← multiple (`CreateReedWithEcho`, reply-count-notify chain) — feeds realtime pub/sub composite strings (see flag below, `ReedRef` is a special case)
- `LikeCert.AuthorID` (`json:"authorID"`) ← `loadLikeCertTx` — like-cert response
- `[]string` returned by `ListUserFollowing` ← feeds `bootstrapAccountRecoveryResponse.Following`
- `Invite.CreatedBy` (federation invite) ← `ListFederationInvitations`
- `Ripple.ReedAuthorID`/`.UserID` ← `scanRipple` — feeds `RippleWire.UserID` on ripple endpoints

**deletion/account.go, deletion/store.go:**
- `AccountCert.UserID` ← feeds `AccountRemoval.UserID`/`AccountRemovalWire.UserID` (HTTP + WS)
- `Cert.UserID` ← feeds `ReedRemoval.UserID`/`ReedRemovalWire.UserID` (HTTP + WS)

**realtime/db.go** (all feed WS wire messages or dispatch targeting):
- `GetOnlineFollowers`'s follower list, `PendingEvent`/`PendingReedEvent`/`PendingAccountEvent`'s `RequesterUserID`/`UserID` fields, `GetOnlineReedHolder`'s holder return, `UnallocatedReed.AuthorID`, `ProfileSubscriber.ViewerUserID`, broadcast-subscriber fan-out list, `ReedCoverageTarget.AuthorUserID` (feeds `ReedCoverageMsg.UserID`), plus the same `deletion.GetCert`/`GetAccountCert` re-entry points as above.

**invites/store.go:**
- `Invite.CreatedBy`/`.ClaimedBy` ← `scanInvite` — `ClaimedBy` confirmed wire-facing via `invites/handlers.go`'s `statusResponse.ClaimedBy` (`GET /invites/{id}`); `CreatedBy`'s current wire exposure unconfirmed, flagged for verification at implementation time (see flags below).

### (c) Every JSON-tagged struct field representing a person's identity — full list, by file

This is the field-level checklist for what changes shape from `string`
(bare) to canonical form (either `identity.IdentityID` or `string`
containing the canonical form, depending on the Go-typing decision made
at implementation time — see Open design question 3).

**db.go:** `User.ID`, `UserInfo.ID`, `InvitedBy.ID`, `Key.UserID`, `KeyRevocation.UserID`, `Reed.UserID`, `ReedRemoval.UserID`, `AccountRemoval.UserID`, `LikeCert.AuthorID`.

**services.go:** `ReplyListItem.UserID`, `FollowListItem.UserID`, `UserSearchResult.ID` (bare leak, no `.UserID()` — easy to miss), `EchoerListItem.UserID`, `ReedRef.AuthorID` (special case, see flag), `Invite.CreatedBy`/`.ClaimedBy` (federation), `Ripple.UserID`/`.ReedAuthorID` (untagged internal, feed `RippleWire`).

**handlers.go:** `bootstrapAccountRecoveryRequest.UserID`, `RippleWire.UserID`, `bootstrapAccountRecoveryResponse.Following []string`.

**recovery/wire.go:** `Profile.ID`, `InvitedBy.ID`, `KeyWire.UserID`, `Revocation.UserID`, `ReedRequest.AuthorID`, `FollowingRequest.UserIDs []string`.

**realtime/wire.go, realtime/messages.go:** `ReedRemovalWire.UserID`, `AccountRemovalWire.UserID`, `InboundJSONMsg.UserID`, `ReedStatsMsg.UserID`, `ReedCoverageMsg.UserID`, `ReedEchoesMsg.UserID`, `ReedRepliesMsg.UserID`, `ReedLikesMsg.UserID`, `RippleWire.UserID`, `RipplePostedMsg.UserID`/`RippleUpdatedMsg.UserID`, `RequestReedData.AuthorID`, `RelayRequestData.AuthorID`, `DataResponseData.UserID`, `KeyFetchErrorData.UserID`/`RevokedKeyUsedData.UserID`, `SubscribeProfileData.UserID`/`UnsubscribeProfileData.UserID`, `ReedNotHeldData.AuthorID`.

**invites/handlers.go:** `statusResponse.ClaimedBy`.

**wire.go (federation):** `federationListItemWire.CreatedBy`, `.ReviewedBy`.

### (d) HTTP routes with a userID path parameter (main.go) — every one now carries the canonical form in the URL

| Route | Methods | Handler |
|---|---|---|
| `/users/{userID}/profile` | GET | `GetUserProfile` |
| `/users/{userID}/info` | GET | `GetUserInfo` |
| `/users/{userID}/follow` | POST, DELETE | `FollowUser`, `UnfollowUser` |
| `/users/{userID}/following` | GET | `GetUserFollowing` |
| `/users/{userID}/followers` | GET | `GetUserFollowers` |
| `/users/{userID}/keys/{fingerprint}` | GET | `GetPublicKey` |
| `/users/{userID}/keys/{fingerprint}/revoke` | POST | `RevokeKey` |
| `/users/{userID}/keys/{fingerprint}/revocation` | GET | `GetKeyRevocation` |
| `/reeds/{userID}/{reedID}` | GET, DELETE | `GetReed`, `DeleteReed` |
| `/reeds/{userID}/{reedID}/echoes` | GET | `GetReedEchoCount` |
| `/reeds/{userID}/{reedID}/chorus` | GET | `GetReedChorus` |
| `/reeds/{userID}/{reedID}/replies` | GET | `GetReedReplies` |
| `/reeds/{userID}/{reedID}/like` | POST, DELETE | `LikeReed`, `UnlikeReed` |
| `/reeds/{userID}/{reedID}/ripples` | POST, QUERY | `PostRipple`, `GetRipples` |

**A real design/UX question, not just mechanics:** a URL path segment
containing `@` (e.g. `/users/u53r1d@srv123/profile`) needs URL-encoding
consideration (`@` is a valid path character per RFC 3986 and doesn't
strictly need percent-encoding, but verify how `mux`, the SPA's router,
and any reverse proxy/CDN in front of this handle a literal `@` in a path
segment before assuming it's a drop-in change) — see Open design
question 4.

Non-userID path params, unaffected: `/server/keys/{fingerprint}`,
`/federation/invitations/{id}/revoke`, `/federation/connect/{id}`,
`/invites/{id}`, `/ripples/{rippleID}`.

### (e) `identity/identity.go` payload builders — highest-risk category, cross-repo (server + client)

Every `Build*Payload` function that takes a `userID`/`authorID`-shaped
param currently embeds the **bare** form in the signed byte sequence.
This is the category most likely to break silently if the server and
client don't change in lockstep, since signature verification happens on
both sides independently.

Full list of affected functions (all in `identity/identity.go`):
`BuildProfilePayload`, `BuildReedPayload`, `BuildPublicKeyPayload`,
`BuildUserRevocationPayload`, `BuildServerRevocationPayload`,
`BuildReedRemovalUserPayload`, `BuildReedRemovalServerPayload`,
`BuildReedLikeUserPayload`, `BuildReedLikeServerPayload`,
`BuildAccountRemovalUserPayload`, `BuildAccountRemovalServerPayload`,
`BuildInviteUserPayload`, `BuildInviteServerPayload`,
`BuildNewProfilePayload`, `BuildRippleUserPayload`,
`BuildRippleServerPayload`.

Not affected (no userID-shaped param): `BuildUserIdentityPayload`,
`BuildFederationInvitationPayload`, `BuildFederationConnectPayload`.

The package doc comment at the top of `identity/identity.go` currently
states outright that wire payloads are "UNCHANGED by this type's
existence" and "keep signing the bare userID" — this sentence is the
exact design note that needs to be reversed as part of this work, along
with every comment on every `.UserID()` call site elsewhere (category b)
that documents the same now-reversed reasoning.

**This is also the one category where the SPA (client) must change in
the same breath as the server** — the SPA independently reconstructs
these same header/payload byte sequences to produce signatures the
server verifies, and to verify signatures the server produces. If the
server starts signing over `"userID@serverID"` and the client still
builds/verifies against bare `userID`, every signature check fails. This
is not sequenceable as "backend first, then frontend" the way the
`identities` FK work was — category (e) needs server and client changed
together, or behind a temporary dual-check (verify against both forms) if
a staged rollout is wanted. See Open design question 5.

### (f) `roles.RootUserID` and hardcoded literal userID constants

- `roles/roles.go` — `const RootUserID = "1"`, doc-commented as "the
  reserved users.id for the operator root account." Every comparison site
  (`IsRoot`, `RoleForSignup`, `SignupRole`, `ValidateProfileRole`) compares
  a `userID string` parameter against this literal.
- `main.go`, `root.go`, `handlers.go` — every consumer of `roles.RootUserID`,
  including `root.go`'s bootstrap/export flow, which **already manually
  constructs `roles.RootUserID + "@" + serverID`** for the OpenPGP
  identity name (worth using as the template for what the constant itself
  should become, or for how comparisons should be restructured — see Open
  design question 6).

## Flags — things that don't fit the simple "find and flip" pattern

1. **`UserSearchResult.ID`** (`services.go`) is a bare-`users.id` wire leak
   with no `.UserID()` call at all, because the DB column itself is bare
   today. An audit pass driven only by grepping `.UserID()` will miss
   this class of bug — must also audit every raw `SELECT ... u.id ...`
   that flows into a wire struct.
2. **`ReedRef`** (`services.go`) is a hybrid: `AuthorID` is bare today, but
   it's formatted into a composite string `"userID@serverID/reedID"` for
   realtime pub/sub via `FormatReedRef`/`ParseReedRef` — a **third**
   representation, not `IdentityID`, not plain-bare, with a `/reedID`
   suffix. `identity.ParseIdentityID`'s last-`@`-index splitting logic is
   only safe today because `crypto.Alphabet` excludes `@`; the `/reedID`
   suffix on this composite format needs its own explicit handling, not a
   blind "make `AuthorID` an `IdentityID`" swap.
3. **`invites/store.go`'s `Invite.CreatedBy`** — does not appear to
   currently surface on any invites HTTP response body
   (`statusResponse`/`createResponse`/`checkResponse` don't include it).
   Confirm whether it's genuinely dead/future-use before assuming it
   needs conversion, or whether some response path this scoping pass
   didn't catch actually exposes it.
4. **`root.go`'s `openPGPName := roles.RootUserID + "@" + serverID`** is
   the one place that already manually builds a canonical-looking string
   — worth reconciling with `identity.LocalID(roles.RootUserID, serverID)`
   for consistency, even though this specific string is for OpenPGP UID
   naming, not an `identities.id` lookup, and may have different
   formatting constraints (OpenPGP UID conventions vs. this refactor's
   `@`-join).
5. Every `.UserID()` call site added during the prior pass carries an
   explanatory comment documenting *why* the bare exemption existed. All
   of these comments need rewriting/removal, not just the code around
   them — stale comments asserting a now-false design rule are worse than
   no comment.

## Locked decisions (answered by the user — do not re-litigate, do not reintroduce staged/compat framing)

1. **`users.id` is dropped.** `identity_id` (i.e. `identities.id`) is the
   only PK-shaped identity column on `users` going forward. No redundant
   bare `id` column survives. Every query/struct that referenced
   `users.id` as a bare lookup key needs to target `identity_id` instead
   — or, more likely, `users` no longer needs its own `id` column at all
   and callers should carry `identities.id` directly.
2. **Client→server id handshake:** the client generates a bare id for its
   own keypair/local use, sends it to the server (signup), and the
   **server responds with the canonical `"userID@serverID"` form**. The
   client never composes the canonical form itself — it only ever stores
   and re-uses whatever the server returned. This applies to
   `GenerateUserID`/signup and any other place a client currently mints
   an id it will later need in canonical form.
3. **Wire-facing JSON fields stay plain `string`.** No `identity.IdentityID`
   type at the JSON/wire layer — that type (and its `.UserID()`/
   `.ServerID()`/parsing methods) is a Go/DB-internal convenience only.
   Wire fields just happen to contain the canonical string value.
4. **No URL-encoding of `@`** in path segments. `/users/u53r1d@srv1d/profile`
   is a literal, valid path as far as this project is concerned — ship it
   as-is, don't add percent-encoding handling anywhere for this.
5. **No staged rollout, no dual-format acceptance, no transition window,
   anywhere, ever, on this project.** This is not specific to category
   (e) — it is the standing rule for the whole codebase (see memory:
   `feedback_blank_slate_no_migration_questions.md`, "second occurrence,
   escalated"). Change the server and the client together, atomically,
   in the same pass. Do not design, mention, or leave room for a
   "verify against both forms" fallback anywhere in this implementation.
6. **`roles.RootUserID` cannot be a canonical compile-time constant**
   (serverID is runtime-only) — so it stays the bare literal `"1"`, used
   differently on each side:
   - **Frontend**: comparing the bare userID part against `"1"` is
     sufficient — the client only ever reasons about its own account.
   - **Backend**: must compare the full canonical identity (bare part
     **and** serverID) — i.e., resolve/construct this server's own root
     identity (`identity.LocalID("1", thisServerID)`) and compare against
     that, never just string-match the bare `"1"` alone. This is a
     security requirement, not a style preference: a bare-only comparison
     would let an admin from a *different*, federated instance whose
     remote userID happens to also be `"1"` get treated as this
     instance's root. Every one of the ~6 comparison sites in category
     (f) (`IsRoot`, `RoleForSignup`, `SignupRole`, `ValidateProfileRole`,
     plus call sites in `main.go`/`root.go`/`handlers.go`) needs to be
     re-examined against this rule specifically.

## SPA / client-side inventory (mirrors backend categories a-f above)

Researched via a full Explore pass through `spa/src`. Do not trust exact
line numbers once implementation starts — re-grep to confirm.

### (g) SPA signing/verification payload builders — direct mirror of backend (e)

**`spa/src/lib/services/signing.ts`** is the client's `identity/identity.go`
equivalent. `bytesToSign(headers, content)` (lines 59-73) is the exact JS
mirror of the Go backend's `signing.BytesToSign` — the one choke point
every payload funnels through. It treats header values opaquely, so it
needs no change itself; every **caller** passing a bare userID into a
`userID`/`authorID`/`reedAuthorID`/`rippleAuthorID` header does.

Every `build*Payload` function in `signing.ts`, all currently receiving
**bare** userIDs at every call site: `buildProfilePayload`,
`buildReedPayload`, `buildPublicKeyPayload`, `buildUserRevocationPayload`,
`buildServerRevocationPayload`, `buildReedRemovalUserPayload`,
`buildReedRemovalServerPayload`, `buildReedLikeUserPayload`,
`buildReedLikeServerPayload`, `buildAccountRemovalUserPayload`,
`buildAccountRemovalServerPayload`, `buildInviteUserPayload`,
`buildInviteServerPayload`, `buildRippleUserPayload`,
`buildRippleServerPayload`. (`buildUserIdentityPayload`/
`buildNewUserIdentityPayload` take no userID param — identity established
via signature/session, not embedded — no change needed, matches backend's
`BuildUserIdentityPayload`.)

**Call sites** (every one passes a bare userID today, sourced from a wire
`api.*` object field or `localStorage.getItem('userId')`):
`spa/src/lib/verifiers/index.ts` (the central verification module —
`verifyPublicKey`, `verifyKeyRevocation`, `verifyUser`, `verifyReed`,
`verifyRipple`, `verifyReedRemoval`, `verifyReedLike`,
`verifyAccountRemoval`, `verifyInvite`), `spa/src/lib/services/reedLike.ts`,
`reedRemoval.ts`, `invites.ts`, `accountRemoval.ts`,
`spa/src/lib/components/RippleComposer.svelte`,
`spa/src/routes/signup/+page.svelte`, `spa/src/routes/account/+page.svelte`,
`spa/src/routes/profile/[userId]/+page.svelte`.

`spa/src/lib/services/verify.ts`'s generic `verify(serverSignature,
payload)` needs no change — it verifies an already-built payload string
against the server's countersignature; the risk is entirely upstream in
how `payload` gets built (the callers above).

### (h) A SECOND, independent signed-byte surface the backend-only scoping pass could not have found — HTTP per-request signatures

**`spa/src/lib/services/request-signer.ts`** (`signRequest`,
`buildCanonicalRequestString` — a private method) and
**`request-signer-shared.ts`** (a near-duplicate
`buildCanonicalRequestString` used in a service-worker context) build a
**separate** signed string per HTTP request — distinct from the
resource-level payloads in (g). This canonical request string embeds the
**literal URL path** (`urlObj.pathname + urlObj.search`), which for
endpoints like `getUserProfile`/`deleteReed`/`likeReed` currently contains
`/users/{userID}/...`/`/reeds/{userID}/{reedID}/...` with a bare userID.

**This is the most important finding of the SPA research pass**: once
category (d) (backend URL routes) switches to canonical
`/users/{userID@serverID}/...`, this per-request signature's input bytes
change shape automatically, since it signs the literal path — but that
change has to be **exactly** matched on both sides or every signed
request fails verification. `X-Syrinx-User-Id` header (bare, `request-
signer.ts` line ~358) is explicitly excluded from the signed bytes
(stripped before signing) but is trusted server-side as an out-of-band
claim — decide whether this header's value also becomes canonical (for
consistency) even though it isn't itself part of the signed bytes.

### (i) IndexedDB (`spa/src/lib/services/db.ts`) — confirms the original Task 10 survey, now definitively in scope

Every user-referencing store keyed by bare userID, confirmed unchanged
since the original survey: `users`/`usersInfo` (keyPath `id`),
`following`/`unfollow`/`pendingFollows` (keyPath `userId`), `reeds`
(compound keyPath `[userID, id]`), `tags` (embedded `{userID, id}` refs),
`removedAccounts` (keyPath `userID`). No call site anywhere upgrades a
bare userID to canonical form before writing to these stores — every
write takes whatever the API response contained verbatim. This means:
once the API starts returning canonical userIDs (per locked decision 2:
server responds with canonical form, client just stores what it's given),
**these stores start receiving canonical values automatically, with no
client-side composition logic needed** — but the stores' own keying
scheme, and every reader that assumes a bare shape, still needs updating
to match (e.g. `reeds.ts`'s `getReed(userId, reedId)` calls, `user.ts`'s
`getByUserId`, `following.ts`'s methods — see the full call/read/write
inventory the research pass produced for exact locations).

### (j) API client (`spa/src/lib/services/api.ts`) — same route list as backend (d), client side

Every function building a `/users/{userID}/...` or
`/reeds/{userID}/{reedID}/...` URL: `getUserProfile`, `getUserInfo`,
`getUserProfileWithStatus`, `getUserInfoWithStatus`, `createUserKeys`,
`getReedEchoCount`, `listReplies`, `getReed`, `listEchoers`, `listRipples`,
`postRipple`, `listFollowing`, `listFollowers`, `getReedOrRemoval`,
`deleteReed`, `likeReed`, `unlikeReed`, `revokeKey`, `getKeyRevocation`,
`followUser`/`unfollowUser`, `getPublicKey`. All bare today; none compose
canonical form client-side (correct, per locked decision 2 — the server
is the sole source of the canonical form).

Also: `signup()` POSTs a bare `userID` **form field** sourced from
`apiService.getUserID()` (`GET /users/id`, a server-reserved-bare-id
endpoint) — this is where a brand-new bare id first enters the client,
before signup completes. Per locked decision 2, the *response* to signup
(or an equivalent point) is where the client should first receive and
start using the canonical form — confirm exactly which response carries
it and start storing that, not the pre-signup bare id, everywhere after.

### (k) Existing `userID@serverID` composite-string display convention — two independent parsers, worth consolidating

- **`spa/src/lib/utils/reedRef.ts`** — `parseReedRef`/`formatReedRef`,
  format `userID@serverID/reedID`. Splits on **first** `@`, then first
  `/` after it.
- **`spa/src/lib/utils/reedMarkdown.ts`** — mention grammar `~userID@serverID`,
  `readMention` splits on first `@` after an alphanumeric run, no fixed
  length assumed on either side (already variable-length-id-aware,
  explicitly commented as such).
- **`spa/src/lib/utils/identicon.ts`** — `identiconIdentity(userID, serverID)`
  already composes `${userID}@${serverID}` as identicon hash input —
  i.e. this exact convention already predates this decision as a display/
  hash concept, just never as the actual account identifier.

These two parsers are independently implemented and not quite identical,
both already robust to variable-length ids, both splitting on **first**
`@`. Worth consolidating into one shared parse/format pair rather than
introducing a third implementation for the new canonical-id use — and
reconciling with `identity.ParseIdentityID`'s Go-side last-`@` splitting
convention (**note the mismatch**: Go splits on the *last* `@`, these two
JS parsers split on the *first* `@` — need to pick one splitting
convention and make both sides agree, or guarantee via alphabet
constraints that it never matters, same reasoning as
`identity/identity_id.go`'s existing doc comment about why last-index
splitting was chosen there).

### (l) Account recovery / backup export — durable artifacts, no version field

Two independent durable formats embed bare userIDs with **no schema/
format version field** to detect a shape mismatch across this change:

- **`spa/src/lib/services/backupRestore.ts`** — `.sxi.gpg`/`.sxb.gpg`
  backup files. `extractProfile` matches `usersTable` rows against
  `localStorage['userId']` via **strict bare-string equality**
  (`item.id === userId`) — this is baked into the backup format itself.
  `restoreItem` writes backup table contents verbatim into IndexedDB, no
  shape verification. Per decision 5 (atomic, no compat/staging): old
  backups are simply incompatible after this change ships — that's
  acceptable and expected on this project, not a gap to paper over with
  format detection. No action needed beyond ensuring the *new* format is
  internally consistent; do not add version-detection/dual-format-read
  logic.
- **`spa/src/lib/services/accountRecovery.ts`** — server-bootstrap
  recovery flow, structurally identical risk (`fetchBootstrap` posts a
  bare `userID` field).
- **`spa/src/lib/services/recoveryKeyNest.ts`** — `buildKeyNest` embeds
  `userID: key.userID` (bare) at every level of the recursive key-nest
  structure POSTed to the recovery-claim endpoints.

Same treatment as above: this is pre-launch, blank slate — old recovery/
backup artifacts are not preserved across this change, full stop.

## Done — ReedRef special case (task 15), no code change needed

Re-investigated directly (not delegated — small enough to check in-session)
after the backend tasks (12-14) landed, since those touched adjacent code
(`CreateReedWithEcho`, `ReplyCountNotifyTargets`) that the original flag
worried might introduce a double-composition bug.

**Finding: `ReedRef` was never actually at risk.** `ReedRef` has always
had `AuthorID` and `ServerID` as two separate struct fields (not one
composite string) — `FormatReedRef` builds `AuthorID + "@" + ServerID +
"/" + ReedID` from them. Every construction site was checked:

- `ParseReedRef` — parses raw client-supplied text
  (`userID@serverID/reedID`, the SPA's mention/ref syntax) directly into
  the two bare parts by construction; can never produce a canonical value
  in `AuthorID`.
- `ReplyCountNotifyTargets` (services.go) — explicitly splits a canonical
  `identity.IdentityID` via `.UserID()`/`.ServerID()` before constructing
  the `ReedRef`, so `AuthorID` is always bare.
- `CreateReedWithEcho`'s `echoTarget` parameter, `ResolveThreadIDForParent`'s
  `parent` parameter — both are `ReedRef` values passed in from callers
  that already respect the same invariant (ultimately sourced from
  `handlers.go`'s `parseReedRef`, itself backed by `ParseReedRef`).

The one lookalike found during a repo-wide grep for `AuthorID:`/
`.AuthorID =` assignments (`services.go`'s `loadLikeCertTx`,
`AuthorID: authorIdentity.String()`) is `LikeCert.AuthorID` — a
completely different struct with the same field name, correctly holding
canonical form per locked decision 3 (it's a wire-facing cert field, not
a `ReedRef`). Not a collision with this task's concern.

**Conclusion: no code change was needed.** The struct's split-field
design already prevented the double-composition risk by construction;
the agents that touched adjacent code in tasks 12-14 correctly preserved
the "AuthorID is always bare" invariant rather than violating it. Same
outcome pattern as the first pass's `roles.go`/`handlers.go`/
`coverage/stats.go` tasks (verified clean, zero lines changed).

## Done — SPA display components (Avatar/Username/identicon, category k)

**Found a real, repo-wide bug**, independent of the reedRef.ts/
reedMarkdown.ts parser question the SCOPE doc originally worried about:
`Avatar.svelte` and `Username.svelte` both took a `userID` prop AND a
separate `serverID` prop, composing `${userID}@${serverID}` internally
(`Avatar` via `identiconIdentity`/`identiconForUser` in `identicon.ts`;
`Username` inline). This was correct while `userID` was always bare, but
once wire fields (`user.id`, `reed.userID`, `ripple.userID`) and URL
route params became canonical (`userID@serverID`) throughout tasks 12-14
and 16, every caller passing BOTH a canonical `userID` AND a separately-
sourced `serverID` (almost always `x.serverSignature?.serverID` — the
*countersigning* server, a different fact from the id's own suffix) was
silently producing double-composed garbage like
`"u1@srv1@srv2"` — Svelte doesn't error on an unused/extra component prop,
so `npm run check`/`npm run build` never caught this class of bug on
their own.

**Fix**: `Avatar`/`Username`/`identiconForUser` all now take ONE canonical
`userID` prop/param — the `serverID` prop is removed entirely, not just
ignored. `identiconIdentity` (the old two-arg composer) is deleted rather
than left dangling. `Username`'s internal `isLocal`/`isAdmin` checks now
split the single canonical value via `lastIndexOf('@')` (matching the Go
side's `identity.ParseIdentityID` last-`@` convention) — `isAdmin`
compares only the bare part, per locked decision 6 (frontend only needs
the bare part; the backend's stricter bare+serverID check doesn't apply
client-side since the client only ever reasons about its own account).

Every call site fixed (15 files): `Avatar.svelte`, `Username.svelte`,
`identicon.ts`, `ChorusSection.svelte`, `ConversationSection.svelte`,
`FollowListModal.svelte` (also had to drop `serverID` from a local `Row`
type, or `svelte-check` fails — the type-level version of the same bug,
which DOES get caught by the checker), `LikedReedsList.svelte`,
`Quote.svelte`, `ReedAuthorHeader.svelte`, `ReedsList.svelte`,
`UserProfileCard.svelte`, `spa/src/routes/feed/broadcast/+page.svelte`,
`spa/src/routes/feed/follow/+page.svelte`, `spa/src/routes/pipe/[tag]/+page.svelte`,
`spa/src/routes/reed/[userID]/[reedID]/+page.svelte` (two separate call
sites in this last file — one `Avatar` call already had no `serverID`
prop, one `Username` call still did and needed the same fix).

**`reedRef.ts`/`reedMarkdown.ts` — independently re-verified, confirmed
no fix needed.** Both parse formats where a bare userID/serverID can
never contain `@`: `reedMarkdown.ts`'s `ID_CHAR = /[a-zA-Z0-9]/` (confirmed
by direct inspection) strictly excludes `@`, matching the Go backend's
`crypto.Alphabet` (also alphanumeric-only). So there is exactly one `@`
in the relevant substring for both formats (`userID@serverID/reedID` for
`reedRef.ts`, `~userID@serverID` for `reedMarkdown.ts`'s mention grammar)
— "first `@`" and "last `@`" are the same position, no actual ambiguity,
no fix needed. `MentionLink.svelte`'s `userID`/`serverID` props (fed by
`reedMarkdown.ts`'s parser output) are correctly left as two separate
bare parts — this is the same "reference with separate parts" pattern as
`ReedRef` (task 15), not the "one canonical value" pattern `Avatar`/
`Username` needed to move to.

**Process note**: the agent originally delegated this task hit a session
API limit mid-work, after making substantial real progress in its
worktree's working tree (all 15 files above) but before committing or
reaching the reedRef.ts/reedMarkdown.ts verification. Per explicit user
instruction not to create new git branches/worktrees to route around
this, the coordinator (this session, no subagent) picked up the
interrupted agent's uncommitted working-tree changes directly in the
main checkout, reviewed each file's diff for correctness, ran
`npm run check` (caught one real remaining bug — the `FollowListModal.svelte`
`Row` type — and fixed it), found and fixed one more missed call site
(`reed/[userID]/[reedID]/+page.svelte`'s second `Username` usage still
passing `serverID`), completed the reedRef.ts/reedMarkdown.ts
verification the interrupted agent never reached, and committed the
result directly.

**Verification**: `npm run check` — 0 errors, 0 warnings. `npm run build`
— succeeds clean including service-worker compilation. Repo-wide grep
confirmed no remaining `serverID={` prop passed to `Avatar`/`Username`
anywhere, and no remaining two-arg `identiconIdentity`/`identiconForUser`
call sites.

## Next step

All open design questions are resolved. Backend categories (a)-(f) and
SPA categories (g)-(l) are both now scoped. This is a single atomic
implementation pass across backend + frontend together (locked decision
5 — no staged/dual-format rollout anywhere, no exceptions). Implement it
directly: there is nothing left to research or decide before starting to
write code. Suggested order (mechanical, not a design choice): backend
schema (`users.id` → canonical, drop the bare column per decision 1) and
`identity/identity.go` payload builders first, since everything else
depends on those; then the rest of the Go call sites (categories a-c,
f); then the SPA in lockstep for category (e)/(g)/(h) specifically
(signing must land together, not backend-then-frontend-later); then
IndexedDB/API-client/display-parser updates (i-k); backup/recovery (l)
needs no special handling beyond "new format only, don't preserve old."

## Done — SPA signing.ts + verifiers (category g, plus the upstream localStorage['userId'] audit)

Scope covered: `spa/src/lib/services/signing.ts` and every call site listed
in category (g) — `spa/src/lib/verifiers/index.ts`,
`spa/src/lib/services/reedLike.ts`/`reedRemoval.ts`/`invites.ts`/
`accountRemoval.ts`, `spa/src/lib/components/RippleComposer.svelte`,
`spa/src/routes/signup/+page.svelte`, `spa/src/routes/account/+page.svelte`,
`spa/src/routes/profile/[userId]/+page.svelte`, plus a repo-wide audit of
every `localStorage['userId']` get/set/remove site.

### The upstream localStorage['userId'] question — already correct, no fix needed

Grepped every `localStorage.setItem('userId', ...)` in `spa/src` (only two
sites exist, both in `spa/src/lib/services/auth.ts`):

- `AuthService.signup()` (line ~191): `const user = await response.json();
  localStorage.setItem('userId', user.id);` — stores whatever `id` field
  came back in the **signup HTTP response body** (`POST /api/users/signup`),
  not the pre-signup bare id.
- `AuthService.saveUserToStorage(user)` (line ~84): same pattern, stores
  `user.id` from whatever wire `User` object the caller passed in.

The pre-signup bare id (`apiService.getUserID()`'s `reserved.userID`,
`GET /users/id`) is used in `signup/+page.svelte` only to (a) name the
OpenPGP key being generated before the server has ever seen this identity,
and (b) as the `userID` **form field** POSTed to `/api/users/signup` — it is
never itself written to `localStorage`. This already matches locked decision
2 exactly: client generates a bare id, sends it, and only ever stores what
the server hands back afterward.

**Assumption requiring double-check once the backend agent's work lands:**
this hinges on the `Signup` handler's JSON response body having an `id`
field that is `db.go`'s `User.ID` (i.e. `services.go`'s `User` struct,
`json:"id"`) — which category (c) of this doc explicitly lists as a field
that changes from bare to canonical, and decision 1 drops `users.id` in
favor of `identity_id`/`identities.id` directly. `spa/src/lib/types/api.ts`'s
`User.id: string` stays a plain string either way (decision 3), so no SPA
type change was needed — only confirm once backend code is visible that the
`Signup` handler still serializes the canonical value under the JSON key
`"id"` (not a renamed field), since the SPA reads it positionally by that
key name.

### signing.ts — no change

`bytesToSign`/`stringToSign` treat header values opaquely; confirmed via
re-read, matches the doc's own prediction. None of the 16 `build*Payload`
functions needed a signature change — every one already accepts whatever
string is passed in as `userID`/`authorID`/`reedAuthorID`/`rippleAuthorID`,
with no bare-specific logic (no splitting, truncation, or `@`-stripping
anywhere in the file).

### Call sites audited — all already correct, zero required changes except one bug found and fixed

Every call site in `verifiers/index.ts`, `reedLike.ts`, `reedRemoval.ts`,
`invites.ts`, `accountRemoval.ts`, and `RippleComposer.svelte` forwards a
userID-shaped value sourced from one of: a wire object field read off an
`api.*` type (`key.userID`, `user.id`, `reed.userID`, `ripple.userID`,
`cert.userID`/`.authorID`, etc.), `authService.getCurrentUser()` (which
itself resolves through the now-correct `localStorage['userId']` /
IndexedDB `userRepository`), or `localStorage.getItem('userId')` directly
(`verifyReedLike`, `verifyInvite`). None of these compose, split, or
truncate an id — they all just pass through whatever they read. Since the
backend change makes every wire object field and the localStorage value
already canonical, **these files needed zero code changes** — confirmed by
re-reading each function against the SCOPE doc's category-(g) list.

`spa/src/routes/profile/[userId]/+page.svelte` and the reed-detail route
(`spa/src/routes/reed/[userID]/[reedID]/+page.svelte`, referenced from
`ReedsList.svelte`) source their `userID` from the URL route param
(`$page.params.userID`/`.userId`) — this becomes canonical automatically
once backend category (d) ships canonical URL routes; it's routing/URL
territory, explicitly out of this task's scope, so left untouched.

**One real bug found and fixed**, not predicted by the original scoping
pass: `spa/src/routes/account/+page.svelte`'s `revokeKey()` (key-rotation
flow) built the new key's OpenPGP identity name as
`` `${user.id}@${serverId}` ``. Here `user` is the already-loaded, verified
wire `User` object (from `apiService.getUserProfile`/`getUserInfo`), so
`user.id` will be the canonical `"userID@serverID"` string once the backend
ships — meaning this line would have produced a doubled identity string
(`userID@serverID@serverID`) for every key rotation. This differs from
`signup/+page.svelte`'s superficially similar `` `${reserved.userID}@${serverId}` ``,
which is correct because `reserved.userID` is genuinely the pre-signup bare
id. Fixed by using `user.id` verbatim as the key name and removing the now
unused `serverId` local. No other file in scope had this pattern.

### Verification run

- `cd spa && npm run check` (svelte-check via `svelte-kit sync`) — 0 errors,
  0 warnings.
- `npm run build` — succeeds clean, all routes prerender/build including
  `account`, `signup`, `profile/[userId]`, `reed/[userID]/[reedID]`.
- `npm run test:signing` — all 12 tests pass (payload byte-shape mechanics;
  opaque to bare-vs-canonical, so unaffected by this change).
- `npm run test:key-revocation` — all 7 tests pass.
- `.spec.ts` files under `spa/tests/` are Playwright e2e tests requiring a
  live backend; not run, per this task's stated verification scope.

### Not touched (explicitly out of scope, confirmed by grep, matches the doc's own boundary list)

`request-signer.ts`/`request-signer-shared.ts` (category h — separate
signed-byte surface), `db.ts`/IndexedDB repositories (category i),
`api.ts`'s URL-building (category j), `reedRef.ts`/`reedMarkdown.ts`/
`identicon.ts` (category k), any Go file.

## Done — services.go

Scope covered: every `DataService` method in `services.go` — the file's
`WHERE u.id = $1` / `WHERE id = $1` bare lookups against `users`, every
dangling `u.identity_id` reference (the first-pass `identities` refactor's
old join column, now removed by the foundation commit `e970ccd` — `users.id`
IS `identities.id` directly, no separate column), and every `.UserID()`
call site added by the prior pass to strip a canonical value back to bare
for a wire-facing struct field. Builds on the foundation described in
`PROGRESS_identity_indirection.md`'s "services.go — all ~109 call-sites"
section — that section's pattern (bare-in via exported signature,
`identity.LocalID`-convert internally, `.UserID()`-decode before
populating wire structs) is exactly what needed reversing here.

### Fixed: dangling `u.identity_id` references (compile-time-invisible SQL bugs)

Grepped `identity_id` across the file and found five live SQL statements
still referencing the dropped column (would have failed at runtime with
`column u.identity_id does not exist`, invisible to `go build`):

- `GetUserProfile`'s `invited_by` self-join —
  `LEFT JOIN users inv ON inv.identity_id = u.invited_by` →
  `LEFT JOIN users inv ON inv.id = u.invited_by`.
- `GetUserInfo`'s three `EXISTS`/`COUNT` subqueries (`has_reeds`,
  `followersCount`, `followingCount`) — `r.user_id = u.identity_id` /
  `uf.user_id = u.identity_id` / `ufl.user_id = u.identity_id` → all
  `= u.id`.
- `MentionTargetValid`'s `account_removals` `NOT EXISTS` check —
  `ar.user_id = u.identity_id` → `ar.user_id = u.id`.
- `SearchUsers`'s `account_removals` `NOT EXISTS` check — same fix.
- `ListFederationInvitations`'s dual creator/reviewer join —
  `JOIN users creator ON creator.identity_id = fi.created_by` /
  `LEFT JOIN users reviewer ON reviewer.identity_id = fi.reviewed_by` →
  both `.id = `.

### Fixed: bare `users.id`/`users.identity_id`-column INSERT and lookups (category a)

- **`Signup`** — the `INSERT INTO users (id, identity_id, ...)` statement
  inserted `in.UserID` (bare) as `id` and `selfIdentity` (canonical) as
  `identity_id`. Since `identity_id` no longer exists as a column, this is
  now `INSERT INTO users (id, ...)` with `selfIdentity` (canonical) as the
  sole PK value.
- **`UserServerSignedAt`, `GetActiveKeyFingerprint`, `GetUserRole`,
  `UpdateUser`, `DeleteUser`** — all had a bare-userID `WHERE u.id = $1` /
  `WHERE id = $1` against `users`. Fixed by converting the incoming
  `userID string` param to canonical via `identity.LocalID(userID,
  s.serverID)` before the query, same "bare-in, convert internally"
  pattern the trial functions (`GetUserProfile`, `BindDeviceTx`) already
  used in the first pass — kept for consistency, see judgment call below.
- **`AddPublicKey`**'s `UPDATE users SET user_fingerprint = $1 WHERE id =
  $2` used the bare `in.UserID` where `selfIdentity` (already computed
  earlier in the same function) was needed — fixed to reuse `selfIdentity`
  rather than reconverting.
- **`SearchUsers`**'s `UserSearchResult.ID`, fed directly from `SELECT
  u.id`, needed **no change** — verified explicitly per the task brief:
  since `u.id` itself is canonical now, this field is canonical
  automatically. The only fix `SearchUsers` needed was the dangling
  `identity_id` join above.
- **`GetPublicKey`/`GetKeyRevocation`/`AddPublicKey`/`GetReed`/
  `GetReedAttestation`/`ListFederationInvitations`/`GetFederationInvitation`/
  `insertReedCoreTx`** — none of these had a bare-`users.id` issue (they
  filter on `user_keys.owner`/`reeds.user_id`/`federation_invitation.
  created_by`, which were already canonical from the first pass), but see
  the `.UserID()` section below — all of these had a bare-decode step that
  needed removing.

### Fixed: `.UserID()` call sites removed (category b) — wire fields now hold canonical form directly

Per locked decision 3 (wire fields stay plain `string`, holding the
canonical value), every `.UserID()` call that decoded a scanned
`identity.IdentityID` back to bare before populating a wire-facing struct
field was removed, and the scan target simplified from `identity.IdentityID`
to plain `string` (no decode step left to need the richer type):

- `GetPublicKey`/`AddPublicKey` → `Key.UserID`.
- `GetKeyRevocation` → `KeyRevocation.UserID`.
- `ListReplies` → `ReplyListItem.UserID`.
- `listFollowEdge` (shared by `ListFollowing`/`ListFollowers`) →
  `FollowListItem.UserID`.
- `insertReedCoreTx`, `GetReed` → `Reed.UserID`.
- `GetReedAttestation` → `ReedAttestation.UserID`.
- `GetReedChorus` → `EchoerListItem.UserID`.
- `loadLikeCertTx` → `LikeCert.AuthorID`.
- `ListUserFollowing` → the `[]string` feeding
  `bootstrapAccountRecoveryResponse.Following`.
- `GetFederationInvitation` → `federationInvitation.CreatedBy`.
- `ListFederationInvitations` → `federationInvitationListRow.CreatedBy`/
  `.ReviewedBy` — this one also had its `ParseIdentityID`-based nullable-
  safe decode removed entirely (no longer needed: an empty string is now a
  legitimate "no reviewer yet" wire value, not a malformed id to guard a
  panic against).
- `scanRipple` (shared by `GetRipple`/`ListRipples`) →
  `Ripple.ReedAuthorID`/`.UserID`.
- **`PostRipple`** — this one did NOT go through `.UserID()` (the first
  pass deliberately built its returned `Ripple` from the original bare
  params, not a decoded scan), but per decision 3 it's still a wire
  struct, so it was fixed the same way conceptually: the returned
  `Ripple{ReedAuthorID: ..., UserID: ...}` now uses
  `reedAuthorIdentity.String()`/`selfIdentity.String()` (canonical) instead
  of the bare `reedAuthorID`/`userID` params, matching what `scanRipple`
  now returns on the read path (`GetRipple`/`ListRipples`). This required
  care: `identity.BuildRippleServerPayload` (a signed-wire-payload
  builder, category (e)) in the SAME function still receives the bare
  params unchanged — that reconciliation is explicitly out of this pass's
  scope (see "Left alone" below). Two separate values had to stay live at
  once, same hazard the first pass's PROGRESS doc already flagged for this
  exact function.

### Judgment call: `ReplyCountNotifyTargets`/`...ForRemovedReply`/`...ForAuthor` — `.UserID()` calls LEFT IN PLACE, deliberately

These three functions build `ReedRef` structs via
`ReedRef{AuthorID: selfIdentity.UserID(), ServerID: ..., ReedID: ...}`.
I initially converted this to `selfIdentity.String()` (full canonical
form in `AuthorID`), then reverted: every OTHER `ReedRef` construction in
the file (`ParseReedRef`, which splits `"userID@serverID/reedID"`) puts
the **bare** userID in `AuthorID` with `ServerID` as a separate field.
Making only this one function's output hold the full canonical string in
`AuthorID` would create a third, inconsistent `ReedRef` shape — exactly
what the SCOPE doc's `ReedRef` flag (see below) warns against. Confirmed
via grep that neither `services.go` nor `handlers.go` ever round-trips
these specific `ReedRef`s through `FormatReedRef`, so there's no immediate
double-`@serverID` collision risk from leaving it bare either — this is a
consistency judgment call, not a correctness-forced one. Left the
`.UserID()`/`.ServerID()` split in place with a code comment explaining
why, and flagging that reconciling `ReedRef`'s shape with the canonical-id
world belongs to task 15 (see below), not this pass.

### Fixed: `MentionTargetValid` — also had a bare-lookup bug, not just the dangling join

Beyond the `identity_id` → `id` join fix, `MentionTargetValid(ctx, userID,
serverID string)` was comparing the bare `userID` parameter directly
against `u.id` (now canonical) — always-false, same "silent bug class"
category the first pass's PROGRESS doc flagged for `GetUserInfo`/
`SearchUsers`. Fixed by converting `userID`/`serverID` (the mention
target) to canonical via `identity.RemoteID` before the query, consistent
with `insertReedCoreTx`'s existing mention-target handling elsewhere in
the file. The function's existing judgment-call comment (querying
`users`/`account_removals` directly rather than `verified_identities`,
correct-for-now because local mentions are unconditionally verified) was
kept as-is — still accurate, unrelated to this fix.

### `RootUserID` comparisons (locked decision 6) — none found in services.go

Grepped `RootUserID`/`IsRoot`/`SignupRole`/`RoleForSignup`/
`ValidateProfileRole` across the file. The only hit is `Signup`'s
`roles.SignupRole(in.UserID, inviteGrantedRole, in.Invite != nil)` call,
which passes a bare userID into the `roles` package's own internal logic
— that function's comparison against `roles.RootUserID` happens inside
`roles.go` itself, not in `services.go`, and `roles.go`/`main.go`/
`root.go`/`handlers.go` are explicitly out of this task's scope (flagged
as a separate, not-yet-done task in the coordinator's brief). No
`RootUserID` comparison exists anywhere else in `services.go` — confirmed
by grep, not assumed. `GetUserRole` (which callers use to check role
before comparing against root) was audited and needed only the bare→
canonical `WHERE id = $1` fix described above — it returns a role string,
it does not itself perform any `RootUserID` comparison.

### Judgment calls: function-signature decisions

Every exported `DataService` method touched kept a **bare-userID-in,
convert-internally** signature (via `identity.LocalID(userID, s.serverID)`
right at the top of the function body), rather than changing signatures to
accept `identity.IdentityID`/an already-canonical string. Reasoning,
stated once here since it applies uniformly:

- The task brief explicitly allows this ("assume `services.go`'s exported
  function signatures may still receive a bare userID as an argument from
  callers that haven't been updated yet, OR may start receiving canonical
  directly; use judgment per-function").
- Every one of these functions has callers in `handlers.go` (route
  handlers reading a URL path parameter) and/or `root.go`
  (`roles.RootUserID`, a bare literal) — both explicitly out of scope for
  this pass (task 13 owns the URL-parameter side). Changing the signature
  to require canonical input would not compile against those unconverted
  callers.
- Where a function already had the canonical form in hand from earlier in
  its own body (e.g. `Signup`'s `selfIdentity`, `AddPublicKey`'s
  `selfIdentity`), the existing local value was reused directly rather
  than reconverting — no redundant `identity.LocalID` calls were added
  where a canonical value already existed in scope.
- `loadLikeCertTx` (internal, unexported helper) already took
  `identity.IdentityID` params directly, from the first pass — left
  unchanged, since all its callers already hold canonical values and it's
  not called from outside this file.

This means `handlers.go`/`root.go` continue to compile and behave
identically to before this pass (they still pass bare userIDs in, and get
correct behavior because the conversion now happens one layer down inside
`services.go` instead of assuming `users.id` was already bare) — task 13
can later either keep this pattern or thread canonical values further up
the call stack; both are viable given how this pass left the boundary.

### `ReedRef`/`FormatReedRef`/`ParseReedRef` — confirmed the flagged interaction, did not touch it

Per the coordinator's explicit instruction, `FormatReedRef`/`ParseReedRef`
themselves were not modified. Findings from the sweep, for whoever picks
up task 15:

- `ReedRef.AuthorID` is populated bare (via `.UserID()` or an already-bare
  local) at every construction site in `services.go` except one place I
  initially miscoded and then reverted (see the `ReplyCountNotifyTargets`
  judgment call above) — `ParseReedRef` (splits the incoming composite
  string), `DeleteEchoIndexForReed`/`DeleteEchoesByAuthor` (scan directly
  into `ReedRef.AuthorID`, matching `echoed_user_id`, which has no FK at
  all and is stored bare by design), `insertReedCoreTx`'s mention-handling
  loop (reads `m.AuthorID` off an already-bare `ReedRef` built upstream).
- `FormatReedRef(ref ReedRef) string` does `ref.AuthorID + "@" +
  ref.ServerID + "/" + ref.ReedID` — if `AuthorID` ever held the full
  canonical `"userID@serverID"` form instead of bare, this would produce
  `"userID@serverID@serverID/reedID"` (double `@serverID`). Confirmed this
  by tracing the one call site (`ResolveThreadIDForParent`, which calls
  `FormatReedRef(parent)` as its `sql.ErrNoRows` fallback) — `parent`
  there is a caller-supplied `ReedRef` whose `AuthorID` shape depends on
  what the (out-of-scope) caller in `handlers.go` passes in today.
- Net: `ReedRef`'s three-field shape (`AuthorID` bare + separate
  `ServerID`) is internally consistent throughout `services.go` as I left
  it, but is itself a third representation of "a user" alongside
  `identity.IdentityID` and plain wire-canonical strings — reconciling
  that (e.g. collapsing `AuthorID`+`ServerID` into one canonical field, or
  formally documenting `ReedRef` as a deliberately-different on-the-wire
  composite) is task 15's job, not attempted here.

### Left alone (explicitly out of scope, confirmed rather than assumed)

- `identity.BuildRippleServerPayload` and every other `identity/
  identity.go` `Build*Payload` call site in `services.go`
  (`Signup`'s implicit signature verification is the caller's job, not
  persisted here) — all still receive bare params, unchanged. Category
  (e) (payload builders) requires the SPA to change in lockstep per the
  SCOPE doc's own locked decision 5; not attempted in this backend-only,
  `services.go`-only pass.
- `ReedAsMarkdown` (the signed-reed-markdown-envelope builder) — no
  structural change needed, same reasoning as `identity/identity.go`'s
  builders; its one caller is in `handlers.go`, out of scope.
- `SetDefaultIdentity` — still dead code, targets a `profiles` table that
  does not exist in `db.go`. Left untouched, matching the first pass's
  documented judgment.
- `GetReedRemoval`/`InsertReedRemoval`/`GetAccountRemoval`/
  `InsertAccountRemoval`/`HasAccountRemoval` — thin wrappers delegating to
  `deletion.*`, which is out of this pass's scope; left as-is.
- `roles.go`/`main.go`/`root.go`/`handlers.go`/`recovery/`/`realtime/`/
  `deletion/`/`invites/`/the SPA/all `_test.go` files — untouched, per
  the task's stated scope boundary.

### Verification performed

- `go build ./...` — passes clean.
- Scratch Postgres database (`syrinx_canonical_scratch`, dropped after)
  created against the local `syrinx_db` container (started via `docker
  start syrinx_db` — it was present but stopped at the start of this
  task). A temporary `zzz_canonical_verify_test.go` (package `main`,
  deleted before finishing — confirmed via `git status --short` showing
  only `services.go` modified) called `DataService` methods directly (no
  HTTP layer): `InitServer`, `Signup` (twice, two distinct users),
  `GetUserProfile`, `GetUserInfo` (asserting `FollowersCount` after a real
  `FollowUser` — the exact `u.id`-vs-`identity_id` join bug class),
  `ListFollowing`/`ListUserFollowing`, `SearchUsers`, `RevokeKey` +
  `AddPublicKey` rotation + `GetPublicKey` + `GetKeyRevocation`,
  `CreateReed` + `GetReed` + `GetReedAttestation`, `InsertReedLike` +
  `GetReedLike` + `CountLikes`, `CreateReedWithEcho` + `CountEchoes` +
  `GetReedChorus`, `CreateReedWithReply` + `ResolveThreadIDForParent` +
  `ListReplies` + `GetSubtreeReplyCount`, `MentionTargetValid`,
  `PostRipple` + `GetRipple` + `ListRipples` + `SoftDeleteRipple`,
  `InsertFederationInvitation` + `GetFederationInvitation` +
  `ListFederationInvitations` (asserting `CreatedByUsername` resolves via
  the join — the same bug class, on the federation-invitation join) +
  `RevokeFederationInvitation`. Every wire-facing field touched was
  asserted to be non-empty, contain `@`, and end with `@{serverID}` (i.e.
  genuinely canonical, not just "has an `@` somewhere") — not just "the
  call didn't error." All assertions passed on the first fully-corrected
  run. Scratch database dropped and temp test file deleted afterward.
- `go vet ./...`/`go test ./...` were not used as the pass/fail signal,
  per the task's explicit instruction — the pre-existing, out-of-scope
  `recovery_collision_test.go`/`deletion/store_test.go` are still known-
  broken against the `identities` schema and were not touched.

## Done — recovery/realtime/deletion/invites/roles

Scope covered: `recovery/upsert.go` (dangling `identity_id` bugs only —
`recovery/wire.go` deliberately NOT converted, see exception below),
`realtime/*`, `deletion/*`, `invites/*`, `roles/roles.go`. Commits, in
order: `db654d6` (recovery), `19e04fe` (realtime), `e82a254` (deletion),
`d841822` (invites), `8b08162` (roles).

### `recovery/wire.go` — deliberate, documented BARE-FIELD EXCEPTION

Unlike every other package in this pass, `recovery/wire.go`'s JSON fields —
`Profile.ID`, `InvitedBy.ID`, `KeyWire.UserID`, `Revocation.UserID`,
`ReedRequest.AuthorID`, `FollowingRequest.UserIDs` — were **NOT** converted
to canonical form and must stay bare. Reason: these fields are populated
directly from data the SPA sends in recovery-claim/peer-report requests,
built by `spa/src/lib/services/recoveryKeyNest.ts`, which still constructs
its key nests (and the signed payloads built over them) with bare userIDs —
category (e)/(g) of this doc, a separate, not-yet-done task (see category
(l)'s note on `recoveryKeyNest.ts`). Converting `recovery/wire.go`'s Go
struct field *values* now, ahead of the SPA, would make every recovery
signature/claim verification check the wrong bytes against what the SPA is
still actually sending — breaking recovery outright. A prominent file-level
comment was added to `recovery/wire.go` recording this exact reasoning, so
a future reader doesn't mistake it for an oversight. This exception must be
revisited (and these fields converted) only when `recoveryKeyNest.ts` (and
category e/g's other recovery-specific payload builders) convert in the
same atomic pass, server and client together, per this doc's locked
decision 5 (no staged/dual-format rollout).

### `recovery/upsert.go` — 4 dangling `identity_id` bugs fixed

`db.go`'s foundation commit (`e970ccd`) dropped `users`' separate
`identity_id` column — `users.id` IS `identities.id` directly now, via
`users.id VARCHAR(255) PRIMARY KEY REFERENCES identities(id)`. Four live
SQL/Go references to the dropped column name were found and fixed (compile-
time-invisible SQL bugs the Go compiler can't catch):
- `upsertIdentity`'s existence-lock `JOIN users u ON u.identity_id = i.id`
  → `ON u.id = i.id`.
- `insertUser`'s `INSERT INTO users (id, identity_id, ...)` → dropped
  `identity_id` from the column list entirely; `selfIdentity` (canonical)
  is now the sole `id` value, matching `services.go`'s `Signup` pattern.
- `claimUsername`'s `SELECT u.identity_id, ss.signed_at` → `SELECT u.id,
  ...`. Also fixed its self-exclusion comparison, which was still comparing
  a bare `userID`/`profile.ID` param against the now-canonical `users.id`
  (an always-"different" comparison that could wrongly treat a same-
  identity re-report as colliding with itself) — now takes the caller's
  already-computed `selfIdentity` (canonical) instead.
- `updateUserIfNewer`'s `WHERE id = $7` bound bare `profile.ID` against
  canonical `users.id` — always-false, silently updating zero rows. Fixed
  by threading `selfIdentity` through as a new parameter.

Verified against a scratch Postgres DB: fresh own-claim, a newer re-report
(proving the `WHERE id=$7` fix), a peer-report landing in
`unclaimed_accounts` with canonical id, a self-report correctly NOT
colliding with itself, and a genuine username collision correctly cascade-
deleting the loser's `identities` row.

### `realtime/*` — full canonical-in/canonical-out flip, plus a discovered pre-existing bug class

`realtime/db.go`'s `DBService`/`realtime/auth.go`'s `AuthService` were
"bare-in, convert internally" (`identity.LocalID(userID, ds.serverID)` at
the top of every function) — the exact first-pass exception this task
reverses. This convention was already silently broken the moment
`services.go`'s conversion (`afa91ab`) landed: `AuthenticateWebSocket` has
always passed its query-string `userID` through unchanged, and the SPA
(`serverConnection.ts`) has always sent `user.id` (the canonical wire
`User.id`) as that param — so every WS-connection-scoped userID flowing
through this package (`client.userID`, `PendingEvent`/`PendingReedEvent`/
`ProfileSubscriber`/etc. fields) has been canonical since `afa91ab`, while
`db.go` kept re-composing `identity.LocalID(alreadyCanonical, serverID)`
internally, silently double-appending `"@serverID@serverID"` and breaking
every FK lookup that touched those values. This was a real, live bug
already present on `canonical` before this task started — not introduced
by this pass.

Fixed by flipping `db.go`'s convention to "canonical-in, use directly" (44
`identity.LocalID` composition call sites → `identity.IdentityID` direct
cast) and removing the 18 `.UserID()` decode calls that stripped scanned
canonical values back to bare before populating wire structs
(`realtime/wire.go`'s fields are plain `string` per locked decision 3, now
holding canonical values throughout). Also fixed a dangling `identity_id`
column reference in `GetUsername` (same class as `recovery/upsert.go`'s
bugs).

`auth.go`'s `AuthenticateWebSocket`/`getPublicKey` had the same double-
composition bug in their two `identity.LocalID` calls — fixed to use the
already-canonical query-string `userID` directly (`identity.IdentityID(userID)`,
no composition).

**Ripple into `main.go` (small, surgical, as anticipated by this task's own
setup note about the `roles.go` ripple, just discovered a package early):**
`main.go`'s `SetDeviceCheck`/`SetOngoingCheck` closures call OUT to
`services.go`'s still-bare-in `DataService.CheckActiveDevice`/`IsOngoing` —
added a boundary decode (`identity.IdentityID(userID).UserID()`) so a
WS-sourced canonical `userID` doesn't get double-composed when it reaches
`services.go`. Similarly, `RealtimeService.DisconnectUser` (`service.go`)
is called by `handlers.go` (out of scope, unconverted) with a bare `userID`
but must match `connManager`'s canonical-keyed connection registry — added
the same bare-in-convert-internally boundary decode there, matching the
rest of this package's established pattern for external bare-userID
callers.

`deletion.GetCert`/`GetAccountCert` calls in `db.go` (`GetMissingRemovals`,
`GetReedRemovalWire`, `GetMissingAccountRemovals`, `GetAccountRemovalWire`)
still decode to bare right at that call boundary, since `deletion/*` (at
the time `realtime/*` was converted) was still bare-in — but the wire-
facing `MissingRemoval.UserID`/`MissingAccountRemoval.UserID` fields were
written to source from the returned `cert.UserID` rather than the bare
lookup value, so they automatically became canonical once `deletion/*`
converted (next in this task's sequence) — confirmed via a cross-package
scratch-DB test after both commits landed.

Verified against a scratch Postgres DB: `MarkUserOnline`/`GetOnlineFollowers`,
`CreatePendingReedEvent`/`GetPendingReedEvent`, `AllocateReed`/
`GetOnlineHolders`, `CreateProfileSubscription`/`GetProfileSubscribers` all
round-trip canonical values correctly; confirmed the account-removal query
no longer double-composes serverID. A second, separate cross-package test
(after the `deletion/*` commit) proved `GetMissingRemovals`/
`GetMissingAccountRemovals` correctly surface canonical `UserID` end-to-end
through `deletion.Cert`/`AccountCert`.

### `deletion/*` — read-path certs converted, write-path input stays bare (documented asymmetry)

`AccountCert.UserID`/`Cert.UserID` were `.UserID()`-decoded to bare before
being returned from `GetAccountCert`/`GetCert` (via `loadAccountCertTx`/
`loadReedCertTx`/`assembleReedCert`) — reversed, since these are genuine
wire-facing fields (HTTP responses via `services.go`'s thin wrappers,
`realtime/wire.go`'s `AccountRemovalWire.UserID`/`ReedRemovalWire.UserID`
via `NewAccountRemovalWire`/`NewReedRemovalWire`) reflecting server-side
removal state, not SPA-recovery-payload fields — unlike `recovery/wire.go`,
these convert.

**Read/write asymmetry, documented explicitly in both structs' doc
comments:** `InsertAccountCert`/`InsertCert`'s INPUT `cert.UserID` stays
bare — it's still constructed in `handlers.go` (out of this task's scope,
unconverted) from a session-authenticated caller's own bare userID. Only
the RETURNED cert (read path) is canonical. Do not assume `UserID` is
uniformly bare or uniformly canonical across either struct's whole
lifecycle.

Also fixed a real bug in `InsertAccountCert`'s profile-clear step
(`UPDATE users SET username = NULL... WHERE id = $1`): it was binding the
bare `cert.UserID` against `users.id`, which is canonical now — an always-
false comparison that silently cleared zero rows instead of nulling out
the removed account's username/signature ids. Fixed to bind the already-
computed `selfIdentity` instead.

Verified against a scratch Postgres DB: `InsertAccountCert`/
`GetAccountRemoval` round-trip canonical `UserID`, `HasAccountRemoval`
matches the canonical row, the profile-clear fix actually nulls
`users.username` (proven via direct query, not just "insert succeeded"),
and `InsertReedRemoval`/`GetReedRemoval` round-trip canonical `UserID` too.

### `invites/*` — `CreatedBy`/`ClaimedBy` converted, plus a discovered pre-existing double-composition bug

`invites/store.go`'s `scanInvite` decoded `created_by`/`claimed_by` back to
bare via `.UserID()` — reversed. `ClaimedBy` is confirmed exposed via
`statusResponse.ClaimedBy` (`GET /api/invites/{id}`); `CreatedBy` is not
currently serialized on any response body (confirmed by re-reading
`createResponse`/`statusResponse`/`checkResponse`) but is kept canonical
for consistency, since it's populated by the same `scanInvite` path — this
doc's earlier "unconfirmed" flag on `CreatedBy`'s wire exposure is now
resolved: confirmed NOT currently exposed, converted anyway.

**Discovered, pre-existing bug (same double-composition class as
`realtime/auth.go`'s `AuthenticateWebSocket`):** `invites/handlers.go`'s
`Check` handler (`GET /api/invites/check?uid=`) and both signup-flow
`handlers.go` call sites read an `inviteCreatorID`/`uid` query param that's
**already canonical** — the SPA's `inviteShareURL` (`invites.ts`) builds it
from `user.id`, the canonical wire `User.id` — but passed it straight into
`invites.Store.GetPendingInvite`, which composes
`identity.LocalID(creatorID, serverID)` internally expecting bare, silently
double-appending serverID and never matching. This bug has existed since
`afa91ab` landed, same as the `realtime/*` one — not introduced by this
pass. Fixed by decoding to bare only at that specific call boundary
(`identity.ParseIdentityID`'s safe form for the public `Check` endpoint,
since `uid` is untrusted input and `.UserID()` panics on malformed input;
`identity.ParseIdentityID` inline for the two signup-flow call sites),
while leaving the canonical value intact everywhere it's compared directly
against `Invite.CreatedBy` (now also canonical) — e.g. `ResolveSignup`'s
`inv.CreatedBy != by` check, which was actually comparing bare-vs-canonical
before this fix and always failing (a second, distinct pre-existing bug
this same fix incidentally repairs).

**Ripple into `services.go`'s `Signup` (out of scope, otherwise
unchanged):** `in.Invite.CreatedBy` is now canonical but
`identity.LocalID(...)` (for `invited_by`) and `invites.Store.MarkClaimed`
both still expect bare — decoded once (`inviteCreatorBare`) and reused for
both call sites.

**Noted, not changed:** `identity.BuildNewProfilePayload`'s `invitedBy`
param (`handlers.go`'s `CreateAccount`, category (e) — signed payload
builders, separate not-yet-done task) now receives the canonical
`resolved.InviterID` unmodified. This isn't a new break — `GetUserProfile`'s
`InvitedBy.ID` has been canonical since `services.go`'s own conversion
(`afa91ab`, before this task), and the SPA's `verifyUser` already verifies
against that canonical value (confirmed by reading `verifiers/index.ts`:
`buildProfilePayload(..., user.invitedBy?.id ?? '', ...)`), so signup-time
signing and later verification were already mismatched before this commit
(bare signed vs. canonical verified) and are now aligned (both canonical) —
a positive side effect, not a regression, but flagged here since it wasn't
this task's explicit goal.

Verified against a scratch Postgres DB: `Insert`/`GetByCreatorAndID`/
`GetByTokenHash` round-trip canonical `CreatedBy`, and `MarkClaimed`
followed by a re-read correctly surfaces a canonical `ClaimedBy`.

### `roles/roles.go` — `serverID` parameter added to all 4 root-comparison functions (locked decision 6)

`roles.RootUserID` stays the bare literal `"1"` (serverID is runtime-only,
can't be a compile-time constant), but `IsRoot(userID, role, serverID)`,
`RoleForSignup(userID, serverID)`, `SignupRole(userID, inviteGrantedRole,
hasInvite, serverID)`, and `ValidateProfileRole(userID, role, serverID)`
all gained a `serverID` parameter and now compare against the FULL
canonical identity (`identity.LocalID(RootUserID, serverID)`) via a shared
internal `isRootIdentity(userID, serverID)` helper — never just the bare
`"1"` literal. This is the security fix locked decision 6 requires: a
bare-only comparison would let a remote, federated identity whose bare
userID happens to also be `"1"` (e.g. `"1@someOtherServerID"`) be treated
as this server's own root. `isRootIdentity` accepts either a bare local
userID or an already-canonical `IdentityID` string as input, handling both
shapes without the caller needing to know which one it holds.

**Every call site, grepped repo-wide (not just this package), before and
after the change:**
- `IsRoot`/`RoleForSignup` — **zero production callers**, confirmed by
  grep both before and after this change (only `roles/roles_test.go`
  references them). Signatures still updated for consistency and because
  any future caller must get the secure version, not the vulnerable one.
- `SignupRole` — 2 live callers, both updated: `services.go`'s `Signup`
  (`s.serverID` already in scope) and `handlers.go`'s `CreateAccount`
  (threaded via `h.services.db.GetServerID()`).
- `ValidateProfileRole` — 2 live callers, both already in this task's
  `recovery/*` scope: `recovery/nest.go`'s `FlattenKeysNest` (`serverID`
  already a parameter) and `recovery/upsert.go`'s `insertUser` (`serverID`
  already in scope).

**`root.go`/`main.go`'s other `roles.RootUserID` usages — audited, none
needed changes:** every one either passes `roles.RootUserID` as a bare
userID into `services.go`'s already-converted bare-in-convert-internally
functions (`GetUserProfile`/`Signup`/`GetPublicKey` — correct, unchanged,
since `root.go`'s bootstrap flow is exclusively about THIS server's own
root, never a remote one), builds the bootstrap OpenPGP name string
(`roles.RootUserID + "@" + serverID`, cosmetic, explicitly flagged by this
doc's category (f) as optional/separate reconciliation, not required by
this task), or is a log/filename string literal — none are a
`IsRoot`/`SignupRole`/`ValidateProfileRole`/`RoleForSignup` call or a bare
security comparison. `handlers.go`'s `CreateAccount` has one additional
bare `userID == roles.RootUserID` check (rejecting a freshly-client-
generated candidate signup id that collides with the reserved root id,
*before* any identity exists at all) — left as-is: it's a different,
non-vulnerable comparison class, since no *existing* remote identity is
ever being checked there, only a brand-new local candidate id against a
literal.

**KNOWN, ACCEPTED CONSEQUENCE (not silently introduced):**
`roles/roles_test.go` (a `_test.go` file, out of this task's scope to
modify) calls `IsRoot`/`RoleForSignup`/`SignupRole`/`ValidateProfileRole`
with their old 2–3-arg signatures and no longer compiles — `go vet ./...`
and `go test ./...` now fail on this package specifically. `go build ./...`
(this task's explicit required gate) passes clean across the whole repo.
This mirrors the exact precedent already documented earlier in this same
progress trail for `recovery_collision_test.go` breaking under the
identities-indirection pass — accepted the same way, not a new kind of
gap.

**Security-property verification (the critical test this task
required):** ran a direct test proving `IsRoot`/`RoleForSignup`/
`SignupRole`/`ValidateProfileRole` all correctly reject
`"1@someOtherServerID"` (a remote server's own root, bare id `"1"`) as NOT
this server's root, while correctly accepting both the bare local `"1"`
and canonical `"1@thisServerID"` forms as root. Also ran an end-to-end
`Signup` against a scratch Postgres DB proving root-minting on the very
first local signup with userID `"1"` is unchanged (role `root`, canonical
id `"1@{serverID}"`), and that a normal (non-`"1"`) signup still correctly
gets role `user`.

### Verification summary across this whole task

- `go build ./...` passes clean across the whole repo after every commit,
  including the final `roles.go` change.
- `go vet ./...` fails only on `roles/roles_test.go` (documented, accepted
  consequence above) — everything else vets clean.
- Five scratch Postgres databases used across this task (recovery,
  realtime, deletion, invites, roles/cross-check), each created via the
  existing local `syrinx_db` Docker container, each dropped immediately
  after its verification run; every temporary `zzz_*_test.go` file used to
  drive them was deleted before moving to the next package — confirmed via
  `git status --short` showing only the intended source files at each
  commit.

## Done — SPA IndexedDB + api.ts (category i, j)

Scope covered: `spa/src/lib/services/db.ts`, `spa/src/lib/services/api.ts`,
and the IndexedDB repositories that read/write user-referencing stores:
`spa/src/lib/repositories/reeds.ts`, `user.ts`, `following.ts`,
`removedAccounts.ts` (note: these live under `repositories/`, not
`services/` as this doc's task list assumed — file layout, not a scope
change).

**Bottom line: zero code changes needed in any of these six files.** Every
one was already a shape-agnostic pass-through, exactly as this doc's
categories (i)/(j) predicted. This section documents what was checked
file-by-file so that's a verified conclusion, not an assumption.

### db.ts — audited, no change needed (including the `version` constant)

Read the whole file. `IndexedDbService` is fully generic: `put`/`get`/
`delete`/`getAll`/`getAllByIndex`/`getAllSortedByIndex`/`getLatestFromIndex`
take a `storeName` and an opaque `DbKey` (`string | string[]`) and never
inspect the key's content — IndexedDB itself only requires keys to be
comparable/unique, which a `"userID@serverID"` string satisfies exactly as
well as a bare one. The `storeNames` table and the `reeds` compound-key
`ensureStore` call define `keyPath` **structurally** (which field(s) name
the key), never by expected string shape or length. Confirmed via a
throwaway round-trip test (see Verification below) that canonical
`aB3xQ9zK@srv456def`-shaped ids pass through every affected store (`users`,
`usersInfo`, `following`, `reeds` compound key + `userID` index,
`removedAccounts`, `tags` embedded ref) with no truncation, splitting, or
mangling, and that two distinct canonical ids remain distinct records.

**`version` (currently 9) — left unchanged, decision documented:** the
file's own convention, per its v8/v9 comments, is that a bump marks a
*structural* schema change: v8 changed `reeds`'s `keyPath` from `'id'` to
`['userID', 'id']`; v9 added the new `ripples` store. This change alters
neither — no store's `keyPath` changes shape, no store is added or removed,
and `onupgradeneeded`'s `ensureStore` helper is additive/idempotent
regardless of version number, so nothing depends on a bump to take effect.
The file's own prominent comment block (lines 40-49) documents a real prior
incident where bumping `version` on every unrelated change caused
unconditional store-wiping and permanent data loss for `privateKeys`/
`unsignedReeds` (which have no server-side copy to resync from) — bumping
here with no structural change to justify it would repeat exactly the
pattern that comment warns against, for no benefit. Decision: **no version
bump.**

### reeds.ts — audited, no change needed

Every function was read in full: `getReed`, `storeReed`,
`deleteReedsByAuthor`, `getReedsByAuthor`, `getUnsignedReedsByAuthor`,
`getReedsByTag`, `announcePublishedReeds`, plus the module-level
`initFollowIds`/`prependFollowId`/`getFollowReeds`/`removeBroadcastReed`.
Every `userId`/`userID`/`authorId` value is used only for: (1) IndexedDB
key construction (`[userId, reedId]`, `reed.userID`), (2) opaque equality
comparison (`r.userID === reed.userID`, `authors[reed.userID]`,
`followedSet.has(reed.userID)`), or (3) storing/reading a wire `reed`
object's `userID` field verbatim (`storeReed`'s `dbService.put('reeds',
reed, ...)`). No length checks, no substring/slice/split operations, no
manual composition of an id from parts anywhere in the file (confirmed via
grep for `.length`/`substring`/`slice(`/`split(`/`indexOf(` — the handful
of hits are unrelated: reed-content-length limits and array `.slice`
pagination, not id-shape logic). `storeReed`'s `tags` handling stores
`{ userID: reed.userID, id: reed.id }` refs verbatim from the wire object.
Will start receiving/storing canonical values automatically once the
backend ships, no code change required.

### user.ts — audited, no change needed

`UserRepository.get`/`put`/`delete`/`isTombstone`/`writeTombstone`/
`getByUserId` all treat `userId` as an opaque string: direct pass-through
to `dbService.get/put/delete`, or (`writeTombstone`) constructing
`{ id: userId, __meta__: {...} }` verbatim. `getByUserId`'s cache-miss path
calls `apiService.getUserProfile(userId)` and stores the returned wire
`api.User` object via `this.put(user)` — verbatim, no reshaping. No change
needed.

### following.ts — audited, no change needed

`isFollowing`/`recordLocalFollow`/`follow`/`unfollow`/`getPendingFollows`/
`getPendingUnfollows`/`syncPending` (covers the `following`, `unfollow`,
and `pendingFollows` stores — confirmed no separate file exists for these,
they're all in this one module) all treat `userId` as an opaque string used
only as a `dbService` key or embedded verbatim in a `{ userId, timestamp }`
record. No change needed.

### removedAccounts.ts — audited, no change needed

`put`/`get`/`has` — `put` stores the wire `api.AccountRemoval` cert object
verbatim (its `userID` field flows through unmodified into the `userID`-
keyed store); `get`/`has` use `userID` only as an opaque `dbService` key. No
change needed.

### api.ts — audited, no change needed; `@`-encoding finding relevant to the parallel request-signer task

Every route-building function listed in category (j) — `getUserProfile`,
`getUserInfo`, `getUserProfileWithStatus`, `getUserInfoWithStatus`,
`createUserKeys`, `getReedEchoCount`, `listReplies`, `getReed`,
`listEchoers`, `listRipples`, `postRipple`, `listFollowing`,
`listFollowers`, `getReedOrRemoval`, `deleteReed`, `likeReed`, `unlikeReed`,
`revokeKey`, `getKeyRevocation`, `followUser`/`unfollowUser`,
`getPublicKey` — builds its URL via plain template-literal interpolation
(e.g. `` `/users/${userId}/profile` ``), never via `encodeURIComponent` or
any other escaping on the userID segment. The only `encodeURIComponent`
calls in the file are on invite ids (`getInviteStatus`, `revokeInvite`,
`revokeFederationInvitation`) — a different, out-of-scope identifier class,
correctly left alone. This already matches locked decision 4 (no
URL-encoding of `@`) with zero changes required — these functions were
never encoding in the first place.

**`@`-encoding empirical check (relevant to the parallel request-signer.ts
task, which reads `urlObj.pathname` from a `new URL(...)`):** confirmed via
a direct Node check that the WHATWG `URL` parser — the same parser
`fetch()` uses internally and the same one `request-signer.ts` uses to
build its canonical signed string — does **not** percent-encode `@` in a
path segment:
```
new URL('http://x/api/users/abc123@srv456/profile').pathname
// => '/api/users/abc123@srv456/profile'  (literal '@', unescaped)
```
So a literal `@` survives unmodified from `api.ts`'s template-literal URL,
through `fetch()`, and through any `new URL(...).pathname` read — all three
layers agree, no encoding mismatch risk between this task and the
request-signer task.

Also confirmed unchanged, per the brief: `signup()` POSTs a bare `userID`
**form field** sourced from `apiService.getUserID()` (`GET /users/id`) —
this is the pre-signup bare-id-reservation step described in locked
decision 2 and is correct as-is; the client only starts using the
canonical form from whatever the signup response (or equivalent) hands
back afterward, which is `signing.ts`'s already-audited territory (see the
"Done — SPA signing.ts + verifiers" section above), not this one.

### Verification

- `cd spa && npm install` (svelte-check/vite binaries were not present in
  this fresh worktree checkout) then `npm run check` (svelte-kit sync +
  svelte-check) — **0 errors, 0 warnings.**
- No code changes were made in this task, so no `npm run build` re-check
  beyond `npm run check` was needed to confirm nothing broke; skipped
  re-running build since there is no diff to build against.
- Wrote a throwaway (not committed) script driving the real
  `IndexedDbService` class (via `fake-indexeddb`, installed with
  `npm install --no-save` so `package.json`/`package-lock.json` are
  untouched) with a canonical `"aB3xQ9zK@srv456def"`-shaped id through
  `put`/`get`/`getAllByIndex` across every user-referencing store —
  confirmed no truncation/mangling, and that two distinct canonical ids
  remain distinct records. Script and the throwaway dependency were removed
  before finishing; `git status --short` in `spa/` is clean.
- No existing unit tests reference these six files directly (`npm run
  test:signing`/`test:key-revocation`/`test:identicon`/`test:reed-markdown`
  cover other modules); none were run since none apply, and none were
  edited.

### Not touched (explicitly out of scope, confirmed by grep, matches the doc's own boundary list)

`signing.ts`/`verifiers/index.ts` (category g, already done),
`request-signer.ts`/`request-signer-shared.ts` (category h, parallel task),
`reedRef.ts`/`reedMarkdown.ts`/`identicon.ts` (category k),
`backupRestore.ts`/`accountRecovery.ts`/`recoveryKeyNest.ts` (category l),
any Go file.

## Done — Task 21: fix test fixtures broken by the canonical-everywhere pass, full-stack verification

`go test ./...` initially showed 44 test failures + 1 build failure
spanning 4 Go packages after tasks 1-19 landed. All fixed. Root causes
fell into two buckets:

**Bucket 1 — stale hand-rolled test schemas.** Nine `_test.go` files
each hand-roll a local copy of `db.go`'s schema (`ensureXSchema(db)
error` helpers) rather than using real `InitDB`, so they'd drifted:
still had `users.identity_id` as a separate column from `users.id`
(removed by commit `e970ccd`). Fixed schema + seed-helper INSERTs +
every literal bare/canonical userID argument to match what the actual
current `services.go`/`handlers.go` implementation now expects at each
call site (checked individually per function, not assumed) in:
`mentions_integration_test.go`, `follow_counts_test.go`,
`reply_counts_test.go`, `devices_test.go`, `signup_invite_test.go`,
`handlers_signup_gate_test.go`, `recovery_collision_test.go`,
`federation_test.go`, `federation_handshake_test.go`,
`ripples_test.go`, `ripples_handlers_test.go`, `reed_tip_check_test.go`,
`deletion/store_test.go`, `invites/store_test.go`,
`invites/handlers_test.go`, `roles/roles_test.go` (this last one was a
pre-existing known/accepted `go vet` failure from task 6's signature
change, fixed here too since it was quick).

**Bucket 2 — two genuine, previously-untested production bugs**, found
while tracing why fixture fixes alone didn't make certain handler tests
pass — not test staleness, but real callers now passing canonical
userIDs into functions that still composed `identity.LocalID` internally
expecting bare, silently double-composing (e.g. `"u1@srv@srv"`) and
matching zero rows:

1. `services.go`'s `GetReedRemoval`/`GetAccountRemoval`/
   `HasAccountRemoval` delegate to `deletion.GetCert`/`GetAccountCert`/
   `HasAccountRemoval`, which are still correctly bare-in for their other
   caller (`realtime/db.go`, which decodes to bare first) — but
   `handlers.go`'s `checkRippleParentReed`/`GetUserProfile`/
   `signatureAuthMiddleware` all pass an already-canonical, URL/session-
   sourced userID straight through. Fixed the three `services.go`
   wrappers to decode canonical-in to bare before delegating, leaving the
   `deletion` package's bare-in contract untouched. `ReedExists`/
   `IsBlankEcho` have genuinely mixed callers (`ReedRef.AuthorID` is
   bare, `checkRippleParentReed`'s `userID` is canonical) so gained
   dual-shape acceptance via `ParseIdentityID`, same pattern as
   `BindDevice`.
2. `invites/store.go`'s `CountByCreator`/`Insert`/`GetByCreatorAndID`/
   `Revoke` were documented as "bare-string-in, convert internally," but
   their only real caller (`invites/handlers.go`'s `Create`/`Status`/
   `RevokeInvite`) sources `caller` from the session-authenticated
   request context — canonical since task 13 Option A (`main.go` wires
   `Deps.UserIDKey` to the same context key populated from the canonical
   `X-Syrinx-User-Id` header everywhere else). Fixed these four methods
   to stop composing, since every real caller already passes canonical.
   `MarkClaimed`/`GetPendingInvite` correctly stay bare-in — their real
   callers (`services.go`'s `Signup`, `Check`'s public/unauthenticated
   `uid=` query-param decode) genuinely still pass/decode to bare.

Both bugs were live in the request paths they affect (account/reed
removal lookups, invite creation/status/revocation) and would have
surfaced as real user-facing failures the first time someone exercised
account deletion, reed deletion, or the invite flow through a browser —
no existing test had covered a canonical URL-sourced userID reaching
either code path before this task.

### Final verification

- `go build ./...` — clean.
- `go vet ./...` — clean (the `roles/roles_test.go` failure is fixed,
  not just accepted, as of this task).
- `go test ./...` with `go clean -testcache` first (cold, no caching) —
  every package passes: `syrinx`, `coverage`, `crypto`, `deletion`,
  `encoding`, `identity`, `invites`, `observability`,
  `observability/metrics`, `realtime`, `recovery`, `roles`, `secret`,
  `signing`. Zero failures, zero skips, zero known exceptions remaining.
- No dual-format/staged-rollout code exists anywhere in any of the files
  touched by this task (locked decision 5) — every fix either updates a
  literal test value to the one currently-correct form, or removes
  internal `identity.LocalID` composition outright; no branch was added
  that accepts both bare and canonical as a transition measure, except
  the two pre-existing, deliberate dual-shape acceptance points
  (`BindDevice`, and now `ReedExists`/`IsBlankEcho` for the same
  documented reason: genuinely different callers with genuinely
  different established conventions, not a migration window).
