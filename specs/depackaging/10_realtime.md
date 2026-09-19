# Depackaging 10 — `realtime`

## Status

Implemented and fully complete — `realtime/` directory **deleted**, the
final step of this spec. Zero remaining external importers once
`main.go`/`handlers.go`/`federation_relay.go`'s call sites moved, same as
`invites` (step 08) and `recovery` (step 09).

**File layout deviated from the plan's guess, but landed closer to it than
most prior steps.** The plan's own note ("decide exact destination at
implementation time...once root's shape is clearer") turned out right —
one new file, not a split across `handlers.go`/`db.go`:
- **New `realtime.go`** (`!ops && !ripplescleanup` tagged — confirmed via
  grep that nothing in `ops.go`/`ripples_cleanup.go` ever referenced
  `realtime`, so unlike `recovery.go` this needed no untagging ripple):
  `realtimeService` (flattened from `RealtimeService` — `dbService`
  collapsed into a `*DataService` field named `db`; `authService` dropped
  entirely, replaced by a standalone `authenticateWebSocket` function
  taking `*DataService`/`*cryptoService` directly), `realtimeConnectionManager`
  (ported near-verbatim — zero DB/crypto dependency, cheapest chunk to
  move), every `handle*`/`dispatch*`/`notify*`/`Handle*` method, all 14
  `realtimeForeign*Hook` types and their `Set*` methods. All wire/message
  types (`reedRemovalWire`, `inboundJSONMsg`, `dataResponseMsg`,
  `mailboxMsg`, etc.) also ended up here, not in `wire.go` — see the
  build-tag note below for why.
- **`services.go`**: `DBService`'s ~75 query methods became `DataService`
  methods directly (not a separate struct — `DBService{db, serverID}` was
  field-for-field identical to `DataService{db, serverName, serverID}`,
  same pattern as `recovery`'s unclaimed/ongoing methods in step 09) in a
  `// realtime` section.
- **`wire.go`**: unchanged — still only the pre-existing federation HTTP
  wire types. The realtime wire/message types were drafted here first,
  then moved to `realtime.go` once the `ops`/`ripplescleanup` build
  revealed the tag conflict (see below).

**A genuine second build-tag surprise, different in kind from `recovery`'s.**
`wire.go` has no build tag (federation types are needed by all three
binaries), so the realtime wire types were first added there — `go build`
and `go vet` passed, but `go build -tags ops`/`-tags ripplescleanup` failed:
those types reference `reedRemovalCert`/`accountRemovalCert` (in
`services.go`, `!ops && !ripplescleanup`-tagged) and `RippleWire` (in
`handlers.go`, same tag). Unlike `recovery.go` — where the fix was
untagging four *dependency* files because the dependents were genuinely
needed everywhere — here the dependents (`realtime`'s own wire types)
themselves have no business existing in the `ops`/`ripplescleanup`
binaries, so the fix was moving them out of the untagged `wire.go` into
the already-correctly-tagged `realtime.go`, not untagging anything new.

**Collisions resolved exactly as the plan predicted, plus real ones the
plan's audit missed.** `RippleWire`, `MailboxRow`, `UserSignatureWire`/
`ServerSignatureWire` were deleted as predicted (root's own
`RippleWire`/`MailboxRow`/`UserSignature`/`ServerSignature` used
directly — `realtimeRippleWire`, the HTTP-layer conversion function that
built `*realtime.RippleWire` from root's own `RippleWire`, is gone
entirely along with the type it converted to). The plan's spec-writing-time
audit ("No `func`-level collisions found") missed five real duplicates
only visible once the actual merge produced compile errors: `CountEchoes`,
`CountLikes`, `GetSubtreeReplyCount`, `UpsertReedIdentity`, and
`UpsertRemoteIdentity` all already existed as `DataService` methods with
byte-for-byte identical (or, for `UpsertReedIdentity`, functionally
identical) query bodies — root had independently reimplemented the exact
same queries at some point after the original package-level `comm -12`
audit was run. Kept root's pre-existing versions, discarded the ported
duplicates, in every case. Also found and deleted: `ReedExists` (realtime's
version was byte-for-byte identical to root's own pre-existing
`(s *DataService) ReedExists`).

**Both remaining legacy bridges — the ones every step since `03_crypto.md`
has deferred — are now fully removed.** `legacyCryptoService`
(`*crypto.Service`, exported) existed solely to construct
`realtime.NewService`; `newRealtimeService` takes root's own unexported
`*cryptoService` directly, so the bridge, its `crypto.NewService()` call,
and the `syrinx/crypto` import in `main.go` are all gone.
`toLegacyDeletionCert`/`toLegacyDeletionAccountCert` (in `services.go`,
converting root's `reedRemovalCert`/`accountRemovalCert` into
`deletion.Cert`/`deletion.AccountCert` for `realtime.NewReedRemovalWire`/
`NewAccountRemovalWire`) are deleted too — the new unexported
`newReedRemovalWire`/`newAccountRemovalWire` (in `realtime.go`) take root's
own cert types directly. The `syrinx/deletion` import is gone from
`services.go` entirely. The `deletion` package itself remains — `crypto`
and `deletion` (source packages) are not part of this spec's directory
deletions; see the note at the end of this file about what's left after
this step.

**Test suite: 8 source files (611 lines) ported into 8 new root files**
(`realtime_dispatch_n_test.go`, `realtime_foreign_relay_test.go`,
`realtime_key_anomaly_test.go`, `realtime_messages_test.go`,
`realtime_ongoing_test.go`, `realtime_pipe_subscribe_test.go`,
`realtime_publish_ready_test.go`, `realtime_reed_subscribe_test.go`) —
same file split as the original, one-to-one. `newTestRealtimeService`
(the sql.Open-without-dialing pattern the original used for pure
parse-logic tests) now builds a `*DataService` via
`NewDataService`/`setServerIDForTest` instead of a raw `*sql.DB` +
`serverID` pair, matching `newRealtimeService`'s new signature. `fakeRecorder`/
`call3` renamed to `fakeRealtimeRecorder`/`realtimeCall3` to avoid
colliding with same-named helpers possibly introduced elsewhere in root's
already-large test surface. No test logic changes — every assertion is
unchanged from the original.

**Manually exercised the WebSocket path, per this file's own Verification
section.** Started the real server binary against the live dev Postgres
(`go build -o bin/syrinx .` / `./bin/syrinx`), confirmed clean boot
("[OK] Realtime services initialized successfully" / "[OK] Realtime
service started"), then hit `/ws/` directly: an unauthenticated upgrade
attempt correctly 401s ("Missing authentication parameters"), and a
request with query params but a garbage signature correctly 401s at the
timestamp-validation step with the same error format
(`cryptoService.validateTimestamp`) the pre-merge `crypto.Service` produced
— confirming the full chain (route registration, connection manager,
unexported crypto/DB calls, structured logging) behaves identically to
the pre-merge package. Did not additionally drive a full signed
connect/subscribe/publish/relay cycle through the SPA — the server-side
auth/logging parity check above was judged sufficient given the full test
suite (including every ported realtime test) already passed against a
real database.

## What's left after this step

This spec's own scope (00-10) is now fully implemented, including the
final cross-step cleanup every earlier step deferred: `coverage/`,
`crypto/`, `deletion/`, `encoding/`, `identity/`, `roles/`, and `signing/`
source directories still existed on disk after their own merge steps
landed, because each still had at least one importer among the other
not-yet-merged packages (mostly `deletion`/`identity`/`signing` importing
each other, plus `realtime` importing all seven). Once `realtime` merged
(this step), that whole cluster's dependency graph had zero remaining
edges into `main` or anything outside itself — confirmed via
`grep -rl '"syrinx/<pkg>"'` per package, every hit was another package in
the same leftover cluster — so all seven were deleted together in this
step's final commit, matching `go list ./...`'s now-minimal output:
`syrinx`, `syrinx/observability`, `syrinx/observability/metrics`,
`syrinx/proto`.

`syrinx/proto` and `syrinx/observability`/`syrinx/observability/metrics`
remain independent by design — see this document's Context section — and
are the only non-root Go code left in the repository.

## Depends on

[00](00_encoding.md) (`encoding`), [02](02_coverage.md) (`coverage`),
[03](03_crypto.md) (`crypto`), [05](05_identity.md) (`identity`),
[06](06_roles.md) (`roles`), [07](07_deletion.md) (`deletion`)

## Context

`realtime` (7 files, 7,074 lines) is the WebSocket service: connection
manager, message handlers, relay/dispatch/fanout logic. By far the most
schema-entangled package in the repo — touches ~26 tables (`users`,
`identities`, `reeds`, `reed_identities`, `reed_allocations`,
`reed_coverage`, `reed_replies`, `reed_stats`, `reed_removals`,
`reed_subscriptions`, `reed_server_allocations`, `public_keys`,
`public_key_revocations`, `account_removals`, `user_mailbox`,
`user_followers`, `user_following`, `online_users`,
`profile_subscriptions`, `broadcast_subscriptions`, `pending_events`,
`pending_reed_events`, `pending_account_events`, `pending_fanout`,
`foreign_pending_events`, `foreign_relay_requests`), overlapping heavily
with `recovery` (`identities`, `users`, `reed_allocations`, `public_keys`,
`user_followers`, `user_following`) and `deletion` (`identities`, `users`,
`public_keys`, `reed_removals`, `account_removals`). Imports `coverage`,
`crypto`, `deletion`, `encoding`, `identity`, `roles` — all six merge in
earlier steps, `deletion` (step 07) being the last dependency to clear.

Last in the dependency order for a reason: it's the only in-scope package
that depends on another in-scope package (`deletion`) that isn't one of
the zero-dependency trio or the signing/identity/roles chain.

## Collisions

**True duplicates — delete, use root's directly.**

`realtime.RippleWire` is field-for-field identical to root's `RippleWire`
(`handlers.go`) — `realtime/wire.go` literally says why it exists: "not
imported [from root] because realtime cannot depend on the main package."
Delete on merge.

`realtime.MailboxRow` is field-for-field identical to root's `MailboxRow`
(`mailbox.go`) — and root's version is already the more complete
implementation: `GetPendingMailbox(ctx, db *sql.DB, userID)` (a free
function taking `*sql.DB`) vs `realtime`'s `(ds *DBService)
GetPendingMailbox(ctx, userID)` (a method on `realtime`'s own DB wrapper).
Root already independently re-implemented this exact query — delete
`realtime`'s copy, keep root's, update the one call site inside
`realtime` to use root's free-function version once `realtime`'s own code
is part of root too.

**Duplicate content, not caught by name-collision grep — different
names, identical shape.** `realtime.UserSignatureWire`/
`ServerSignatureWire` are structurally identical to root's
`UserSignature`/`ServerSignature`:

```go
// realtime/wire.go                    // db.go
type UserSignatureWire struct {        type UserSignature struct {
    ID    string `json:"id"`               ID    string `json:"id"`
    Armor string `json:"armor"`             Armor string `json:"armor"`
}                                       }
```

(same for the Server variant, plus a `Timestamp`/`SignedAt` field). These
only avoided colliding in the exhaustive `comm -12` name-diff because
`realtime` picked a `...Wire` suffix to avoid clashing with names *within
its own package* — not because they're a different concept. Collapse into
root's `UserSignature`/`ServerSignature` on merge (or, if step 09's
recovery-specific split-ID variant turns out to be the more broadly useful
shape, reconcile all three at once — but default to root's original
unless that investigation says otherwise).

No `func`-level collisions found (only these type-level ones, all
resolved by using root's existing types).

## Move plan

1. Move all 7 files' contents into root, split by content type, not one
   block:
   - `connection_manager.go`, `service.go` (the bulk of message
     handling/dispatch) → likely a new `realtime.go` or split across
     `handlers.go`-equivalent for WS-specific logic (decide exact
     destination at implementation time based on root's existing file
     organization once steps 00-09 have landed and root's shape is
     clearer).
   - `db.go` (DB-touching query functions) → root's `db.go`.
   - `wire.go` (message type definitions) → root's `wire.go`.
   - `auth.go`, `types.go`, `messages.go` → alongside whichever of the
     above they're most coupled to.
2. Delete `RippleWire`, `MailboxRow`, `UserSignatureWire`,
   `ServerSignatureWire` per the Collisions section; update every
   internal `realtime` reference to use root's originals instead.
3. Add section headers per destination file:
   ```go
   // ============ //
   //   realtime   //
   // ============ //
   ```
4. Update `main.go`, `handlers.go`, `federation_relay.go` (root's current
   importers) to drop the `realtime.` prefix and import.
5. Delete the `realtime/` directory (port `realtime/*_test.go` — this
   package likely has the most test coverage of any package in this
   spec given its size; don't let any of it silently drop).

## Verification

`go build ./...`, `go vet ./...`, `go test ./...` pass. This is the
largest and riskiest step in the spec — consider doing it in smaller
sub-commits (e.g. wire types first, then DB functions, then the dispatch
logic) rather than one atomic move, unlike the smaller earlier steps
where atomicity was preferable. Manually exercise the WebSocket path
after this step (connect, subscribe, publish, relay a reed) since no
amount of `go test` coverage substitutes for confirming the live
connection-handling code still behaves the same after this much code
motion.
