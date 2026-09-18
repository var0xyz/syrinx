# Depackaging — fold syrinx's Go packages into root

## Status

Proposed.

## Context

syrinx has 14 Go subpackages plus root (`package main`). Go's package
system makes all 14 look like independent, reusable modules — clean
imports, no cycles. They aren't. Applying a stricter bar — would this
survive as a standalone library outside this repo, or is it just a
directory that happens to compile — most of the 14 fail it:

- **Schema-coupled.** `coverage`, `deletion`, `invites`, `realtime`,
  `recovery`, and `signing` (partially) all take a raw `*sql.DB` (or, for
  `signing`, a `DBTX` interface over the same) and run queries against
  tables `db.go`'s schema-init script owns. None of them could run against
  a different schema; the package boundary is invisible to the one thing
  that actually matters here, table ownership. Several of them share
  tables with each other (`realtime`, `recovery`, and `deletion` all touch
  `identities`, `users`, `public_keys`) — a schema change has to be
  reasoned about across package boundaries anyway, so the boundary buys
  nothing.
- **Too small to be a package.** `encoding` (41 lines, 1 file), `roles`
  (224 lines, 1 file), `secret` (474 lines, 1 file) are each a single
  file. `proto` is generated code (see the note on `proto` below — out of
  scope for this spec).
- **Carrying dead code.** `crypto` has 10 of ~28 methods with zero call
  sites anywhere outside the package — verified by grepping every method
  name against the rest of the tree. Dead code shouldn't be moved, it
  should be deleted, and deleting it first shrinks what this spec asks
  anyone to relocate.
- **A container of per-feature builders, not a cohesive concept.**
  `identity` has unexported helpers (`rippleServerHeaders`, and similarly
  named ones per feature) that only make sense in the context of one
  specific feature each. It reads less like "the identity module" and more
  like the place per-feature signing-payload builders ended up because
  nothing else could hold them without an import cycle.

**A second, previously invisible cost of the split: forced duplication.**
Because `realtime` and `recovery` cannot import root (root imports them —
that would be a cycle), each has hand-duplicated wire-shape structs root
already defines. `realtime/wire.go` says so directly in a comment:
"duplicated here (not imported) because realtime cannot depend on the main
package." Folding these into root doesn't just remove a package boundary,
it deletes the forced duplicates outright — see the collision tables in
each per-package file for exactly which ones are true duplicates (delete)
vs same-name-different-shape (rename, not the same thing).

## Scope

**In scope** — fold into root: `coverage`, `crypto` (after dead-code
deletion), `deletion`, `encoding`, `identity`, `invites`, `realtime`,
`recovery`, `roles`, `secret`, `signing`.

**Out of scope**:

- `proto`. Generated code from `.proto` sources, already being discussed
  for a rename/relocation (to `wire.proto`/`wire.pb.go`) in a separate,
  interrupted thread — not part of this spec. It also has a structural
  constraint none of the in-scope packages share: root is `package main`,
  and Go doesn't allow an executable's package to be imported, so
  generated wire types used by more than just `main` can never literally
  live in root the way everything else in this spec does. Whatever that
  rename lands on, it stays its own minimal package.
- `observability` and `observability/metrics`. Initially considered for
  this spec on file-size grounds alone, but `observability/metrics`
  defines a real `Recorder` interface (13 methods) with two
  implementations (`Noop`, `OTEL`), injected via `SetMetrics` into both
  `handlers.go` and `realtime.RealtimeService`, and used directly in
  `recovery/identity.go`. That's a genuine interface-based
  dependency-injection abstraction, not a loose grab-bag of functions like
  `encoding`/`secret` — it stays independent.

## Dependency order

The 11 in-scope packages import each other, not just root. Folding them in
the wrong order means moving code twice. The table below is the actual
topological order, derived by listing every in-scope package's in-scope
imports directly (`go list -f '{{join .Imports "\n"}}' ./<pkg>/ | grep
'^syrinx'`, re-verified this session — an earlier draft of this table got
this wrong by assuming `identity`/`roles`/`signing` were leaves that could
move last, when in fact `invites`, `recovery`, and `deletion` all depend on
them):

| # | Package | Depends on (must land first) |
|---|---------|-------------------------------|
| [00](00_encoding.md) | `encoding` | — |
| [01](01_secret.md) | `secret` | — |
| [02](02_coverage.md) | `coverage` | — |
| [03](03_crypto.md) | `crypto` | — (dead-code deletion first, see file) |
| [04](04_signing.md) | `signing` | — |
| [05](05_identity.md) | `identity` | 04 (signing) |
| [06](06_roles.md) | `roles` | 05 (identity) |
| [07](07_deletion.md) | `deletion` | 02, 04, 05 (coverage, signing, identity) |
| [08](08_invites.md) | `invites` | 00, 03, 04, 05, 06 (encoding, crypto, signing, identity, roles) |
| [09](09_recovery.md) | `recovery` | 00, 02, 03, 04, 05, 06 (encoding, coverage, crypto, signing, identity, roles) |
| [10](10_realtime.md) | `realtime` | 00, 02, 03, 05, 06, 07 (encoding, coverage, crypto, identity, roles, deletion) |

`coverage`, `crypto`, `encoding`, and `secret` go first because nothing
else in scope depends on them. `signing` → `identity` → `roles` is a
strict chain (`identity/identity.go` imports `signing`;
`roles/roles.go` imports `identity`) and all three are also imported
directly from root today (`services.go`, `handlers.go`, `main.go`,
`mailbox.go`), so merging them doesn't remove anything root needs — it
just collapses the chain to nothing once `deletion`/`invites`/`recovery`/
`realtime` no longer need to import them either. `deletion`, `invites`,
and `recovery` each depend on some subset of the signing/identity/roles
chain plus the zero-dependency trio, so they can't move until step 06
lands. `realtime` is last: it depends on `deletion` (step 07) in addition
to everything the other schema-coupled packages need.

## Section-header convention for moved code

Root files already use a boxed comment header to divide sections (see
`services.go:83-85`, `handlers.go:2976-2978`):

```go
// =============== //
//   UserService   //
// =============== //
```

When a package's contents land in a root file, the moved block must be
preceded by one of these headers, named after the source package (e.g.
`//   realtime   //`), so the code's origin stays visible after the
package boundary is gone. If a package's code lands in more than one root
file (e.g. handlers in `handlers.go`, wire types in `wire.go`), each
destination file gets its own header — not just one overall.

## Status at a glance

| # | Package | Verdict | Notes |
|---|---------|---------|-------|
| 00 | `encoding` | Merge | 2 tiny functions into `utils.go`, no collisions |
| 01 | `secret` | Merge | New `secret.go`, no collisions |
| 02 | `coverage` | Merge | Smallest schema-coupled package |
| 03 | `crypto` | Delete dead code, then merge | 10 unused methods found |
| 04 | `signing` | Merge | Base of the signing→identity→roles chain |
| 05 | `identity` | Merge | Per-feature builder container, not cohesive |
| 06 | `roles` | Merge | Single 224-line file, last of the chain |
| 07 | `deletion` | Merge | 9 tables, some true-duplicate structs with root |
| 08 | `invites` | Merge | 7 tables, one same-name-different-shape collision |
| 09 | `recovery` | Merge | 14 tables, three-way `UserSignature`/`ServerSignature` name collision |
| 10 | `realtime` | Merge | ~26 tables, largest, must go last (depends on `deletion`) |
| — | `proto` | Out of scope | Separate relocation discussion; can't become part of `package main` |
| — | `observability` + `observability/metrics` | Out of scope | Real `Recorder` DI interface, genuinely independent |

## Verification

This spec produces no code by itself. Each per-package file's own
verification is: after the move, `go build ./...` and `go vet ./...`
succeed, `go test ./...` passes unchanged, and the removed package
directory no longer exists. Collisions listed in each file were found by
diffing top-level `func`/`type` names between the package and root
(`comm -12` on sorted name lists) — re-run that diff after each move
lands, since a later package's merge can introduce a new collision with
code the earlier merge just added to root.
