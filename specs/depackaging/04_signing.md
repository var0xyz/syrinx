# Depackaging 04 — `signing`

## Status

Implemented (root's own call sites only, `store.go` half only), with
deviations. `signing/` still exists in full and stays exported —
`deletion`, `invites`, `recovery` haven't merged yet, and (see deviation
below) neither has `identity`, so `signing.go`'s `BytesToSign` didn't move
either.

**Deviation — `signing.go` deferred to step 05, not moved with `store.go`.**
The original plan assumed both halves move together since `identity`
"merges immediately after." At implementation time, `identity` hadn't
merged *yet* — `BytesToSign`'s only caller is `identity/identity.go`,
which still imports the still-exported `signing` package. Moving
`BytesToSign` into root now would add code nothing in root calls.
Deferred: `signing.go` stays in `signing/` untouched; move it as part of
step 05, when `identity` merging actually gives root a direct caller.

**Deviation — `db.go` was wrong again (same finding as `coverage`,
step 02).** `store.go`'s row types and 4 DB functions moved into
`services.go`, not `db.go` — `db.go` is still pure DDL string constants,
no home for query-helper functions.

**Simplification beyond the original plan — eliminated the
`WireUserSignature`/`WireServerSignature` intermediate layer entirely.**
The spec flagged this as a maybe ("may become unnecessary entirely,
check at implementation time"). Verified: all 5 callers of
`signing.UserWire`/`ServerWire` in `services.go` immediately unpacked the
result into root's own `UserSignature`/`ServerSignature` field-by-field —
the wire types added a conversion step with no remaining purpose once
inside root. `getUserSignatureWire`/`getServerSignatureWire` (new,
replacing `GetUserSignature`+`UserWire` / `GetServerSignature`+
`ServerWire`) return root's `UserSignature`/`ServerSignature` directly.
One caller (the `KeyRevocation.SuccessorSignature` case) only ever needed
the raw `.Signature` field, not a full wire block — that one uses a new
`getUserSignatureRow` (row-only, no wire conversion) instead.

## Depends on

—

## Context

`signing` has two halves that look different but both dissolve once
`identity` (step 05) and the schema-coupled packages (steps 07-10) merge:

- **`signing.go`** (1 function, `BytesToSign`): pure canonicalization
  logic, no DB. This is what `identity/identity.go` imports — the sole
  reason this session originally floated splitting `signing` in two.
- **`store.go`**: `DBTX` interface (subset of `*sql.DB`/`*sql.Tx`) plus
  `InsertUserSignature`/`InsertServerSignature`/`GetUserSignature`/
  `GetServerSignature`/`UserWire`/`ServerWire`, all querying
  `user_signatures`/`server_signatures` — tables owned by root's `db.go`.

Since `identity` is also merging (step 05, immediately after this one),
there's no remaining reason to keep `signing.go` split out as its own
package — once `identity` is part of root, `BytesToSign`'s only caller is
root itself. **This step turned out not to move both halves together —
see Status.** `identity` hadn't merged yet at the point `store.go` moved,
so `signing.go` was deferred to land alongside step 05 instead.

Importers today: `deletion/store.go`, `deletion/account.go` (step 07),
`identity/identity.go` (step 05), `invites/store.go` (step 08),
`recovery/reeds_follows.go`, `recovery/import.go`, `recovery/upsert.go`
(step 09) — all of them merge in later steps, so `signing`'s package form
only needs to keep serving those packages a little longer after this step
lands (they keep importing `"syrinx/signing"` until their own step).

## Collisions

**Same name, different shape — needs a rename, not dedup.**
`signing.UserSignature`/`ServerSignature` are DB row structs:

```go
type UserSignature struct {
    ID          int64
    PublicKeyID string
    Signature   string
}
type ServerSignature struct {
    ID           int64
    PrivateKeyID string
    Signature    string
    SignedAt     time.Time
}
```

Root's `db.go` `UserSignature`/`ServerSignature` are wire structs:

```go
type UserSignature struct {
    ID    string `json:"id"`
    Armor string `json:"armor"`
}
type ServerSignature struct {
    ID       string    `json:"id"`
    Armor    string    `json:"armor"`
    SignedAt time.Time `json:"timestamp"`
}
```

Different field sets, different types (`int64` vs `string` ID), different
purpose (DB row vs wire block) — genuinely two concepts sharing a name.
Rename `signing`'s pair on merge, e.g. `userSignatureRow`/
`serverSignatureRow`, to something that doesn't collide and reads clearly
next to root's existing wire-shaped `UserSignature`/`ServerSignature`.

`WireUserSignature`/`WireServerSignature` (the `UserWire`/`ServerWire`
conversion targets) turned out to have **no root equivalent at all** —
diffed at implementation time: root's `db.go` `UserSignature`/
`ServerSignature` are the wire types every caller actually wanted, and
`WireUserSignature`/`WireServerSignature` were a pointless middle layer
(different field names — `Fingerprint` vs `ID` — existing only because
`signing` couldn't construct root's real type directly). Eliminated
entirely rather than ported — see Status.

No collisions on `DBTX`, `InsertUserSignature`, `InsertServerSignature`,
`GetUserSignature`, `GetServerSignature`.

## Move plan

1. Moved `store.go`'s contents into `services.go` (not `db.go` — see
   Status, same wrong-guess pattern as step 02). `signing.go`
   (`BytesToSign`) deferred to step 05 — see Status.
2. Renamed the colliding row types per the Collisions section above:
   `UserSignature`→`userSignatureRow`, `ServerSignature`→
   `serverSignatureRow`, `DBTX`→`signingDBTX`.
   `InsertUserSignature`/`InsertServerSignature`→`insertUserSignature`/
   `insertServerSignature` (unexported, mechanical rename, no shape
   change). `GetUserSignature`/`GetServerSignature`/`UserWire`/
   `ServerWire` collapsed into two new functions,
   `getUserSignatureWire`/`getServerSignatureWire`, returning root's
   `UserSignature`/`ServerSignature` directly (see Status). Added
   `getUserSignatureRow` for the one caller needing the raw row instead
   of a wire block.
3. Added the section header in `services.go` (appended after the
   `coverage` section from step 02, not in `utils.go`/`db.go` as
   originally planned — see Status for `signing.go`'s deferral and
   `db.go`'s wrong-guess):
   ```go
   // ========== //
   //   signing   //
   // ========== //
   ```
4. Updated all `services.go` call sites to the new unexported functions;
   dropped `services.go`'s `"syrinx/signing"` import. Left `deletion`,
   `identity`, `invites`, `recovery` importing `"syrinx/signing"`
   unchanged — none of them merge in this step.
5. `signing/store.go` and its DB row/function contents are untouched in
   `signing/` (still needed by `deletion`/`invites`/`recovery`) — only
   root gained its own copy. `signing/signing.go` (`BytesToSign`) is
   untouched too, deferred to step 05. `signing/` directory deletion
   waits until step 09 (`recovery`, the last remaining external
   importer) lands.

## Verification

`go build ./...`, `go vet ./...`, `go test ./...`, plus `go build -tags
ops` and `go build -tags ripplescleanup` all pass. Confirmed the renamed
row types (`userSignatureRow`, `serverSignatureRow`, `signingDBTX`) don't
collide with anything root already has. `signing/`
directory deletion is deferred to after step 09, not this step.
