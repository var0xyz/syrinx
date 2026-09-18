# Depackaging 04 — `signing`

## Status

Proposed.

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
root itself. Both halves move together in this one step; no split.

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
conversion targets) — check these against root's own wire types at
implementation time; not diffed this session, likely close to or
identical to root's `UserSignature`/`ServerSignature` given the naming,
in which case `UserWire`/`ServerWire` may become unnecessary entirely
(construct root's wire type directly instead of converting through an
intermediate `WireUserSignature`).

No collisions on `DBTX`, `InsertUserSignature`, `InsertServerSignature`,
`GetUserSignature`, `GetServerSignature`, `UserWire`, `ServerWire`,
`BytesToSign`.

## Move plan

1. Move `signing.go` and `store.go`'s contents into root. `BytesToSign`
   is small and general enough to fit `utils.go` alongside `encoding`'s
   two functions (step 00) — same file, same header. The `store.go`
   contents (DB row types + 4 functions) are substantial enough to
   warrant their own section in `db.go`, next to the `user_signatures`/
   `server_signatures` DDL they already depend on.
2. Rename the colliding row types per the Collisions section above.
3. Add section headers:
   - In `utils.go`, alongside (not replacing) the `encoding` header from
     step 00:
     ```go
     // ========== //
     //   signing   //
     // ========== //
     ```
   - In `db.go`:
     ```go
     // ================ //
     //   signing/store   //
     // ================ //
     ```
4. Update call sites incrementally as each importing package merges in
   its own later step — don't force all of them to update in this step;
   `deletion`, `identity`, `invites`, `recovery` keep importing
   `"syrinx/signing"` until their own step lands.
5. Delete the `signing/` directory only after step 09 (`recovery`, its
   last remaining external importer) lands.

## Verification

`go build ./...`, `go vet ./...`, `go test ./...` pass. Confirm the
renamed row types don't collide with anything root already has (re-run
the `comm -12` check after renaming, not just before). `signing/`
directory deletion is deferred to after step 09, not this step.
