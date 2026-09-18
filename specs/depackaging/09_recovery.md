# Depackaging 09 — `recovery`

## Status

Proposed.

## Depends on

[00](00_encoding.md) (`encoding`), [02](02_coverage.md) (`coverage`),
[03](03_crypto.md) (`crypto`), [04](04_signing.md) (`signing`),
[05](05_identity.md) (`identity`), [06](06_roles.md) (`roles`)

## Context

`recovery` (15 files, 3,298 lines) implements server-identity export/
import, own-identity claim, and DB-reconstruction recovery. Touches
`identities`, `users`, `servers`, `reeds`, `reed_allocations`,
`user_devices`, `user_followers`, `user_following`, `pending_follows`,
`unclaimed_accounts`, `ongoing_recoveries`, `public_keys`, `private_keys`,
`public_key_revocations` — 14 tables, the second most schema-entangled
package after `realtime`, and overlapping with `realtime` on `identities`,
`users`, `reed_allocations`, `public_keys`, `user_followers`,
`user_following`. Imports `coverage`, `crypto`, `encoding`, `identity`,
`roles`, `signing` — all six merge in earlier steps of this spec.

**This merge contradicts an existing documented decision.**
`specs/recovery/README.md` states, in three separate places, that "all
server-side recovery logic must live in the `syrinx/recovery` Go package"
as a deliberate boundary (`main` only wires boot/routes/middleware). That
was a reasonable call at the time recovery was designed — but the same
stricter bar this whole depackaging spec applies (would this survive as
independent code, or is it just schema-coupled queries in a separate
directory) applies here too: `recovery` takes a raw `*sql.DB` and queries
tables root owns, same as `deletion`/`invites`/`realtime`. **This step
also updates `specs/recovery/README.md`'s three "Code organization"
mentions** to drop the package-boundary mandate and note it's superseded
by this spec — see the Move plan's final item.

## Collisions

**True duplicate — delete, don't rename.** `recovery.Invite` is byte-for-
byte identical to root's `Invite` (`db.go`):

```go
type Invite struct {
    ID       string `json:"id"`
    UserID   string `json:"userID"`
    Username string `json:"username"`
}
```

Delete `recovery`'s copy on merge, use root's directly — same pattern as
`realtime.RippleWire` (step 10) and unlike `invites.Invite` (step 08,
which needs a rename because it's a different shape despite the same
name).

**Also present in `recovery/wire.go`, not yet cross-checked against root
by name** (only `Invite` was diffed this session — `comm -12` found no
other `recovery/wire.go` type name collides with root's current types,
but re-run the check at implementation time since earlier steps in this
spec will have added new types to root by the time this step lands):
`UserSignature`, `ServerSignature` — see below, these DO collide but in a
three-way way, not just recovery-vs-root.

**Three-way name collision — `UserSignature`/`ServerSignature`.** By the
time this step lands, root will already have absorbed `signing`'s
DB-row-shaped `UserSignature`/`ServerSignature` (renamed in step 04, e.g.
to `userSignatureRow`/`serverSignatureRow`) alongside its own original
wire-shaped `UserSignature`/`ServerSignature`. `recovery/wire.go` has a
**third** shape:

```go
type UserSignature struct {
    KeyID string `json:"-"`
    Armor string `json:"armor"`
}
// custom UnmarshalJSON/MarshalJSON decode the wire's `id` into KeyID
```

```go
type ServerSignature struct {
    ServerID    string    `json:"-"`
    Fingerprint string    `json:"-"`
    Armor       string    `json:"armor"`
    Timestamp   time.Time `json:"timestamp"`
}
// custom UnmarshalJSON/MarshalJSON decode the wire's `id` into ServerID+Fingerprint
```

This is neither root's wire shape nor `signing`'s DB-row shape — it's a
third variant that splits the wire `id` field into components via custom
JSON marshal/unmarshal. Needs its own rename on merge (e.g.
`recoveryUserSignature`/`recoveryServerSignature`, or fold its
split-on-decode behavior into root's existing type if the split-ID
behavior turns out to be needed elsewhere too — worth checking whether
root or `signing` ever need this same ID-splitting logic before assuming
three permanently separate types is the right end state).

## Move plan

1. Move all 15 files' contents into root, organized by what they do, not
   dumped as one block — e.g. HTTP handlers (`handlers.go`, `routes.go`,
   `middleware.go`) into `handlers.go`, DB-touching code
   (`store.go`, `upsert.go`, `reeds_follows.go`) into `db.go`, wire types
   (`wire.go`) into `wire.go`, following the same file-by-content-type
   split this spec has used for every other package.
2. Delete `recovery.Invite`, use root's `Invite` directly at every former
   call site.
3. Rename `recovery`'s `UserSignature`/`ServerSignature` per the
   Collisions section.
4. Add section headers per destination file:
   ```go
   // ============ //
   //   recovery   //
   // ============ //
   ```
5. Update call sites; delete the `recovery/` directory (port
   `recovery/*_test.go` cases first).
6. **Update `specs/recovery/README.md`**: replace the three "Code
   organization" mentions of "all server-side recovery logic must live in
   the `syrinx/recovery` Go package" with a note that recovery's code now
   lives in root per `specs/depackaging/`, linking back to this file.
   Don't delete the historical design rationale in the rest of that
   document — only the package-boundary mandate itself is superseded, not
   the recovery feature's design decisions.
7. **Also update `specs/README.md`** — its top-of-file intro says "All
   server-side recovery implementation belongs in the **`syrinx/recovery`**
   package; main only wires boot, routes, and middleware," the same claim
   repeated a second place beyond `specs/recovery/README.md`. Update it
   the same way.

## Verification

`go build ./...`, `go vet ./...`, `go test ./...` pass — recovery has
substantial test coverage (bundle export/import round-trips, own-identity
claim flow, nested key-chain handling per `specs/recovery/`'s numbered
steps), don't let any of it silently drop during the port.
`specs/recovery/README.md` no longer contradicts the actual code
organization.
