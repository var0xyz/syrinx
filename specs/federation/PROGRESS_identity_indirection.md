# Progress tracker — `identities` indirection refactor

Companion to [`ANALYSIS_identity_indirection.md`](ANALYSIS_identity_indirection.md)
(the design rationale — read that first for *why*). This doc tracks *what's
actually landed* vs. *what's still outstanding*, so work can resume cold in a
fresh session/context window.

**If you are picking this up fresh: read `ANALYSIS_identity_indirection.md`
first, then this file, then check `git log`/`git diff` against what's
described below before touching anything — this doc is a snapshot, not
live state.**

## The problem (one paragraph)

Federation's planned design has every table reference a remote user via
`(serverID, userID)`, where `serverID` only resolves once a federation
handshake completes. During server recovery (`RECOVERY_MODE`), a peer can
report an identity or reed holding naming a remote server that hasn't been
(re-)federated against this fresh instance yet — hydration order inverted:
recovery stalls on an OOB admin handshake ceremony that has nothing to do
with restoring local data. Fix: an `identities` indirection table that's a
real FK target from day one, with the remote server resolution filled in
later, in place, once the handshake completes.

## Locked decisions (do not re-litigate these — ask only if genuinely stuck)

| Decision | Answer |
|---|---|
| `identities.id` shape | Always `"{userID}@{serverID}"` — for local users, `serverID` = this server's own id (from `servers WHERE self = TRUE`). Root user: `"1@{serverID}"`. |
| Scope | **Full FK repoint** — `identities` is *the* source of truth for "a user," not a federation-only side table. Every table that FK'd to `users(id)` now FKs to `identities(id)`. |
| `users` table | Demoted to a satellite/profile table (mirrors how `private_keys`/`public_keys` hang off `servers`). Keeps its own `id` column **unchanged** (still the bare userID — required because `identity/identity.go` embeds that exact string in cryptographically signed wire payloads; changing it breaks signature verification). Gains `identity_id` — unique FK to `identities(id)`, `ON DELETE CASCADE`. |
| Verification gate | Centralized in a Postgres **view**, `verified_identities` — not a per-caller Go check, not a Go resolver function. Read paths should query the view; only privileged call sites (recovery hydration, handshake completion, admin tooling) query `identities` directly. |
| Migrations | **None — blank slate.** This is a pre-launch project with no production data. Edit `db.go`'s `CREATE TABLE IF NOT EXISTS` DDL directly. Never ask about preserving old rows/signatures/wire formats — there's nothing to preserve. (See memory: `feedback_blank_slate_no_migration_questions.md`.) |
| Collision delete | `recovery/upsert.go`'s `claimUsername` currently does `DELETE FROM users` on the losing side of a username collision. Once repointed, this becomes `DELETE FROM identities` (cascades to `users` and everything else) — same semantics as today, one level higher in the FK chain. **Not yet implemented** — see Task 3 below. |
| Root user | `roles.RootUserID` stays the literal `"1"` in Go, but the canonical `identities.id` for root is `"1@{serverID}"`. Root bootstrap must mint that `identities` row (not just a `users` row). **Not yet implemented** — see Task 6 below. |
| Sequencing | Schema (`db.go`) first, fully landed and verified in isolation, before touching any Go query code — to avoid a half-converted, non-compiling-against-schema intermediate state if context runs out mid-refactor. |
| Canonical-ID Go type | **`IdentityID`** — a distinct Go type (not a bare `string`) for the canonical `"userID@serverID"` form, compiler-enforced so a bare userID can never be silently passed where a canonical id is required. Threaded through **every** call site that touches an FK'd column, in every package (not just `services.go` — also `handlers.go`, `recovery/*`, `realtime/*`, `deletion/*`, `roles/*`). Lives in `identity/` (the one shared package already imported by `main`, `recovery`, and `realtime`) since `recovery`/`realtime`/`deletion` cannot import `main`. **Not yet implemented as of this doc revision** — see Task 2 below, this is the very next thing to build. |
| Frontend / IndexedDB | **In scope, not yet started, not yet a numbered task.** Surveyed via an Explore agent (see "Frontend gap" section below) — the SPA's IndexedDB layer (`spa/src/lib/services/db.ts`) keys every user-referencing store by a bare `userID` string (no server component), even though the app already uses `userID@serverID` as a *display* convention elsewhere (mentions, reed refs). This is the same structural problem the backend just fixed, unaddressed client-side. Needs its own numbered task once backend work stabilizes enough to know the exact wire shape the client needs to consume. |

## Done

### `db.go` — schema foundation (landed, verified)

Commit state: **uncommitted** working-tree change to `db.go` as of this
writing (`git status` shows `M db.go`). Not committed — user said "do not
commit" earlier in this work; confirm with user before committing.

What changed:

1. **New `identities` table** (inserted right after `servers`/
   `user_signatures`/`server_signatures`, before `users`, in both the DDL
   and the `queries` exec-order slice):
   ```sql
   CREATE TABLE IF NOT EXISTS identities (
       id VARCHAR(255) PRIMARY KEY,
       remote_user_id VARCHAR(255) NOT NULL,
       server_id VARCHAR(16) REFERENCES servers(id),
       public_key_fingerprint VARCHAR(255),
       verified BOOLEAN NOT NULL DEFAULT FALSE,
       created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
       UNIQUE (remote_user_id, server_id)
   );
   CREATE INDEX IF NOT EXISTS idx_identities_server_id ON identities(server_id);
   ```
   `server_id` is nullable — a provisional/unverified remote identity can be
   inserted (and FK'd against by every other table) before its server has
   been federated.

2. **`users` demoted to a satellite table** — added:
   ```sql
   identity_id VARCHAR(255) NOT NULL UNIQUE REFERENCES identities(id) ON DELETE CASCADE
   ```
   `users.id` is **unchanged** (still bare userID, not `userID@serverID`) —
   deliberately, to avoid breaking `identity/identity.go`'s signed wire
   payloads.

3. **New `verified_identities` view** (added right after
   `createUsersTable`/`createUserIndexes` in the exec-order slice):
   ```sql
   CREATE OR REPLACE VIEW verified_identities AS
   SELECT i.*
   FROM identities i
   JOIN servers s ON s.id = i.server_id
   WHERE s.self = TRUE
      OR i.verified = TRUE;
   ```
   **Known limitation, still live:** this does NOT exclude a revoked
   peer — there's no `federation_established` table (see
   [03](03_approval_established.md)'s status note; trust actually lives
   on `servers.revoked`), and moreover nothing in the codebase ever sets
   `servers.revoked = TRUE` at all (see
   [05](05_revoke_established.md)'s status note — peer revocation was
   never actually built). Once revocation exists, this view needs
   `AND s.revoked = FALSE` added to its WHERE clause. Flagged in the DDL
   comment in `db.go`.

4. **All 28 FKs that referenced `users(id)` now reference `identities(id)`**
   (mechanical `sed 's/REFERENCES users(id)/REFERENCES identities(id)/g'`,
   then hand-verified). Full list of tables touched: `users.invited_by`,
   `user_keys.owner`, `user_key_revocations.owner`, `reeds.user_id`,
   `reed_echoes.echoing_user_id`, `reed_replies` (via composite FK to
   `reeds`, unaffected directly), `reed_mentions.mentioned_user_id`,
   `reed_removals.user_id`, `account_removals.user_id`,
   `reeds_liked.liker_user_id`, `ripple_responses.user_id`,
   `user_followers.user_id`/`follower_user_id`,
   `user_following.user_id`/`following_user_id`, `online_users.user_id`,
   `reed_allocations.holder_user_id`, `pending_account_events.user_id`,
   `profile_subscriptions.viewer_user_id`/`author_user_id`,
   `unclaimed_accounts.user_id`, `ongoing_recoveries.user_id`,
   `pending_follows.follower_user_id` (note: `following_user_id`
   deliberately stays FK-less — see comment in `db.go`, different
   scenario: target has *no* identity row at all yet, not even
   provisional), `invites.created_by`/`claimed_by`,
   `federation_invitation.created_by`/`reviewed_by`,
   `user_devices.user_id`.
   `broadcast_subscriptions` needed no direct edit — it FKs to
   `online_users(user_id)`, which transitively now resolves to
   `identities`.

5. **Comments updated** in two places to stay accurate post-change:
   - `reed_mentions` comment no longer claims "foreign mentions can't be
     inserted, nothing to FK against" — that's now false (the FK target
     supports it). Clarified that foreign-mention *insertion* is still not
     wired up in Go (separate follow-up, not done), only the schema now
     supports it.
   - `pending_follows` comment updated from "not yet in users" to "no
     identities row at all yet."

**Verification performed:** spun up a throwaway scratch Postgres database
(`syrinx_ddl_scratch`) on the existing local `syrinx_db` Docker container,
ran `InitDB` against it via a temporary `_test.go` file (since `InitDB` is
unexported and lives in `package main`), confirmed all 60 DDL statements
executed cleanly, inspected `\d identities`, `\d users`, `\d+
verified_identities`, and `\d reed_mentions` to confirm every FK actually
points where intended. Scratch DB and temp test file were then dropped/
deleted — nothing left behind. `go build ./...` passes (schema-only changes
are invisible to the Go compiler; this does **not** mean the Go code is
correct against the new schema, only that it still compiles — SQL string
mismatches only surface at runtime).

### `identity/identity_id.go` — `IdentityID` type (landed, verified)

New file, new tests (`identity/identity_id_test.go`, 5 test cases, all
passing). Defines:

- `type IdentityID string` — the canonical `"userID@serverID"` form as a
  distinct type, not a bare string.
- `LocalID(userID, serverID string) IdentityID` / `RemoteID(...)` (alias,
  mechanically identical — the local/remote distinction is only which
  `serverID` gets passed in, never a different construction) —
  constructors.
- `ParseIdentityID(id IdentityID) (userID, serverID string, ok bool)` —
  splits back into parts; `ok=false` on malformed input (no separator, or
  separator at the very start/end).
- `(id IdentityID) UserID() string` / `.ServerID() string` — panic on
  malformed id (a programming error, not bad input — callers should only
  ever hold well-formed ids).
- `(id IdentityID) String() string` — `fmt.Stringer`.

Lives in `identity/` (not `db.go`/`main`) because `recovery`, `realtime`,
and `deletion` are separate packages that cannot import package `main`;
`identity/` is already imported by all of them (or can trivially add the
import for `deletion`/`roles`, which don't yet).

**The conversion rule this settles** (apply everywhere in tasks 3–8
below): a column needs `IdentityID`/canonical-form conversion **if and
only if** it's one of the 27 columns that now FKs to `identities(id)`
(the `owner`, `*_user_id`, `invited_by`, `created_by`, `reviewed_by`,
`claimed_by` columns listed under "All 28 FKs" above). **`users.id`
itself is exempt** — it stayed a bare, unchanged userID column (see
Locked decisions: wire/signature format must not change), so anything
filtering `WHERE u.id = $1`/similar against the `users` table's own PK
keeps using the bare userID untouched. Self-joins on `users` (e.g. via
`invited_by`) must join through `identity_id`, not `id`, on the joined
side.

**Trial call sites converted** (to prove the type works end-to-end before
fanning out to the rest of `services.go`):

- **`Signup`** (`services.go`) — now mints the `identities` row
  (`verified = TRUE`, since local signups need no handshake) inside the
  same transaction as the `users`/`user_keys` inserts, before either.
  `users.identity_id` and `user_keys.owner` both now store the canonical
  form; `users.invited_by` converts the inviter's bare userID (from
  `invites.Invite.CreatedBy`) to canonical via `identity.LocalID` (invites
  are always created by a local user).
- **`GetUserProfile`** (`services.go`) — the `invited_by` self-join
  changed from `LEFT JOIN users inv ON inv.id = u.invited_by` to
  `LEFT JOIN users inv ON inv.identity_id = u.invited_by` (per the
  conversion rule above). The function's own `WHERE u.id = $1` stays a
  bare userID lookup — unaffected.
- **`BindDeviceTx`** (`services.go`) — converts its bare `userID` param
  to canonical internally (`identity.LocalID(userID, s.serverID)`) right
  before touching `user_devices`, which now FKs to `identities`. Function
  signature unchanged (still takes bare `userID string`) — device binding
  is always local-account-only per
  [recovery 17](../recovery/17_device_binding.md), so there's no remote
  case to support here; this is the "bare-string-in, convert internally"
  half of the per-function decision described in Task 2's description
  below, as opposed to functions that would need to accept an already-
  canonical `IdentityID` directly.

**Verification performed:** same scratch-DB approach as the `db.go`
check — temporary `_test.go` in `package main`, real `Signup`/
`GetUserProfile` calls against a live Postgres instance (not mocked),
asserting: `identities` row lands with the exact canonical id and
`verified = true`; `users.identity_id` and `user_keys.owner` both store
the canonical form (not the bare userID); a second manually-inserted user
with `invited_by` pointing at the first user's canonical id round-trips
correctly through `GetUserProfile`'s self-join (`InvitedBy.ID` /
`.Username` both resolve correctly). All assertions passed. Scratch DB and
temp test file deleted after. `go build ./...` and `go test ./identity/...`
both pass.

### `services.go` — all ~109 call-sites (landed, verified)

All call-sites in `services.go` that touch one of the 27/28 FK'd columns
(or a column that transitively requires canonical form via a composite FK
to an already-FK'd table — see "Transitive-FK discovery" below) are now
converted, on top of the three trial functions (`Signup`, `GetUserProfile`,
`BindDeviceTx`) documented above. `GetUserRole` was audited and correctly
required **no change** (see "Judgment calls" below).

**Scope discipline:** only `services.go` was touched. `recovery/*`,
`realtime/*`, `deletion/*`, `roles.go`, `handlers.go`, and all `_test.go`
files were left untouched, per the task boundary — those are separate,
already-tracked follow-up tasks (4–9 below) and some of them (`invites/*`,
`deletion/*`) are now the reason a handful of `services.go` functions
(`GetPendingInvite`, `GetReedRemoval`/`InsertReedRemoval`/
`GetAccountRemoval`/`InsertAccountRemoval`/`HasAccountRemoval`) still
delegate to unconverted code — these will only work correctly once their
respective packages are converted. Not a regression from this pass; just
flagging where the boundary currently sits.

**What changed, function by function** (grouped, not exhaustive — see
`git diff services.go` for the literal diff):

- **Auth/session helpers**: `IsUnclaimed`, `IsOngoing` — convert internally
  via `identity.LocalID`, bare-string signature kept (callers in
  `handlers.go` always pass a local, session-scoped userID for own-account
  recovery-status checks).
- **Keys**: `GetPublicKey`, `GetKeyRevocation`, `PublicKeyExists`,
  `AddPublicKey`, `RevokeKey` — all converted. `AddPublicKey`'s existence/
  concurrency lock retargeted from `SELECT 1 FROM users WHERE id = $1 FOR
  UPDATE` to `SELECT 1 FROM identities WHERE id = $1 FOR UPDATE`, per the
  task brief's flagged special case (identities is the actual FK target
  now, not users). `checkReedTipTx`'s analogous lock got the same
  retarget. Every place that used to scan `uk.owner`/`rv.owner` straight
  into a wire-shape `Key.UserID`/`KeyRevocation.UserID` field now scans
  into `identity.IdentityID` first and decodes back via `.UserID()` —
  those fields are wire shape (mirrors `users.id`'s own bare-userID
  exemption) and must never leak the canonical `userID@serverID` form to
  callers.
- **Follows**: `FollowUser`, `UnfollowUser`, `listFollowEdge` (shared by
  `ListFollowing`/`ListFollowers`), `ListUserFollowing` — all converted.
  `GetUserInfo`'s follower/following/has-reeds subqueries were joining
  against the bare `u.id` instead of `u.identity_id` — see "Judgment
  calls" below, this was a live bug, not a mechanical conversion.
- **Reeds**: `checkReedTipTx`, `insertReedCoreTx` (and everything that
  calls it — `CreateReed`, `CreateReedWithEcho`, `CreateReedWithReply`),
  `insertReplyTx`/`InsertReply`/`ResolveThreadIDForParent`/`ListReplies`,
  `IsBlankEcho`, `GetReedAttestation`, `DeleteReed`, `ReedExists`,
  `CountEchoes`, `GetReedChorus`, `DeleteEchoIndexForReed`,
  `DeleteEchoesByAuthor`, `DeleteMentionsForReed`,
  `DeleteMentionsByAuthor`, `ReplyCountNotifyTargets` (+`ForRemovedReply`/
  `ForAuthor`), `GetSubtreeReplyCount`, `GetReed`, `ListUserReeds` — all
  converted, including the transitive-FK columns discovered along the way
  (see below). `reed_echoes.echoed_user_id`/`echoed_reed_id` and
  `reed_mentions`' non-owning side were deliberately left bare — no FK on
  those columns at all (verified directly in `db.go`, not assumed).
- **Likes**: `GetReedLike`, `InsertReedLike`, `DeleteReedLike`,
  `CountLikes`, `loadLikeCertTx` — converted; `loadLikeCertTx`'s signature
  changed to take `identity.IdentityID` params directly (it's an internal
  helper, all three callers already had the canonical form in hand).
- **Devices**: `GetActiveDeviceID` — converted (was missed by the
  `BindDeviceTx` trial pass; `CheckActiveDevice` needed no change, it
  delegates).
- **Federation invitations**: `InsertFederationInvitation`,
  `GetFederationInvitation`, `ListFederationInvitations`,
  `RevokeFederationInvitation` — all converted.
  `ListFederationInvitations`'s dual `JOIN users` (creator/reviewer
  display names) now joins through `identity_id`, same pattern as
  `GetUserProfile`'s `invited_by` self-join; the nullable `reviewed_by`
  column is scanned as a plain string and decoded via `ParseIdentityID`
  (its `ok` bool) rather than scanned directly into `identity.IdentityID`,
  since an empty string isn't a well-formed id and `.UserID()` panics on
  malformed input.
- **Ripples**: `PostRipple`, `scanRipple` (shared by `GetRipple`/
  `ListRipples`), `ListRipples`, `GetRipplesExpiresAt`, `SoftDeleteRipple`
  — all converted. `PostRipple` is the one function in this file that
  needed both forms of a value live at once: `identity.BuildRippleServerPayload`
  is a signed-wire-payload builder and must keep receiving the bare
  `reedAuthorID`/`userID` (per the "wire format unchanged" locked
  decision), while the `ripples`/`ripple_responses` INSERTs need the
  canonical form — handled with separate `reedAuthorIdentity`/
  `selfIdentity` locals alongside the original bare params, not by
  converting the params in place.
- **Not touched (confirmed out of scope / dead code, not silently
  dropped)**: `SetDefaultIdentity` (targets a `profiles` table that
  doesn't exist anywhere in `db.go` — pre-existing dead/broken code,
  unrelated to this refactor); `DeleteUser` (zero callers anywhere in the
  codebase — account removal goes through `deletion.InsertAccountCert`/
  `account_removals` instead; left as dead code with a comment flagging
  that it would need the same `DELETE FROM identities` cascade fix the
  PROGRESS doc's "Collision delete" decision already calls out for
  `recovery/upsert.go`'s `claimUsername`, if it's ever wired up).

**Transitive-FK discovery (a real deviation from the task brief's literal
scope, done deliberately):** the task brief's conversion rule was framed
as "one of the 27/28 columns with a direct `REFERENCES identities(id)`
line." In practice several tables have **no direct FK to `identities`**
but instead a **composite FK to an already-FK'd table**, which forces the
same canonical-form requirement transitively — Postgres will reject the
insert otherwise, since the composite FK compares both columns for an
exact match. Traced and converted:
- `reed_replies.user_id` / `parent_user_id` — composite FK to
  `reeds(user_id, id)`.
- `pending_fanout.user_id` — composite FK to `reeds(user_id, id)`.
- `reed_allocations.author_user_id` — composite FK to `reeds(user_id, id)`
  (`holder_user_id` was already a direct FK, per the original list).
- `reeds_liked.author_user_id` — composite FK to `reeds(user_id, id)`
  (`liker_user_id` was already direct).
- `ripples.reed_author_id` — composite FK to `reeds(user_id, id)`.
- `ripple_responses.reed_author_id` — composite FK to
  `ripples(reed_author_id, reed_id)`, which itself chains to `reeds` —
  two levels of transitivity (`user_id` on the same table was already
  direct).
- `reed_mentions.mentioning_user_id` — composite FK to
  `reeds(user_id, id)` (`mentioned_user_id`, the mention *target*, was
  already direct).

This is not scope creep for its own sake — without it, every reed-adjacent
insert in the file would fail at runtime with a foreign-key violation the
moment `reeds.user_id` actually held a canonical value, because the
composite FK requires an exact match on both sides. `pending_reed_events.
user_id` (also composite-FK'd to `reeds`) is not in `services.go` at all
— no action needed here, flagged only for whoever picks up the next
package.

**Judgment calls made (flagged explicitly, as requested):**

1. **`GetUserRole`** — audited, found to need **no change**. It queries
   `SELECT role FROM users WHERE id = $1`, which is the bare, exempt PK —
   correct as-is, since every caller (`handlers.go`'s `isAdmin`) always
   passes the session-authenticated local caller's own bare userID. This
   was the flagged authz special case in the task brief; the conclusion
   is "already correct," not "needed a fix."

2. **`MentionTargetValid`** — the task brief specifically asked me to
   reason through whether this should resolve via `verified_identities`
   or raw `identities`/`users` existence. Conclusion: for today's reality
   (only local mentions are ever inserted — see `db.go`'s `reed_mentions`
   comment), querying `users`/`account_removals` directly and querying
   `verified_identities` produce **identical results**, because every
   local identity is unconditionally verified in the view's own
   definition (`s.self = TRUE OR i.verified = TRUE`). So this function's
   existing raw-existence-check shape was left as-is (not routed through
   the view) — changing it would be a no-op for current behavior. Left an
   explicit comment on the function that this will need revisiting once
   foreign mention targets are actually wired up (a provisional,
   unverified remote identities row must not pass this gate at that
   point), per `ANALYSIS_identity_indirection.md`'s "single choke point"
   requirement. This is a real, load-bearing judgment call, not a
   mechanical one — flagging it here as instructed.

3. **`u.id` vs `u.identity_id` bug class — found and fixed in three
   places beyond the three trial functions.** Comparing the exempt bare
   `users.id` against a now-FK'd column that stores the canonical form is
   a silent, always-false comparison (not a compile error, not even
   guaranteed to error at runtime — it just never matches). Found and
   fixed in:
   - `GetUserInfo` — `has_reeds`/`followersCount`/`followingCount` were
     all joining `r.user_id`/`uf.user_id`/`ufl.user_id` against the bare
     `u.id`; would have silently returned 0/false for every user, always,
     once the FK'd columns actually held canonical values. Fixed by
     joining through `u.identity_id`.
   - `SearchUsers` — `account_removals ar WHERE ar.user_id = u.id` would
     never match, silently leaking removed accounts back into search
     results. Fixed via `u.identity_id`.
   - `MentionTargetValid` — same pattern, same fix, folded into the
     judgment-call writeup above since it's the same function the task
     asked me to reason through anyway.

   Flagging this as its own item because it's a bug class, not a single
   fix — it's exactly the kind of "missed call site" the codebase's own
   `feedback_duplicate_queries.md`-style caution and
   `ANALYSIS_identity_indirection.md`'s "single choke point" reasoning
   warn about. Every other `u.id`/bare-PK usage in the file was
   re-checked against this same pattern (see the independent audit note
   below) and no further instances were found.

4. **Ripple wire-payload / DB-write split (`PostRipple`)** — not really a
   judgment call so much as a hazard worth flagging loudly: this is the
   one function in the file where getting the bare-vs-canonical split
   wrong in either direction breaks something invisibly (wrong wire
   payload → signature verification breaks silently downstream; wrong DB
   value → FK violation, at least that one's loud). Handled by keeping
   both a bare and a canonical local variable live at once rather than
   converting in place — see the "Ripples" bullet above.

**Verification performed:** same scratch-DB approach as `db.go` and the
trial functions — temporary `_test.go` in `package main`
(`syrinx_ddl_scratch2`, dropped after), exercising, against a real
Postgres instance: two local signups; `GetUserRole`/`GetUserInfo`
(explicitly asserting `FollowersCount` after a real follow, to catch the
`u.id`-vs-`u.identity_id` bug class); `FollowUser`/`UnfollowUser`/
`ListUserFollowing`/`ListFollowing`; `SearchUsers`; `RevokeKey` +
`AddPublicKey` rotation + `GetPublicKey`; `CreateReed`; `MentionTargetValid`;
`InsertReedLike`/`CountLikes`/`DeleteReedLike`; `CreateReedWithEcho` +
`CountEchoes`/`GetReedChorus`; `CreateReedWithReply` +
`ResolveThreadIDForParent`/`ListReplies`/`GetSubtreeReplyCount`/
`ReplyCountNotifyTargets`; a mentioning reed + `reed_mentions` row
assertions; `PostRipple`/`GetRipple`/`ListRipples`/`SoftDeleteRipple`;
`ListFederationInvitations`'s dual-join smoke check; `IsUnclaimed`. Every
FK'd/transitively-FK'd column touched by these flows was asserted against
its actual stored value (canonical where expected, bare where a column
has no FK), not just "the query didn't error." All assertions passed.
`go build ./...` and `go vet ./...` both clean throughout. Scratch DB and
temp test file deleted after — nothing left in the working tree beyond
`services.go` itself.

An independent second-pass audit (fresh agent, no context from the
conversion work itself, re-derived the FK/composite-FK list directly from
`db.go` rather than trusting this write-up) found one additional
pre-existing, not-introduced-by-this-diff issue outside the scope of the
conversion rule: `GetUserInfo`'s `has_reeds` subquery
(`EXISTS (SELECT 1 FROM reed_removals rr WHERE rr.reed_id = r.id)`) is
missing `AND rr.user_id = r.user_id` — since reed ids are only unique
per-author, this can match a different author's removal row with the same
reed id. Left unfixed: it's not a bare-vs-canonical bug, it predates this
diff, and fixing it would be changing query logic outside this task's
conversion-rule scope rather than converting an identity column. Flagging
here so it isn't lost.

### `recovery/upsert.go` + `recovery/reeds_follows.go` + `recovery/store.go` + `recovery/nest.go` (landed, verified)

All four files in scope converted: `recovery/upsert.go`, `recovery/reeds_follows.go`,
`recovery/store.go`, `recovery/nest.go`. `recovery/nest.go` needed **no
changes** — first-time review (it was never covered by the original
inventory, per this doc's Task 8) confirmed it has zero DB access; every
function in it (`FlattenKeysNest`, `VerifyProfileServerCountersig`,
`verifyKeyCountersig`, `verifyRevocation`, `decodeKeyNestArmor`,
`ValidateChallengeAge`, `VerifyChallengeSignature`) is wire-payload
verification only, calling `identity/identity.go`'s `Build*Payload`
functions with bare userIDs — which is correct as-is, since those payloads
are signed bytes and must never receive the canonical `userID@serverID`
form (same `users.id` exemption as everywhere else).

**Key judgment call, made explicitly (this is the deviation from a naive
reading of the task brief, flagging prominently as instructed):** the task
brief frames this package's whole point as "unblocking a not-yet-federated
remote user" and asks each function to be judged local-vs-remote. Audited
`recovery/wire.go`'s wire types (`Profile`, `ReedRequest`,
`FollowingRequest`, `ServerSignature`) and every verification call site in
`nest.go`/`handlers.go`: **there is no `serverID` field anywhere on a
subject** (author, follow target, profile being claimed/reported) — only
`ServerSignature.ServerID`, which is checked equal to this server's own
`serverID` at every single verification call site
(`nest.go:43,138,175,210`, `handlers.go:112`). Cross-checked against
`specs/recovery/05_peer_identity_report.md` and `06_reeds_follows_complete.md`:
"peer" in this package's spec means another **user** on this same server
whose cached profile a claiming user re-reports from their own device —
not another **server**. So **every identity minted or looked up in this
package is local** (`identity.LocalID(userID, serverID)`, never
`identity.RemoteID`) — the same "bare-string-in, convert internally"
pattern `services.go`'s `BindDeviceTx` uses, not the provisional/remote
path the task brief's framing initially suggested. The genuinely
cross-server "remote peer reports a not-yet-federated identity" scenario
`ANALYSIS_identity_indirection.md` describes is a **federation-side**
concept (proposals 04/06, not yet built) — this package's job today is
narrower: don't block on `identities` existing, using the local form. This
reasoning is recorded as a file-level comment at the top of
`recovery/upsert.go`.

**What changed, file by file:**

- **`recovery/upsert.go`**:
  - `SaveOwnIdentity`/`SavePeerIdentity`/`upsertIdentity`/`insertUser` all
    gained a `serverID string` parameter (threaded from `recovery.Deps.ServerID`
    via `recovery/identity.go`'s handlers) — needed to construct the
    canonical form, since nothing in this package carried a serverID
    before.
  - `upsertIdentity`'s existence check + row-lock retargeted from
    `SELECT ... FROM users u ... WHERE u.id = $1 FOR UPDATE OF u` to
    `SELECT ... FROM identities i JOIN users u ON u.identity_id = i.id ...
    WHERE i.id = $1 FOR UPDATE OF i` — locks the `identities` row (the
    actual FK target), exactly the task brief's flagged rewrite seam.
  - `insertUser` now mints **both** the `identities` row (`verified = TRUE`,
    `server_id = serverID`, since every subject here is local — no
    handshake needed, mirrors `services.go`'s `Signup`) and the satellite
    `users` row (now including `identity_id`, which the original code
    never set — a real gap the old code would have hit as a `NOT NULL`
    violation the moment `db.go`'s schema change landed). Minted with
    `ON CONFLICT (id) DO NOTHING` — see "Open design question 1" below for
    why this is a deliberate, documented compromise, not silently glossed
    over.
  - `updateUserIfNewer` — audited, confirmed it correctly touches only
    `users` profile columns (username/bio/role/fingerprint/signatures),
    never `identities` — this is exactly the "update in place, never
    rewrite the identities row" mechanic the design requires; there is
    simply nothing on the `identities` row that a same-server re-report
    should change.
  - `insertKeys` — signature changed from bare `userID string` to
    `owner identity.IdentityID` (callers already hold the canonical form,
    same as `services.go`'s `loadLikeCertTx`).
  - `claimUsername` — **locked decision implemented**: the collision
    loser is now deleted via `DELETE FROM identities WHERE id =
    $1` (using the holder's `identity_id`, fetched via the same query)
    instead of `DELETE FROM users WHERE id = $1`. `ON DELETE CASCADE`
    from `identities` removes the satellite `users` row and everything
    else in one shot — same semantics as before, one level higher in the
    FK chain. The `u.id <> $2` comparison in the same query correctly
    stayed a bare-userID comparison (both sides are the exempt `users.id`
    PK, not an FK'd column).
  - `drainPendingFollows` — gained a second parameter,
    `targetIdentity identity.IdentityID`, alongside the existing bare
    `targetUserID string`. Necessary because `pending_follows.following_user_id`
    has **no FK** (by design — db.go's comment: the target may have no
    `identities` row at all yet) and so only ever stores the bare form,
    while the two destination tables it drains into
    (`user_following`/`user_followers`) are both fully FK'd to
    `identities` and need the canonical form. `pending_follows.follower_user_id`
    **is** FK'd (confirmed directly in `db.go`), so the `SELECT
    follower_user_id`/`SELECT following_user_id` pulled from that table
    inside the drain query are already canonical — no extra conversion
    needed on that side.
  - `bindClaimDeviceTx` — signature changed to accept
    `ownerIdentity identity.IdentityID` directly (device binding is
    local-account-only per `specs/recovery/17_device_binding.md`, so its
    caller — `SaveOwnIdentity` — always has `selfIdentity` in hand
    already; same convention as `services.go`'s `BindDeviceTx`).

- **`recovery/reeds_follows.go`**:
  - `SaveReed`/`SaveFollowing` both gained a `serverID string` parameter.
  - `SaveReed`'s author-existence check retargeted from
    `SELECT EXISTS(SELECT 1 FROM users WHERE id = $1)` to
    `SELECT EXISTS(SELECT 1 FROM identities WHERE id = $1)` — **this is
    the actual fix the task brief calls "the motivating use case."**
    Verified end-to-end in the scratch DB test: a reed holding for an
    author who exists *only* as a just-minted `identities`+`users` row
    (peer-reported via `SavePeerIdentity`, never claimed) inserts
    successfully.
  - `reeds.user_id`, `reed_allocations.holder_user_id` (direct FK) and
    `reed_allocations.author_user_id` (composite FK to `reeds(user_id,
    id)`, transitive — same "transitive-FK discovery" class
    `services.go`'s conversion pass hit) all converted to canonical form.
  - `coverage.BumpAllocationCount`'s `authorUserID` parameter also
    converted to canonical, since it filters `reeds.user_id = $2` — the
    same FK'd column, even though `coverage/stats.go` itself is out of
    this task's scope (its parameter is a plain string with no FK of its
    own to react to).
  - `SaveFollowing`'s existing-target check retargeted from `SELECT id
    FROM users WHERE id = ANY($1)` to `SELECT id FROM identities WHERE id
    = ANY($1)` (same reasoning as `SaveReed`'s author check).
    `user_following`/`user_followers` inserts use the canonical form;
    the `pending_follows` insert for a still-unknown target keeps the
    **bare** form on `following_user_id` (no FK, by design) while
    `follower_user_id` (FK'd) is canonical — matches
    `drainPendingFollows`'s expectations in `upsert.go` exactly.

- **`recovery/store.go`** — mechanical conversion, as expected. All four
  wrappers (`InsertUnclaimed`, `DeleteUnclaimed`, `InsertOngoing`,
  `DeleteOngoing`) gained a `serverID string` parameter and now convert
  internally via `identity.LocalID`, matching `services.go`'s
  `IsUnclaimed`/`IsOngoing` pattern exactly. `CountUnclaimed` needed no
  change (unfiltered row count, no user-scoped column touched).

- **`recovery/nest.go`** — no changes (see above).

- **Call-site updates outside the four in-scope files, kept to the
  minimum required to keep the package compiling** (not a violation of
  the "don't touch handlers.go" boundary — these are `recovery/handlers.go`
  and `recovery/identity.go`, the package's own HTTP handler files, not
  the root `handlers.go`, which was correctly left untouched):
  `recovery/handlers.go`'s `ReportReed`/`ReportFollowing`/`CompleteImport`
  and `recovery/identity.go`'s `ClaimIdentity`/`ReportPeerIdentity` now
  pass `d.ServerID` (already present on `recovery.Deps`, needed no new
  wiring) as the new `serverID` argument to `SaveReed`/`SaveFollowing`/
  `SaveOwnIdentity`/`SavePeerIdentity`/`DeleteOngoing`.

**Open design questions — how they were handled here (flagging prominently
as instructed, not silently resolved):**

1. **Pre-handshake uniqueness** (`UNIQUE (remote_user_id, server_id)`
   doesn't dedupe `server_id IS NULL` rows). **Did not fully solve this —
   not expected to.** Every identity minted in this package today has a
   non-NULL `server_id` (always the local `serverID`, never NULL), so the
   specific NULL-dedup gap the analysis doc describes cannot actually
   occur through this package's current code paths — there is no code
   path in `recovery/*` today that mints a `identities` row with
   `server_id IS NULL`. `insertUser`'s `INSERT ... ON CONFLICT (id) DO
   NOTHING` sidesteps a *same-id* race (two concurrent same-server
   inserts for the same `userID@serverID`) but does **not** address the
   analysis doc's actual concern (multiple *different* provisional rows
   for what should be the same not-yet-verified remote user, which
   requires `server_id IS NULL` to occur in the first place). This gap is
   still open and will become live the moment a federation-side path
   (proposals 04/06) starts minting genuinely remote/provisional
   `identities` rows — flagging here so it isn't lost when that work
   starts, per the task brief's explicit instruction not to make it
   silently worse. Not touched further in this pass.
2. **Row update-in-place mechanics on handshake completion.** Confirmed
   `updateUserIfNewer` never touches the `identities` row (only `users`
   profile columns) — this was a deliberate check, not an oversight, so
   that a future federation-side handshake-completion code path (still
   unbuilt) retains a clean seam to update `identities.server_id`/
   `verified` in place without this package's own update path fighting
   it or silently overwriting those fields on every same-server
   re-report. No handshake-completion code was added here — out of
   scope, per the task brief.

**Verification performed:** scratch Postgres database
(`syrinx_recovery_scratch`) created via the existing `syrinx_db` Docker
container; ran `InitDB` against it via a temporary `_test.go` file
(`package main`, since `InitDB` is unexported). To get it to compile and
run, `recovery_collision_test.go` (a **pre-existing** `_test.go` file, not
one of the four in-scope files, calling `SaveOwnIdentity`/`SavePeerIdentity`
with their old signatures against its own pre-`identities` ad hoc schema —
see "Known consequence" note below) was temporarily moved out of the
package directory for the duration of the test run only, then moved back
byte-for-byte unchanged immediately after — confirmed via `git status`/
`git diff` showing zero changes to that file before committing to this
report. Exercised, against real Postgres, asserting actual stored values
(not just "no error") at every step: `InitDB`; a real local `Signup` (root
user) to have a genuine local identity in play; `SaveOwnIdentity` for a new
local claim (asserted `identities.server_id`/`verified`, `users.identity_id`,
absence from `unclaimed_accounts`, presence in `ongoing_recoveries`);
`SavePeerIdentity` for a fresh peer — **the critical case** — proving a
provisional-shaped `identities` row (freshly minted, no prior claim) is
insertable and immediately FK-usable, and that it correctly lands in
`unclaimed_accounts`; `SaveReed` against that freshly peer-reported
author (proving the motivating scenario: a reed holding for an author
who exists only via peer report, never claimed, inserts successfully) and
asserted `reeds.user_id`/`reed_allocations.holder_user_id`/`author_user_id`
all store canonical values; `SaveReed` against an unknown author asserting
`ErrAuthorNotFound`; `SaveFollowing` against an unknown target asserting a
bare-form row lands in `pending_follows`; a follow-up `SavePeerIdentity`
for that same target asserting `drainPendingFollows` correctly moved the
edge into `user_following`/`user_followers` in canonical form and deleted
the `pending_follows` row; a username collision asserting the loser's
`identities`, `users`, and `user_keys` rows are all gone (cascade) and the
winner is untouched; `store.go`'s four wrappers plus `CountUnclaimed`.
`go build ./...` passes cleanly throughout (checked both before and after
the temporary test file's removal). Scratch DB dropped and temp test file
deleted after — nothing left in the working tree beyond the five
`recovery/*.go`/`recovery/handlers.go`/`recovery/identity.go` source
changes.

**Known consequence, flagged prominently (not hidden):** the new
`serverID string` parameter on `SaveOwnIdentity`/`SavePeerIdentity` breaks
compilation of the pre-existing `recovery_collision_test.go` (root package
`main`, calls both functions with their old signatures against its own
ad hoc pre-`identities` schema). `go build ./...` (non-test code) passes
cleanly, but `go vet ./...` / `go test ./...` do **not** — this was true
before my change too in the sense that the fixture already didn't know
about `identities` at all (it manually drops/recreates tables with direct
`REFERENCES users(id)` FKs, no `identities`/`identity_id` awareness), but
my signature change is what turns that latent mismatch into a compile
error. This is unavoidable without touching a `_test.go` file, which is
explicitly out of scope for this task — `services.go`'s conversion pass
hit the same category of issue and deferred it the same way (see this
doc's Task 9: "test fixture repointing... is a distinct follow-up task").
Whoever picks up Task 9 needs to repoint or delete
`recovery_collision_test.go` (and re-check for any other stale fixtures)
before `go vet ./...`/`go test ./...` will pass again.

### `realtime/db.go` + `realtime/auth.go` (landed, verified)

Converted all ~53 call-sites (2 in `auth.go`, ~51 in `db.go`) that touch a
directly- or transitively-FK'd column. `DBService`/`AuthService`/
`RealtimeService` constructors (`NewDBService`, `NewAuthService`,
`NewService`) now take `serverID string` explicitly (threaded from
`main.go`'s `dataService.GetServerID()`), cached as a struct field —
avoiding a per-call DB round-trip to resolve `servers WHERE self = TRUE`.
Every subject in this package is local (WebSocket connections belong to
this server's own signed-in users; no remote/federated actor exists in
this call graph today), so the uniform pattern is
`identity.LocalID(userID, serverID)` right before touching an FK'd column
— same "bare-string-in, convert internally" convention as `services.go`/
`recovery/*`.

**`AuthenticateWebSocket`/HTTP-auth-parity (the critical risk for this
package):** `AuthenticateWebSocket` (`realtime/auth.go`) is a separate,
parallel implementation of the same trust decision as HTTP's
`signatureAuthMiddleware`, using a query string instead of headers.
Verified both paths reach the same verdict:

- `user_keys.owner` / `user_key_revocations.owner` (via `getPublicKey`) —
  both FK'd to `identities(id)` and actually **written** in canonical
  form (`services.go`'s `Signup`/`AddPublicKey`/`RevokeKey` are already
  converted) — so querying them canonically here reproduces exactly what
  the HTTP path's `GetPublicKey` already does. No divergence.
- `account_removals.user_id` — FK'd to `identities(id)` in schema, but
  its writer (`deletion.InsertAccountCert`, in the **not-yet-converted**
  `deletion/*` package) still inserts the bare session userID verbatim.
  The HTTP path's equivalent (`signatureAuthMiddleware` →
  `services.go`'s `HasAccountRemoval` → `deletion.HasAccountRemoval`,
  confirmed by reading all three) also queries with the bare userID, for
  the same reason. So `AuthenticateWebSocket` deliberately queries
  `account_removals` with the **bare** userID too — converting only this
  side would silently stop matching real rows (a fail-open bug: removed
  accounts would keep authenticating over WebSocket), which is strictly
  worse than not touching it. This resolves automatically once
  `deletion/*` converts (next task) — flagged in both a code comment on
  `AuthenticateWebSocket` and here so it isn't "fixed" into a divergence
  by someone who hasn't read this reasoning.

**Other judgment calls, all documented inline in code comments:**

- `GetUsername` — its one caller passes an already-canonical value (from
  `pending_reed_events.user_id`, transitively FK'd via composite FK to
  `reeds(user_id, id)`), not a bare userID as the parameter name
  suggested. Naively querying `users.id = $1` would have been the same
  "always-false comparison" bug class caught in `services.go`'s pass
  (`GetUserInfo`/`SearchUsers`). Fixed by joining through
  `users.identity_id` instead, keeping the function's own signature/
  param name unchanged since callers already hold the canonical value.
- `GetMissingRemovals` / `GetReedRemovalWire` / `GetMissingAccountRemovals`
  / `GetAccountRemovalWire` — pass **bare** userIDs to
  `deletion.GetCert`/`deletion.GetAccountCert`, matching
  `services.go`'s already-verified `GetReedOrRemovalCert` (confirmed
  identical pattern there: no `identity.LocalID` conversion before either
  call), because those `deletion/*` functions expect bare userIDs against
  columns `deletion/*` still writes bare. Converting ahead of `deletion/*`
  would break the match.
- `GetMissingAccountRemovals`'s two `EXISTS` subqueries have a real,
  pre-existing cross-package gap (comparing canonical
  `user_following`/`reed_allocations` columns against bare
  `account_removals.user_id`) — flagged, not papered over; only
  `deletion/*`'s conversion can close it.
- `ClearPeerStateForRemovedAccount` — heaviest function, all 6 FK'd
  tables (`user_following`/`user_followers` both directions,
  `reed_allocations`, `reeds` via UPDATE-in-CTE) converted atomically in
  one transaction, both identities (`viewerIdentity`/`removedIdentity`)
  built once up front and reused consistently.
- `GetBroadcastSubscribers` — CTE re-derived from scratch;
  `broadcast_subscriptions.user_id` has no *direct* FK to `identities`
  but is canonical transitively via its own FK to `online_users(user_id)`.

**Verification performed:** `go build ./...` and `go vet ./realtime/...`
pass clean (root `go vet ./...`/`go test ./...` still fail against the
pre-existing, out-of-scope `recovery_collision_test.go` — unrelated,
already tracked). Scratch-Postgres verification exercised: `InitDB`,
local signups, `AuthenticateWebSocket` directly (normal pass-through,
revoked-key rejection, removed-account rejection), presence/online
tracking, follows, `GetBroadcastSubscribers`, and
`ClearPeerStateForRemovedAccount`, asserting real stored/returned values.
Scratch database and temporary test file were deleted before finishing —
confirmed via `git status`.

**Process note:** the first attempt at this task completed and verified
correctly but was lost before being committed (its worktree was cleaned
up before the coordinator captured a path/branch reference). The retry
that produced the above additionally hit an API session limit partway
through its own closing steps (independent self-audit + progress-doc
update + commit) — the coordinator (a different Claude session)
independently re-verified the full diff against real Postgres reasoning
by reading every converted function, cross-checked the two
`deletion/*`-interop judgment calls above against the actual
`deletion/account.go` source (confirmed accurate), and wrote this
section + performed the commit.

### `deletion/account.go` + `deletion/store.go` (landed, verified)

Converted both writers (`InsertAccountCert`, `InsertCert`) and both readers
(`GetAccountCert`/`loadAccountCertTx`, `GetCert`/`loadReedCertTx`,
`HasAccountRemoval`) to store/query `account_removals.user_id` /
`reed_removals.user_id` in canonical form. Also converted every downstream
caller in `services.go` and `realtime/*` that had been deliberately passing
bare userIDs into this package's functions to match the old bare storage —
that special-casing is now gone everywhere.

**What changed in `deletion/*` itself:**

- Both packages gained a `syrinx/identity` import.
- `InsertAccountCert(ctx, db, cert, serverID string)` and
  `InsertCert(ctx, db, cert, serverID string)` — both gained a `serverID`
  parameter. `cert.UserID` stays bare on the `AccountCert`/`Cert` structs
  (confirmed load-bearing: `handlers.go` constructs these structs with a
  bare session userID and reads `cert.UserID` back into wire responses
  directly — those structs are explicitly documented as "in-memory /
  wire-facing shape" and must not change). Conversion happens **internally**,
  right before touching `account_removals`/`reed_removals`, via
  `identity.LocalID(cert.UserID, serverID)` — the same "bare-string-in,
  convert internally" convention every other converted package uses.
- `GetAccountCert(ctx, db, userID, serverID string)`, `GetCert(ctx, db,
  userID, reedID, serverID string)`, and `HasAccountRemoval(ctx, db,
  userID, serverID string)` all gained the same `serverID` parameter and
  convert the same way before querying.
- `loadAccountCertTx`/`loadReedCertTx` (internal helpers) now take an
  already-built `identity.IdentityID` directly (both call sites — insert's
  pre-check and the public getter — already have it in hand after the
  signature change), and decode back to the bare form via `.UserID()` when
  populating the returned `AccountCert`/`Cert`.
- The `deletion/account.go` lines ~76–96
  "null-out-satellite-fields, cascade-delete signature rows" pattern
  (clearing `users.username`/signature columns while leaving the row in
  place) was used as read-only precedent, per the task brief — not modified,
  since it already correctly operates on the bare, exempt `users.id` PK.
- Confirmed via `db.go` that this conversion is not optional: both
  `account_removals` and `reed_removals` carry a composite FK on
  `(user_id, user_fingerprint) REFERENCES user_keys(owner, fingerprint)`,
  and `user_keys.owner` has stored canonical form since `services.go`'s
  conversion pass — so a bare `user_id` here would eventually violate that
  composite FK the moment `user_keys.owner` actually held a canonical value
  matching a real key. This is the same transitive-FK class flagged
  repeatedly in earlier "Done" sections, confirmed by reading `db.go`
  directly rather than assumed.

**What changed in `services.go`:** `GetReedRemoval`, `InsertReedRemoval`,
`GetAccountRemoval`, `InsertAccountRemoval`, `HasAccountRemoval` (the five
`DataService` wrappers around `deletion.*`) now pass `s.serverID` as the
new parameter. `GetReedOrRemovalCert` needed no change — it already called
the bare-`userID` wrapper functions, which now handle conversion internally.

**What changed in `realtime/db.go` / `realtime/auth.go`:**

- `GetMissingRemovals`: scans `rr.user_id` into `identity.IdentityID`
  (previously scanned into a bare `string` and passed straight through)
  and passes `.UserID()` + `ds.serverID` to `deletion.GetCert`.
- `GetReedRemovalWire`: passes `ds.serverID` through to `deletion.GetCert`.
- `GetMissingAccountRemovals`: scans `ar.user_id` into
  `identity.IdentityID`, passes `.UserID()` + `serverID` to
  `deletion.GetAccountCert`. **The SQL itself needed no change** — the two
  `EXISTS` subqueries (`uf.following_user_id = ar.user_id`,
  `ra.author_user_id = ar.user_id`) were already written assuming
  `ar.user_id` was canonical (per this doc's `realtime/*` Done-section
  comments, which explicitly said so); only the underlying stored data was
  wrong. Converting `deletion.InsertAccountCert`'s writer closes the gap
  with zero query changes.
- `GetAccountRemovalWire`: passes `ds.serverID` through to
  `deletion.GetAccountCert`.
- `ReedExists` (both the `realtime/db.go` copy and its `services.go` twin):
  **found and fixed a second instance of the same class of gap**, not
  called out in the original task brief's known-call-sites list. Both
  functions' `NOT EXISTS` subqueries compare `rr.user_id = r.user_id` and
  `ar.user_id = r.user_id` against the already-canonical `r.user_id` —
  these silently never matched a real removal row before this fix (bare
  vs. canonical string comparison), meaning a removed reed or a reed by a
  removed account could still report as "exists." No SQL change needed
  here either (same reasoning as `GetMissingAccountRemovals`); the
  `realtime/db.go` copy's doc comment (which explicitly documented the gap
  as "flagging, not fixing") was updated to reflect closure. Verified in
  the scratch-DB pass below.
- `AuthenticateWebSocket` (`realtime/auth.go`): the inline
  `SELECT EXISTS(SELECT 1 FROM account_removals WHERE user_id = $1)` check
  (not a call through `deletion.*`, but the same interop concern — flagged
  in this doc's `realtime/*` Done section as "HTTP-auth parity") now binds
  `identity.LocalID(userID, as.serverID)` instead of the bare `userID`,
  restoring parity with the HTTP path's `HasAccountRemoval` ->
  `deletion.HasAccountRemoval`, which now also converts internally. Doc
  comments on both the function and the query itself updated to reflect
  the closed gap instead of describing it as deliberate/pending.

**The `GetMissingAccountRemovals` gap closure, specifically:** before this
task, `account_removals.user_id` was written bare while
`user_following.following_user_id` and `reed_allocations.author_user_id`
were already canonical (both converted in the `services.go`/`realtime/*`
passes) — so the function's two `EXISTS` subqueries could never match a
real row; a viewer following or holding allocations from a removed account
would never see the removal via catch-up. Fixed purely by making
`deletion.InsertAccountCert` write canonical `user_id` — no query changes.
Verified directly (see below): seeded a viewer who both follows a
soon-to-be-removed author and holds an allocation for that author's reed,
inserted the account removal, and asserted `GetMissingAccountRemovals`
returns exactly that removal (it would have returned an empty slice before
this fix).

**Verification performed:** scratch Postgres database
(`syrinx_deletion_scratch`) created via the existing `syrinx_db` Docker
container; `recovery_collision_test.go` was temporarily moved out of the
package directory for the duration of the run only (confirmed byte-for-byte
identical via `md5sum` before and after — `d2cce2025b455e5dbac6505c7174e4d6`
both times), then moved back. Ran a temporary `_test.go` in `package main`
exercising, against real Postgres, asserting actual stored/returned values
at every step (not just "no error"): `InitDB`; `InitServer`; two local
signups (author, viewer); `InsertReedRemoval`/`GetReedRemoval` round-trip,
asserting `reed_removals.user_id` is stored as the exact canonical
`"{userID}@{serverID}"` string and `GetReedRemoval` decodes back to the
bare form; `deletion.GetCert` called directly with explicit `serverID`;
seeded a reed + allocation + follow edge, then `InsertAccountRemoval`,
asserting `account_removals.user_id` is stored canonical;
`HasAccountRemoval`/`GetAccountRemoval` round-trip; the
`GetMissingAccountRemovals` gap-closure scenario described above, asserting
a non-empty result with the correct bare `UserID` and note;
`GetAccountRemovalWire`; `GetMissingRemovals`/`GetReedRemovalWire` for the
reed-removal catch-up path; `ReedExists` on both the `services.go` and
`realtime.DBService` copies, asserting `false` for a reed with a
`reed_removals` row present (the second gap found and fixed above). All
assertions passed. `go build ./...` passes clean. `go vet ./...` fails only
on the two known, pre-existing/expected test-fixture breakages —
`deletion/store_test.go` (newly broken by this task's signature changes,
same pattern as every prior conversion pass) and `recovery_collision_test.go`
(already broken from the `recovery/*` pass) — both deferred to Task 9,
neither touched by this task. Scratch database and temporary test file were
deleted before finishing — confirmed via `git status`.

## Not done — remaining work

**Updated caveat:** `services.go`, `recovery/*`, `realtime/*`, and
`deletion/*` are now **fully converted** (see each section's "Done" section
above) — every call-site in those files that touches an FK'd or
transitively-FK'd column now goes through the canonical-form conversion
rule, verified against real Postgres. `roles.go`/`handlers.go` and
`coverage/stats.go` are **still unconverted** and will fail at runtime
wherever they touch one of the FK'd columns without going through the
canonical-form conversion rule — this is expected, those are separate
tracked tasks below, not yet started.

204 non-`db.go` query call-sites across 15 files were inventoried (via a
dedicated Explore-agent survey) before the schema change landed. None of
them have been touched. Full original inventory (file names, line numbers,
function names) is preserved in the conversation history that produced
this doc, but line numbers will drift as edits happen — re-grep rather than
trusting stale line numbers below.

### Task list (mirrors the TaskCreate/TaskUpdate list active when this was written)

1. ~~`db.go`: identities table, FK repoint, view~~ — **DONE** (this doc).

2. **Define `IdentityID` type** (blocks everything below — do this first).
   **Not started.** Locked decision (see table above): a distinct Go type,
   not a bare `string`, for the canonical `"userID@serverID"` form —
   compiler-enforced so a bare userID can never be silently passed where
   an FK'd column needs the canonical form. Design constraints to satisfy:
   - Lives in `identity/` package (already imported by `main`, `recovery`,
     `realtime`; `deletion`/`roles` can add the import). Cannot live in
     `main` (`db.go`) because `recovery`/`realtime`/`deletion` cannot
     import package `main`.
   - Needs a constructor, e.g. `identity.LocalID(userID, serverID string)
     identity.IdentityID` (or a method on `DataService` that closes over
     `s.serverID` for the common local case — `s.localIdentity(userID)`
     was the sketch floated mid-session, still worth considering as a
     `services.go`-local convenience wrapper around the shared type).
   - Needs a parse/split helper for the reverse direction (pull bare
     `userID`/`serverID` back out of a canonical `IdentityID`), since
     several existing call sites — e.g. `identity/identity.go`'s wire
     payload builders — need the **bare** userID, not the canonical form
     (wire format is explicitly unchanged, see Locked decisions).
   - Every `DataService` (and `recovery`/`realtime`/`deletion`) function
     that currently takes `userID string` and uses it against one of the
     27 repointed FK'd columns needs its signature audited: does it need
     to become `identity.IdentityID`, or does it stay a bare `userID` and
     construct the `IdentityID` internally right before the query? Decide
     this per-function based on whether the caller already has (or can
     cheaply get) the canonical form — don't force every caller up the
     stack to construct `IdentityID` early if it doesn't need to.
   - This was the point reached when this doc revision was written —
     **no code for this exists yet.** The type needs to be designed and
     landed (with its own `go build ./...` check) before Task 3 (which is
     `services.go` proper) can start for real.

3. ~~`services.go`~~ — **DONE** (see its "Done" section above for the
   full write-up, transitive-FK discoveries, judgment calls, and
   verification). All special cases flagged below were addressed:
   - `Signup` — done in the earlier trial pass (see "Done" above).
   - `GetUserProfile` — done in the earlier trial pass.
   - `GetUserRole` — audited; needed **no change** (already correctly
     bare against the exempt `users.id`).
   - `AddPublicKey` and `checkReedTipTx` — both retargeted to
     `SELECT 1 FROM identities WHERE id = $1 FOR UPDATE`.
   - `MentionTargetValid` — reasoned through explicitly; left querying
     `users`/`account_removals` directly rather than
     `verified_identities` (behaviorally identical today, see judgment
     call #2 in the "Done" section) with a comment flagging the future
     revisit once foreign mentions are wired up.
   - `ListFederationInvitations` — both `JOIN users` now go through
     `identity_id`.
   - Also found and fixed, beyond the original flagged list: a
     bare-`u.id`-vs-canonical-column bug in `GetUserInfo` and
     `SearchUsers` (see "Done" section, judgment call #3), and several
     transitively-FK'd columns (`reed_replies`, `pending_fanout`,
     `reed_allocations.author_user_id`, `reeds_liked.author_user_id`,
     `ripples`/`ripple_responses.reed_author_id`,
     `reed_mentions.mentioning_user_id`) that the original survey's
     "27/28 direct-FK columns" framing didn't capture.

4. ~~`recovery/upsert.go` + `recovery/reeds_follows.go` + `recovery/store.go`
   + `recovery/nest.go`~~ — **DONE** (see its "Done" section above for the
   full write-up: the local-vs-remote judgment call, every function
   changed, both open design questions addressed/flagged, and
   verification). Summary: every identity in this package's current wire
   protocol is local (no cross-server subject exists in `recovery/wire.go`
   today — see the "Done" section's judgment-call writeup), so every
   function converts via `identity.LocalID(userID, serverID)` with a new
   `serverID string` parameter threaded in from `recovery.Deps.ServerID`.
   `upsertIdentity`'s row-lock retargeted to `identities`; `insertUser`
   now mints both the `identities` and `users` rows; `claimUsername`'s
   collision delete now targets `identities` (locked decision, cascades);
   `SaveReed`/`SaveFollowing`'s existence checks retargeted to
   `identities` (the actual fix for the motivating use case — verified
   end-to-end against a freshly peer-reported, never-claimed author);
   `recovery/store.go` mechanically converted; `recovery/nest.go` reviewed
   and confirmed to need no changes (pure wire-payload verification, no DB
   access). **Known consequence:** breaks compilation of the pre-existing
   `recovery_collision_test.go` fixture (`go build ./...` still passes;
   `go vet ./...`/`go test ./...` do not) — deferred to Task 9 per that
   task's own note about test-fixture repointing being a distinct
   follow-up, same as `services.go`'s conversion pass.

5. ~~**`realtime/db.go` + `realtime/auth.go`**~~ — **Done**, see "Done"
   section above. `AuthenticateWebSocket`/HTTP-auth parity verified with
   no divergence; two cross-package interop gaps with unconverted
   `deletion/*` flagged, not fixed (deliberately left bare to match
   `deletion/*`'s current on-disk storage — see Done section).

6. ~~`deletion/account.go` + `deletion/store.go`~~ — **DONE** (see its
   "Done" section above for the full write-up: writer/reader signature
   changes, the cross-package call-site fixes in `services.go` and
   `realtime/*`, the second `ReedExists` gap found beyond the task brief's
   original list, and verification). Summary: `InsertAccountCert`/
   `InsertCert` now write `account_removals.user_id`/`reed_removals.user_id`
   canonically (converting internally from a new `serverID` parameter,
   `cert.UserID` stays bare — wire-facing shape); `GetAccountCert`/`GetCert`/
   `HasAccountRemoval` query canonically the same way. Every caller in
   `services.go` (5 wrapper functions) and `realtime/*`
   (`GetMissingRemovals`, `GetReedRemovalWire`, `GetMissingAccountRemovals`,
   `GetAccountRemovalWire`, `AuthenticateWebSocket`'s inline check) updated
   to match. `GetMissingAccountRemovals`'s correctness gap closed with zero
   SQL changes (the query already assumed canonical `ar.user_id`; only the
   writer was wrong) — verified end-to-end with a viewer who follows +
   holds an allocation from a removed account. A second, previously
   unflagged instance of the same gap class found and fixed in `ReedExists`
   (both the `services.go` and `realtime.DBService` copies).

7. ~~**`roles.go` + `handlers.go`**~~ — **Done, zero code changes needed.**
   Verified directly (not delegated to an agent — small enough to check
   in-session):
   - Root bootstrap lives in `root.go` (not `handlers.go`/
     `recovery/upsert.go` — the earlier task description's search
     pointer was slightly off), specifically `exportRootIdentity`, called
     from `maybeExportRootKey`. It mints root purely by calling
     `db.Signup(...)` (already-converted `services.go`), which internally
     builds `identity.LocalID(in.UserID, s.serverID)` — so root
     automatically gets `identities.id = "1@{serverID}"`,
     `verified = true`, with no root-specific code required. Verified
     end-to-end with a scratch-DB trial exercising
     `maybeExportRootKey` → `Signup` → `requireRootUser`: confirmed
     `identities.id = "1@srvRoot01"`, `identities.verified = true`,
     `users.id` stayed bare `"1"`, `users.role = "root"`,
     `requireRootUser` finds it afterward, and a second bootstrap attempt
     correctly errors (root already exists). Scratch DB and temp test
     file deleted after; `recovery_collision_test.go` was temporarily
     relocated to run `go test .` and restored byte-for-byte (confirmed
     via `git status`) — same technique documented in `realtime/*`'s Done
     section.
   - `roles.RootUserID = "1"` stays as a Go string literal, unchanged —
     correct as-is, no edit needed (it's a comparison against the bare
     `users.id`/wire-facing userID, which is exempt from conversion, not
     against `identities.id`).
   - `handlers.go` — confirmed via `git log --oneline e1a6d8b..HEAD --
     handlers.go` that no conversion commit has touched this file, and it
     makes 129 calls into `h.services.db.*`, all against functions whose
     *external* Go signatures never changed (every `services.go`
     conversion kept bare-`userID string` params, converting internally —
     see the conversion rule). Zero handler changes needed; the "should
     be a no-op" prediction in this task's original description held.

8. ~~**`coverage/stats.go` + `recovery/nest.go`**~~ — **Done, zero code
   changes needed.** Verified directly in-session:
   - `coverage.BumpAllocationCount(ctx, tx, authorUserID, reedID, delta)`
     takes a plain string and passes it straight through to
     `WHERE user_id = $2` against `reeds.user_id` — it has no conversion
     logic of its own and needs none; correctness is entirely the
     caller's responsibility (the established bare-string-in pattern).
     Confirmed all three real call sites already pass the canonical form:
     `recovery/reeds_follows.go:132` and `realtime/db.go:469,503` all
     call it with `string(authorIdentity)` (already fixed during those
     packages' own conversion passes). `BumpActiveUsers`/`ActiveUsers`/
     `ActiveUsersTx` touch only the singleton `network_stats` table,
     which has no user reference of any kind — untouched, correctly.
   - `recovery/nest.go` re-confirmed (not just trusted from the earlier
     note) to have **zero database access anywhere** — it's pure
     signature verification and wire-payload construction
     (`Verifier.VerifySignature`, `identity.Build*Payload`, an injected
     `ServerKeyLookup` function, never a direct query). Every
     `userID`/`serverID` it touches is correctly bare, since these values
     feed `identity/identity.go`'s wire-payload builders, which must
     never receive the canonical `userID@serverID` form.

9. ~~**Build, vet, fix compile errors, then runtime-verify (test-fixture
   pass)**~~ — **DONE.** `go build ./...` and `go vet ./...` are both
   clean across the entire repo. `go test ./...` passes everywhere except
   three pre-existing, refactor-unrelated failures in `syrinx/invites`
   (confirmed identical on the unmodified `canonical` baseline before any
   fixture changes — see "Done" write-up below) and four tests
   deliberately skipped because they exercise a real, non-test bug in
   `invites/store.go` that this task was not permitted to fix (test-only
   scope). See "Done" section below for the full file-by-file write-up.

### Task 9 — test-fixture pass (landed, verified)

**Scope:** every `_test.go` file in the repo, fixed to compile against and
correctly exercise the post-refactor schema/signatures. No non-test `.go`
file was touched.

**Files touched, and why:**

- **`recovery_collision_test.go`** (repo root, `package main`) — signature
  mismatch: `recovery.SavePeerIdentity`/`SaveOwnIdentity` calls updated to
  pass `serverID` (`"test"`) as the new 3rd positional arg. Schema mismatch:
  `ensureRecoveryCollisionSchema`'s `user_keys`/`unclaimed_accounts`/
  `ongoing_recoveries`/`pending_follows`/`user_devices` tables repointed
  from `REFERENCES users(id)` to `REFERENCES identities(id)` (the base
  `identities` table itself comes from `signup_invite_test.go`'s schema,
  which this file extends). One assertion (`user_keys WHERE owner = $1`)
  fixed from a bare to a canonical (`"holder2@test"`) comparison — was a
  latent always-false-match bug once `owner` started storing canonical
  values.
- **`signup_invite_test.go`** (repo root, `package main`) — schema
  mismatch: shared base schema (`ensureSignupInviteSchema`, used by this
  file, `recovery_collision_test.go`, `root_test.go`, and
  `handlers_signup_gate_test.go`) gained the `identities` table and
  `users.identity_id`/`invites.created_by`/`invites.claimed_by`/
  `user_keys.owner`/`user_key_revocations.owner`/`account_removals.user_id`/
  `user_following`/`user_followers` FK repoints, mirroring `db.go`. Also:
  **found a real, non-test bug** (since fixed — see the follow-up
  subsection below, "`invites/store.go` has now been converted") —
  `invites/store.go` (a package never touched by any prior conversion
  pass) still wrote/read bare userIDs against
  `invites.created_by`/`claimed_by`, which `db.go`'s real schema FKs to
  `identities(id)`. `Store.Insert` hit a live FK violation
  (`invites_created_by_fkey`) the instant a real, non-bootstrap user tried
  to create an invite. `TestSignup_ConsumeInvite`, `TestSignup_OpenValidToken`,
  `TestSignup_AdminInviteGrantsAdminRole` were `t.Skip`'d with a comment
  explaining why — fixing `invites/store.go` was out of this specific
  agent task's scope (test fixtures only, no non-test source changes
  permitted); a follow-up pass in the same session converted it and
  un-skipped all four tests, which now pass.
- **`handlers_signup_gate_test.go`** (repo root) — same root cause as
  above (shares `openSignupTestDB`): `TestCheckUsername_InviteModeRequiresValidInvite`
  also called `invites.Store.Insert` with a bare creator id and hit the
  same FK violation; un-skipped and passing after the `invites/store.go`
  fix.
- **`deletion/store_test.go`** (`package deletion`) — signature mismatch:
  `InsertCert`/`InsertAccountCert`/`GetCert`/`GetAccountCert`/
  `HasAccountRemoval` calls updated to pass the new `serverID` parameter
  (`testServerID = "test-srv"`). Schema mismatch: added `identities` table,
  `users.identity_id`, repointed `user_keys.owner`/`reed_removals.user_id`/
  `account_removals.user_id` to `REFERENCES identities(id)`. `seedUser`/
  `seedUserKey` now mint a matching `identities` row and write canonical
  `owner`/`identity_id` values; cleanup `DELETE` statements updated to
  match.
- **`mentions_integration_test.go`** (repo root) — schema mismatch only
  (already called correctly-converted `services.go` functions, so no
  signature changes needed): added `identities` table, `users.identity_id`,
  repointed `account_removals.user_id`/`reeds.user_id`/
  `reed_allocations.holder_user_id`/`reed_mentions.mentioned_user_id`/
  `reed_removals.user_id` to `identities(id)`. `seedMentionUser` mints a
  matching `identities` row. Fixed three assertions that were comparing
  against bare userIDs where the column now stores canonical form
  (`reed_mentions.mentioned_user_id` in `TestCreateReed_MentionsIndexed`
  and `TestDeleteMentionsByAuthor_ClearsBothSides`; `account_removals.user_id`
  in `TestMentionTargetValid` and `TestSearchUsers`, both of which join
  through `u.identity_id`) — these were the "bare-vs-canonical always-false
  comparison" bug class flagged repeatedly elsewhere in this doc; without
  the fix the assertions would have silently tested the wrong thing (or,
  in `TestCreateReed_MentionOfNonexistentUserRejected`'s case, still
  passed correctly since it asserts an FK-violation error either way).
- **`reed_tip_check_test.go`** (repo root) — schema mismatch only, found
  via a full `go test ./...` run rather than the `grep`-based candidate
  list (it doesn't do a literal `INSERT INTO users`, so it was missed by
  the initial sweep — reuses `openMentionsTestDB`, already fixed above).
  One direct `INSERT INTO reed_removals (..., user_id) VALUES (..., 'alice')`
  updated to the canonical `'alice@testserver'` — was a live FK violation
  once `mentions_integration_test.go`'s schema fix landed.
- **`ripples_test.go`** (repo root) — schema mismatch: added `identities`
  table, `users.identity_id`, repointed `user_keys.owner`/
  `user_key_revocations.owner`/`reeds.user_id`/`reed_removals.user_id`/
  `account_removals.user_id`/`reed_echoes.echoing_user_id`/
  `ripple_responses.user_id` to `identities(id)`. Every
  `&DataService{db: db}` literal (15 occurrences) gained
  `serverID: ripplesTestServerID` — `PostRipple`/`GetRipple`/`ListRipples`/
  `SoftDeleteRipple` all convert internally via `s.serverID`, and an unset
  `serverID` would silently build the malformed `"user@"` form. All seed
  helpers (`insertRipplesTestUser`, `insertRipplesTestReed`,
  `markReedBlankEcho`, `insertReedRemoval`, `insertAccountRemoval`,
  `newRippleTestKey`) mint/write canonical identities. **Found and fixed a
  real silently-broken assertion**: `TestSoftDeleteRipple_OwnerSucceeds`
  queried `ripples.expires_at WHERE reed_author_id = $1` with a bare
  userID; since that now matches zero rows, `expiryBefore`/`expiryAfter`
  both silently stayed at their Go zero-value and the equality assertion
  passed for a completely wrong reason (never read real data). Fixed by
  using the canonical form and asserting the `Scan` error explicitly.
- **`ripples_handlers_test.go`** (repo root) — not in the original
  known-broken list, found via a full `go test ./...` run: shares
  `ripples_test.go`'s fixtures via `openRipplesTestDB`/
  `insertRipplesTestUser`/etc., and all 28 `&DataService{db: db}` literals
  needed the same `serverID: ripplesTestServerID` fix as `ripples_test.go`.
- **`federation_test.go`** (repo root) — schema mismatch:
  `ensureFederationTestSchema` gained `identities` and `users.identity_id`;
  `federation_invitation.created_by`/`reviewed_by` repointed to
  `identities(id)` (`ListFederationInvitations` joins `users` through
  `identity_id`). `seedFederationUser`'s signature changed from taking a
  bare `*sql.DB` to taking `*DataService` — it needs `ds.serverID` (a
  per-test randomly-generated value from `InitServer`, not a fixed
  literal) to mint the matching `identities` row and construct the
  canonical form the FK requires. All 7 call sites across this file and
  `federation_handshake_test.go` updated to pass a `*DataService` instead
  of `.db`. `TestRevokeFederationInvitation_NewOnly`'s `reviewed_by`
  assertion fixed to compare against the canonical form.
- **`federation_handshake_test.go`** (repo root) — no schema of its own
  (reuses `testFederationHandlers`, fixed above); only needed its 6
  `seedFederationUser` call sites updated from `a.ds.db`/`b.ds.db` to
  `a.ds`/`b.ds` to match that function's new signature.
- **`follow_counts_test.go`** (repo root) — schema mismatch: added
  `identities`, `users.identity_id`, repointed `reeds.user_id`/
  `reed_removals.user_id`/`account_removals.user_id`/
  `user_followers.*`/`user_following.*` to `identities(id)`.
  `insertFollowCountTestUser` mints a matching `identities` row; direct
  `user_followers`/`user_following`/`account_removals` inserts in both
  tests converted to canonical form (`GetUserInfo` joins through
  `u.identity_id`, per its `services.go` fix documented above in this
  doc).
- **`reply_counts_test.go`** (repo root) — schema mismatch (added
  `identities`, `users.identity_id`, repointed `reeds.user_id`/
  `reed_removals.user_id`/`account_removals.user_id`) plus a genuine
  bare-vs-canonical bug in the test body itself: `insertReplyTx` takes an
  already-canonical `identity.IdentityID` for the replying user and
  derives the parent's canonical form from `parent.ServerID`
  (`identity.RemoteID`) — the test was passing the bare Go string literal
  `"alice"` (which compiles fine, since `IdentityID` is a defined string
  type an untyped constant satisfies silently) and a `ReedRef` with an
  **empty** `ServerID`, producing the malformed `"alice@"` instead of
  `"alice@testserver"`. This didn't fail the build or immediately error at
  runtime — it would have silently written mismatched rows that
  `GetSubtreeReplyCount`'s own `identity.LocalID(userID, s.serverID)`
  lookup could never match. Fixed by building a real
  `identity.LocalID("alice", "testserver")` value and setting `ServerID`
  on every `ReedRef` used in the test.
- **`devices_test.go`** (repo root) — schema mismatch: added `identities`,
  `users.identity_id`, repointed `user_devices.user_id` to
  `identities(id)`. `deviceTestSvc` gained `serverID: devicesTestServerID`
  (was previously unset, which — same landmine as `ripples_test.go` —
  would have silently built the malformed `"u1@"` form via
  `BindDeviceTx`'s internal `identity.LocalID` call). `insertDeviceTestUser`
  mints a matching `identities` row; one direct
  `user_devices WHERE user_id = $1` query fixed to canonical form.

**Files reviewed and found to need NO changes** (verified by reading, not
assumed): `invites/store_test.go` — `invites/store.go` itself is
unconverted (see the bug noted above), so this test's bare-to-bare ad hoc
schema already correctly matches the package's current (unconverted, but
internally self-consistent) behavior; changing it to use `identities`
would be scope creep unrelated to making tests pass against real code.
`recovery/upsert_test.go` is an empty stub (`package recovery` only, no
test functions). `services_test.go`, `root_test.go` (uses the shared
`signup_invite_test.go` helper, needed no changes of its own),
`account_recovery_test.go`, `spa_handler_test.go`, `mentions_test.go`,
every `recovery/*_test.go` file except the ones listed above, and every
file under `identity/`, `crypto/`, `signing/`, `roles/`, `secret/`,
`encoding/`, `observability/` do not touch an FK'd column or a
converted-signature function.

**Pre-existing, refactor-unrelated failure found and deliberately left
unfixed:** `syrinx/invites`' `TestCreate_Closed`, `TestStatus_ClaimedBy`,
`TestRevokeAndCheck` (all in `invites/handlers_test.go`) fail identically
on the unmodified `canonical` baseline (verified via `git stash` + rerun
before touching any fixture) — pure invite-mode/status-handling logic
bugs with zero relationship to `identities`/FK schema. Not touched, per
the task brief's explicit instruction to document rather than fix
unrelated pre-existing failures.

**Follow-up (same session, after the agent above stopped at the test-fixture
scope boundary): `invites/store.go` has now been converted.** The agent
above correctly found and refused to fix this non-test bug (test-fixture
pass scope boundary) — a human/coordinator pass then converted it directly,
since it's a live production bug (invite creation was completely broken)
rather than a test-only concern, and it's small (one file, ~10 query
call-sites, all local-only per the same "invites are always created/claimed
by a local user" reasoning as everything else in this refactor):

- `invites.Store` gained a `ServerID string` field. Every method
  (`Insert`, `CountByCreator`, `GetByCreatorAndID`, `GetPendingInvite`/
  `GetPendingInviteTx`, `MarkClaimed`, `Revoke`) converts its bare userID
  param(s) via `identity.LocalID(userID, s.ServerID)` right before
  touching `invites.created_by`/`claimed_by`, the same "bare-string-in,
  convert internally" pattern as every other converted package.
  `GetByTokenHash` needs no conversion (token_hash is globally unique, no
  creatorID in scope). `Invite.CreatedBy`/`.ClaimedBy` stay bare
  (wire-facing fields) — `scanInvite` now scans `created_by` as
  `identity.IdentityID` and decodes back via `.UserID()`, same "scan
  canonical, decode to bare" pattern as `services.go`'s `GetPublicKey`/
  `GetReed`.
- Both real construction sites fixed: `services.go`'s
  `NewDataService`/`InitServer` (the `DataService.invites` field) now
  sets `s.invites.ServerID = id` at both points `InitServer` learns the
  server's own id (fresh-mint and existing-row branches); `main.go`'s
  `invites.RegisterRoutes` call now passes
  `&invites.Store{DB: db, ServerID: dataService.GetServerID()}`.
- **Found and fixed a related test-infrastructure footgun while verifying
  this**: several `_test.go` files (`signup_invite_test.go`) construct a
  `DataService` and set `svc.serverID = "srv"` **directly**, bypassing
  `InitServer` entirely — which means `s.invites.ServerID` was never set
  to match, so `identity.LocalID(userID, "")` (empty serverID) never
  matched the canonical `"userID@srv"` form the invite row actually
  stored, silently breaking `MarkClaimed`'s `WHERE` clause (0 rows
  affected → `ErrInvalidInvite`, NOT the `Status() != "pending"` check
  that error message misleadingly suggests — confirmed by direct
  reproduction, this cost real debugging time and is worth flagging for
  future test authors). Fixed by adding
  `DataService.setServerIDForTest(id)` (sets both `serverID` and
  `s.invites.ServerID` together) and switching every affected test call
  site to use it instead of the raw field write.
- Two more test-package-local schema duplicates were found and fixed the
  same way `signup_invite_test.go`'s and `deletion/store_test.go`'s
  already had been: `invites/store_test.go`'s `ensureInviteSchema` (added
  a minimal `servers`/`identities` fixture, updated `users`/`invites` FKs,
  added a `testServerID` constant and updated `seedUserWithRole` to mint
  a matching `identities` row) and `invites/handlers_test.go`'s
  `testDeps` (now passes `ServerID: testServerID` to match).
- All 4 previously-`t.Skip`'d tests (`TestSignup_ConsumeInvite`,
  `TestSignup_OpenValidToken`, `TestSignup_AdminInviteGrantsAdminRole`,
  `TestCheckUsername_InviteModeRequiresValidInvite`) un-skipped and now
  pass.

**Verification performed:** `go build ./...` clean. `go vet ./...` clean
across the entire repo (previously failing on exactly the two known
fixtures documented in this doc's "How to resume" section — both now
fixed, plus the `invites/store.go` gap closed on top). `go test ./...` run
against the live `syrinx_db` Postgres 17 container (confirmed via `.env`/
`.env.example` this is the pattern the existing suite already uses:
`testdb_test.go`'s `newTestDatabase` creates a fresh, uniquely-named
scratch database per test via the admin connection to that same container,
and drops it on cleanup — not the `syrinx` dev database itself, and not a
new pattern invented for this task). Every package passes except
`syrinx/invites`'s three pre-existing, confirmed-unrelated failures (see
above — re-verified after the `invites/store.go` fix landed, still
identical: `TestCreate_Closed`/`TestStatus_ClaimedBy`/`TestRevokeAndCheck`
fail on `mux.Vars` route-matching, nothing to do with FKs/schema). Root
package: 114 tests passing, 0 failing, 0 skipped (the 4 invite-related
skips from the test-fixture pass are now real passing tests).

10. **Frontend / IndexedDB (`spa/src/lib/services/db.ts` and friends) —
    not yet a numbered task in the original 8, added when the user asked
    "do we also need to change the frontend's IndexedDB?" mid-session.**
    Short answer: **yes**, this is a real, separate gap — see "Frontend
    gap" section below for the full survey. Summary: every IndexedDB
    store that references "a user" (`users`, `usersInfo`, `following`/
    `unfollow`/`pendingFollows`, `reeds`'s compound `[userID, id]` key,
    `tags`' embedded refs, `removedAccounts`) is keyed by a **bare**
    `userID` string with no server component — the same structural
    problem the backend `identities` table just fixed, unaddressed
    client-side. The app already has a `userID@serverID` *display*
    convention (mentions, reed refs, identicons) but it never made it
    into the IndexedDB *storage* layer's keys/indexes. Deliberately
    sequenced **after** backend work stabilizes (tasks 2–9) rather than
    done in parallel, because the exact wire shape the client needs to
    consume (does the API start returning `identity`/`identityID`
    alongside or instead of bare `userID`? on which endpoints?) isn't
    decided yet — designing the client store schema before that would be
    guessing. IndexedDB store version is already at 9 with precedent for
    a keyPath migration (v8, on `reeds`) — same mechanism applies here,
    it is real migration work, not just a version bump (per `db.ts`'s own
    documented warning about `keyPath` changes needing explicit
    read-all/delete/recreate, not `ensureStore`).

## Frontend gap — full survey findings

Researched via an Explore agent against `spa/src` mid-session (not yet
acted on — recorded here so it isn't lost). Do not treat line numbers/exact
counts as verified against current code; re-check before acting.

**IndexedDB store inventory** (`spa/src/lib/services/db.ts`, schema
version 9):

| Store | Key shape | References a user? |
|---|---|---|
| `users` (profile cache) | `id` (bare userID) | yes — subject |
| `usersInfo` | `id` (bare userID) | yes — subject |
| `following` / `unfollow` / `pendingFollows` | `userId` (bare) | yes — target |
| `publicKeys` / `privateKeys` / `revocations` | `fingerprint` | indirectly only (fingerprint is server-independent, fine as-is) |
| `reeds` | compound `[userID, id]`; indexes on `userID` and `serverSignature.timestamp` | yes — author, bare `userID` as half the compound key |
| `tags` | `tagName`; value holds embedded `{userID, id}[]` refs | yes — bare `userID` in ref list |
| `removedAccounts` | `userID` (bare) | yes — subject |
| `reedRequests` | `requestId` (MD5 of `serverId`+`authorId`+`reedId`); value has separate `serverId`/`authorId` fields | yes, and this one already carries a server-qualified shape (see below) |

**Every store keyed on "a user" today uses a bare userID string.** None
composes `userID@serverID` as the actual key. `db.ts` (~lines 19–22 at
survey time) explicitly documents `reeds`'s key as `[userID, id]` with no
server component.

**What already anticipates federation client-side** (so this is not a
from-scratch concept for the client):
- `ServerSignature.serverID` (`types/api.ts`) is on every signed wire
  record already — but it's always *this server's own id* today (the
  countersigning server), not a foreign author's.
- `userID@serverID` mention syntax is fully parsed/rendered
  (`utils/reedMarkdown.ts`, `utils/reedRef.ts`, `utils/identicon.ts`).
- `reedRequestsRepository` / `serverConnection.requestReedContent`
  already carry a `serverId` field alongside `authorId`/`reedId` (as a
  value field, not as part of the `reedRequests` store's actual IDB key).
- `api.ts` has federation-handshake API client plumbing already
  (`listFederationInvitations`, `attemptFederationConnection`, etc.).
- Own-account `serverId` is tracked globally (`localStorage['serverId']`,
  a `serverInfo` store) but only as a single value for "this server," never
  per-cached-peer.

**Recovery client code** (`accountRecovery.ts`, `recoveryProgress.ts`,
`recoveryRun.ts`, `recoveryKeyNest.ts`, `recoveryHoldings.ts`) is
currently strict single-server, own-account-only:
- `accountRecovery.assertServerMatch` actively **rejects** restoring a
  backup onto a different server.
- Recovery ledger/progress keys, follow-page lists, and key-nest maps are
  all keyed by bare `userId`/fingerprint throughout.
- `recoveryHoldings.reportRecoveryReed` is explicitly documented as
  "only ever reports the current device's own account's reeds" — no
  peer/foreign-server case exists in this code path at all.

**Client → server API calls using bare userID** (`services/api.ts`):
`getUserProfile(userId)`, `followUser`/`unfollowUser(targetUserId)`,
`getPublicKey(userID, fingerprint)` all pass a bare userID in the URL path,
no server qualifier. Consistent with the rest: server-qualification exists
today only for *this* server's own id, never for referencing an arbitrary
(possibly remote) user.

**Assessment (from the survey, not yet a design decision):** the
`userID@serverID` string format is already well-established as a display/
reference convention, so extending it into IndexedDB keys (e.g. `reeds`
becoming keyed by `[identity, id]`, `following`/`users` keyed by an
`identity` string) is a natural extension of an existing pattern, not a
new concept to introduce — but it's real migration work on the client
(explicit store recreation, not just bumping the DB version number), and
it depends on the backend wire format actually being finalized first
(hence sequenced last, as task 10 above).

## Open design questions not yet resolved

These were flagged in `ANALYSIS_identity_indirection.md` and are **still
open** — they matter once Task 3/4 actually start writing
insert/conflict-resolution logic for provisional (pre-handshake) remote
identities:

1. **Pre-handshake uniqueness.** `UNIQUE (remote_user_id, server_id)` on
   `identities` doesn't dedupe rows where `server_id IS NULL` (Postgres
   treats NULLs as distinct in a unique index). Multiple peers relaying
   claims about the same not-yet-verified remote user can produce
   multiple provisional rows before a decision is made about whether/how
   to dedupe them (e.g. by claimed fingerprint).
2. **Conflicting provisional claims.** No invite-style "who did we
   actually address this to" check exists pre-handshake (unlike
   federation 02's fingerprint-matching). Needs an explicit resolution
   rule before this ships for real traffic, not just recovery hydration.
3. **Row update-in-place mechanics** on handshake completion — the design
   intent is clear (update the existing `identities` row's `server_id`/
   `verified` fields, never insert a new row or rewrite the id), but no
   code implementing that transition exists yet. This is presumably part
   of Task 3 (`services.go`) or a new federation-handshake-completion
   task, not yet explicitly assigned to one of the tracked tasks above —
   flag this gap if resuming.

## How to resume

**The backend is fully done, including the `invites/store.go` gap.**
Every package task (`IdentityID`, `services.go`, `recovery/*`,
`realtime/*`, `deletion/*`, `roles.go`/`handlers.go`,
`coverage/stats.go`/`recovery/nest.go`, Task 9's test-fixture pass, and the
`invites/store.go` follow-up landed in the same session) is complete and
verified: `go build ./...` clean, `go vet ./...` clean across the whole
repo, `go test ./...` passing everywhere except the three pre-existing/
unrelated `syrinx/invites` handler failures documented in Task 9's "Done"
section above (confirmed identical on the unmodified `canonical` baseline
both before and after the `invites/store.go` fix — nothing to do with this
refactor). **The only remaining work is Task 10 (Frontend/IndexedDB)** —
see its section above ("Frontend gap — full survey findings") for the
full survey of what needs to change in `spa/src/lib/services/db.ts` and
friends. The backend wire format is now stable enough to start it.

**Historical context for anyone auditing the commit trail:** everything
through `deletion/*` (Tasks 1–6) was committed on `canonical` before this
doc revision — `db.go`, `identity/identity_id.go`(+test), `services.go`,
`recovery/upsert.go`, `recovery/reeds_follows.go`, `recovery/store.go`,
`recovery/handlers.go`, `recovery/identity.go`, `realtime/db.go`,
`realtime/auth.go`, `realtime/service.go`, `deletion/account.go`,
`deletion/store.go`, and `main.go` (for `serverID` threading into
`realtime.NewService`). Tasks 7–8 (`roles.go`/`handlers.go`,
`coverage/stats.go`/`recovery/nest.go`) needed zero code changes, verified
directly. Task 9 (this revision) touched `_test.go` files (see its "Done"
section for the full list) **plus** `invites/store.go`, `services.go`
(`DataService.invites.ServerID` wiring + the `setServerIDForTest` test
helper), and `main.go` (`invites.Store{ServerID: ...}` at the
`invites.RegisterRoutes` call site) — a small non-test source fix, done
directly rather than via a worktree agent, since the test-fixture agent
correctly refused to touch non-test source per its scope boundary and
flagged the gap instead of silently leaving it.

**Process note for whoever runs the next agent pass:** if delegating to
an isolated worktree agent, require it to commit before finishing (not
just leave changes uncommitted for the coordinator to pick up) — a
worktree can be cleaned up between an agent's completion and the
coordinator's review, and uncommitted work in it is not guaranteed to
survive that gap. This bit the `realtime/*` task once (first attempt's
verified work was lost this way) before the retry was told to commit as a
hard requirement.
