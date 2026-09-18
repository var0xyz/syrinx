# Depackaging 10 — `realtime`

## Status

Proposed.

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
