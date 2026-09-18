# Depackaging 03 — `crypto`

## Status

Implemented (root's own call sites only), with several deviations and one
real blocker found and fixed during the move. `crypto/` still exists and
stays exported — `realtime`, `recovery`, `invites` haven't merged yet.

**Blocker — `observability/metrics` imported `crypto.Hash`.** Not caught
by the original audit's importer list (which only checked for packages
this spec was folding into root, not for out-of-scope packages depending
on an in-scope one). `observability/metrics/hash.go` called
`crypto.Hash` for `UserIDHash`/`EventIDHash`. Since `observability/metrics`
stays independent forever (per this spec's README) and `crypto` was about
to become unexported inside `package main`, this import would have become
permanently impossible to satisfy. Fixed by inlining `sha256.Sum256`
directly in `hash.go` — `crypto.Hash` was a 3-line wrapper, not worth a
whole package dependency for one call. `observability/metrics` no longer
imports `crypto` at all.

**Deviation — build tag.** `crypto.go` was first written with
`//go:build !ops && !ripplescleanup` (matching `secret`/`encoding`'s
tag), but that broke the `ops` build: `ops.go` (`//go:build ops`) and
`mailbox.go` (no build tag, compiles into all three variants) both need
crypto functions. Removed the tag entirely — `crypto.go` now compiles
into all three binaries, matching `mailbox.go`'s untagged pattern.
`ripplescleanup` doesn't reference any of it, which is fine (Go doesn't
require every declaration in a file to be used elsewhere).

**Deviation — a "legacy bridge" instance is needed, not just deferred
imports.** The original move plan assumed leaving `realtime`/`recovery`
importing `"syrinx/crypto"` would be enough. It isn't: `realtime.NewService`
takes a literal `*crypto.Service` parameter (not an interface), and
`recovery.Verifier` is an interface requiring exported `VerifySignature`/
`VerifySignedChallenge` — both fail against root's new unexported
`*cryptoService`. Root now constructs **two** crypto service instances
where this matters: the new unexported `cryptoService` for its own code,
and a `crypto.NewService()` (`legacyCryptoService` in `main.go`,
`legacyCryptoSvc` in `ops.go`) passed only to `realtime.NewService` and
`recovery.*` calls. `Services` gained a `legacyCrypto *crypto.Service`
field for handlers-layer call sites (`handlers.go`'s two
`recovery.VerifyProfileServerCountersig`/`VerifyChallengeSignature`
calls). Both bridge instances are deleted once `realtime` (step 10) and
`recovery` (step 09) merge — nothing else needs them.

**Deviation — every root test file constructing a crypto service or
matching its types needed updating too**, not just production code:
`ripples_test.go`, `ripples_handlers_test.go`, `handlers_signup_gate_test.go`,
`federation_test.go`, `federation_handshake_test.go`,
`recovery_bundle_test.go`, `services_test.go`. Test-local crypto
instances (creating a keypair to sign a test request, no cross-package
constraint) switched to the new unexported `newCryptoService()`/
`cryptoKeyPair`. The two spots needing `recovery.ValidateDecrypt`/
`ImportIntoDB` (in `recovery_bundle_test.go`) use the same legacy-bridge
pattern as production code.

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
tree found 10 with zero *external* callers, but 4 of those 10 are called
*internally*, from `ValidateAndExtractPublicKey` (live, 3 external
callers) — the original pass over this file only grepped for external
callers and wrongly listed all 10 as removable. Re-verified at
implementation time with an internal-caller check too:

| Method | External callers | Internal callers | Verdict |
|---|---|---|---|
| `GenerateNonce` | 0 | 0 | dead, delete |
| `ExtractExpirationTime` | 0 | 0 | dead, delete |
| `FindEntityByIdentity` | 0 | 0 | dead, delete |
| `ValidatePublicKey` | 0 | 0 | dead, delete |
| `ExtractKeyMetadata` | 0 | 0 | dead, delete |
| `ReadArmoredKeyRing` (the `*Service` method — not `openpgp.ReadArmoredKeyRing`, the library function it wraps) | 0 | 0 | dead, delete |
| `ExtractCreationTime` | 0 | 2 (`ValidateAndExtractPublicKey`, `ExtractKeyMetadata`) | **keep** — live via `ValidateAndExtractPublicKey` |
| `ExtractKeyExpirationTime` | 0 | 3 (same two, plus `CreateKeyPair`) | **keep** |
| `ExtractPublicKeyArmor` | 0 | 2 (same two) | **keep** |
| `ExtractEntity` | 0 | 3 (`Encrypt`, `ExtractFingerprintFromArmor`, `ValidateAndExtractPublicKey`) | **keep** |

Only 6 are actually dead, not 10. All 6 are declared on the `Crypto`
interface, along with the 4 kept ones — deleting the 6 doesn't make
`Crypto` pointless on its own, but `Crypto` is still worth removing
regardless (see the "zero real usages as an interface type" finding
above) once the dead methods are gone and the interface's remaining
declarations are checked against what actually needs `Crypto` per the
`middlewares.go:50` finding. `types.go`'s `CryptographicKey` type is
constructed by both `ExtractKeyMetadata` (dead, being deleted) and
`ValidateAndExtractPublicKey` (live) — stays, since
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

1. **Delete first, move second.** Removed the 6 genuinely dead methods
   (see Status) from `crypto/crypto.go`, then the now-pointless `Crypto`
   interface (`crypto/interface.go`, deleted entirely) and its one dead
   field reference at `middlewares.go:50` (`cryptoService crypto.Crypto`
   → `cryptoService *crypto.Service`, since with `Crypto` gone the field
   needs a concrete type — this was itself temporary, see step 4).
   Committed separately before the move (`go build`/`vet`/`test` all
   green at this checkpoint).
2. Moved `crypto.go`, `ids.go`, `types.go`'s remaining contents into a
   new root file, `crypto.go`, no build tag (see Status deviation).
   Renamed every exported identifier to unexported, `PascalCase` →
   `camelCase`: `Service`→`cryptoService`, `NewService`→
   `newCryptoService`, `KeyPair`→`cryptoKeyPair`,
   `CryptographicKey`→`cryptographicKey`, `HashSize`→`cryptoHashSize`,
   `Hash`→`cryptoHash`, `Alphabet`→`idAlphabet`, `Length`→`idLength`,
   `NewID`→`newCryptoID`, `IsValidID`→`isValidCryptoID`,
   `IsValidUUIDv7`→`isValidUUIDv7`, and all 18 live `*Service` methods to
   lowercase (`Sign`→`sign`, `CreateKeyPair`→`createKeyPair`, etc.).
   Resolved a real `gocrypto` import-alias collision: `crypto.go`'s
   `gocrypto "crypto"` (stdlib) vs `ids.go`'s `gocrypto "crypto/rand"` —
   the latter renamed to `cryptorand` in the merged file.
3. Updated every call site (`handlers.go`, `services.go`, `root.go`,
   `ops.go`, `main.go`, `mailbox.go`, `middlewares.go`) to the new
   unexported names and dropped the `"syrinx/crypto"` import — except
   where the legacy-bridge instance is still needed (see Status). Left
   `realtime/auth.go`, `recovery/*.go`, `invites/*.go` importing
   `"syrinx/crypto"` unchanged — those packages haven't merged yet.
4. `crypto/` directory deletion deferred — still needed by `realtime`
   (step 10, also the last consumer of the legacy-bridge pattern),
   `recovery` (step 09), `invites` (step 08).

## Verification

`go build ./...`, `go vet ./...`, `go test ./...`, plus `go build -tags
ops` and `go build -tags ripplescleanup` (this package's functions are
needed by all three binary variants — see Status) all pass at both
sub-steps, landed as separate commits: dead-code deletion first, then the
move plus the `observability/metrics` fix plus every affected root file
(production and test).
