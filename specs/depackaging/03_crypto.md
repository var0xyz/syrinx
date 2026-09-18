# Depackaging 03 — `crypto`

## Status

Proposed.

## Depends on

—

## Context

`crypto` (4 files: `crypto.go`, `ids.go`, `interface.go`, `types.go`) has
zero DB coupling and is used across root, `realtime`, and `recovery` via a
concrete `*crypto.Service` passed around directly (`crypto.NewService()`
called in `main.go`, `ops.go`; `*crypto.Service` threaded through
`root.go`, `services.go`, `mailbox.go`, `realtime/auth.go`,
`recovery/import.go`). This is a DI pattern like `observability/metrics`'s
`Recorder`, but structurally different: `interface.go` declares a `Crypto`
interface with every one of `Service`'s methods, yet it has **zero real
usages as an interface type** anywhere in the tree except one field
declaration (`middlewares.go:50`, `cryptoService crypto.Crypto`) that is
never actually assigned anything but a `*Service` — there's no second
implementation, no mock, nothing polymorphic happening. Unlike
`metrics.Recorder` (two real implementations, `Noop`/`OTEL`, genuinely
swapped), `Crypto` is an interface with one implementer that could be
deleted without changing any runtime behavior.

**Dead code**: cross-referencing every method against the rest of the
tree (grep for `.<MethodName>(` outside `crypto/`) found 10 with zero
external callers:

| Method | External callers |
|---|---|
| `GenerateNonce` | 0 |
| `ExtractCreationTime` | 0 |
| `ExtractExpirationTime` | 0 |
| `ExtractKeyExpirationTime` | 0 |
| `ExtractPublicKeyArmor` | 0 |
| `FindEntityByIdentity` | 0 |
| `ValidatePublicKey` | 0 |
| `ExtractKeyMetadata` | 0 |
| `ExtractEntity` | 0 |
| `ReadArmoredKeyRing` | 0 |

All 10 are also declared on the `Crypto` interface — once they're deleted,
`Crypto` no longer has a reason to exist either (it was never used
polymorphically in the first place). `types.go`'s `CryptographicKey` type
is only constructed by `ExtractKeyMetadata` (dead) and
`ValidateAndExtractPublicKey` (live, 3 external callers) — stays, since
`ValidateAndExtractPublicKey` still needs it.

Live methods (real external callers, must be preserved): `NewID`,
`IsValidID`, `IsValidUUIDv7`, `Hash`, `CreateKeyPair`, `Sign`, `Encrypt`,
`Decrypt`, `ExtractFingerprintFromArmor`, `EncryptSymmetric`,
`DecryptSymmetric`, `VerifySignature`, `VerifyDetachedSignature`,
`VerifySignedChallenge`, `ValidateTimestamp`,
`ValidateAndExtractPublicKey`, `EncryptPrivateKey`, `AddIdentity`,
`DecryptPrivateKey`. `KeyPair` (`types.go`) is live — constructed by
`CreateKeyPair`, consumed via `.PublicKey` at `services.go:296`.

(Note: a separate `cli/` directory in this repo is its own Go module —
`go list ./cli/...` fails to resolve against the main module — and
defines its own unrelated `KeyPair` type. Not in scope, no import
relationship with `syrinx/crypto`.)

## Collisions

None — no root-level `func`/`type` name conflicts for anything in
`crypto/`.

## Move plan

1. **Delete first, move second.** Remove the 10 dead methods listed above
   from `crypto.go`, then remove the now-pointless `Crypto` interface
   from `interface.go` and the one dead field reference at
   `middlewares.go:50`. Run `go build ./...` to confirm nothing else
   referenced them (the grep above should already guarantee this, but
   confirm before moving anything).
2. Move the remaining contents of `crypto.go`, `ids.go`, `types.go` into a
   new root file, `crypto.go`. This is a full-file move of cohesive,
   substantial code (still ~350+ lines after deletion) — no existing root
   file is a natural fit to merge into, so like `secret` (step 01) this
   gets its own new file with no section header needed.
3. Rename `Service`/`NewService` if root already has a naming convention
   for this kind of thing worth matching (check at implementation time —
   not verified either way in this session).
4. Update every call site (`main.go`, `ops.go`, `root.go`, `services.go`,
   `mailbox.go`) to drop the `crypto.` prefix and import. Leave
   `realtime/auth.go` and `recovery/import.go` importing `"syrinx/crypto"`
   until those packages merge in later steps.
5. Delete the `crypto/` directory once its file list is empty (port
   `crypto/*_test.go` cases — `add_identity_test.go`, `ids_test.go`,
   `symmetric_test.go` — to root test files first, dropping any test
   coverage that only existed for the 10 deleted methods).

## Verification

`go build ./...`, `go vet ./...`, `go test ./...` pass at each of the two
sub-steps (delete, then move) — don't combine them into one commit/change,
since a build failure after deletion but before the move pinpoints whether
the dead-code analysis in this file was wrong, while a failure after the
move points at the move itself.
