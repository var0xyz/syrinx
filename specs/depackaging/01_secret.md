# Depackaging 01 — `secret`

## Status

Implemented. `secret/passphrase.go` and `secret/passphrase_test.go` moved
to root `secret.go`/`secret_test.go`, `secret/` directory deleted (this
package had no remaining in-repo dependents once moved).

## Depends on

—

## Context

`secret/passphrase.go` is 474 lines, one file: an OS-keychain-backed
resolver for the server's signing-key passphrase (`Resolver`, `Keyring`
interface, `osKeyring` implementation, `GeneratePassphrase`). It has no
DB coupling and no dependents inside the repo other than root — its only
two callers are `main.go` (`package main`, `//go:build !ops &&
!ripplescleanup`) and `ops.go` (`package main`, `//go:build ops`). Both
callers already compile into the same package under different build tags,
so merging `secret` in doesn't change how those two binaries are built —
it just removes the `"syrinx/secret"` import and qualifier from both.

## Collisions

None found — `Resolver`, `Keyring`, `osKeyring`, `GeneratePassphrase`,
`ErrEnvManaged`, `ErrTooShort`, `MinPassphraseLen`, `SourceEnv`,
`SourceKeychain`, `SourcePrompt`, `SourceGenerated` have no root-level
name conflicts.

## Move plan

1. Move `secret/passphrase.go`'s contents verbatim into a new root file,
   `secret.go`. At 474 lines with no other content sharing the file, this
   is a straight file move, not an insertion into existing content — no
   section header needed (the header convention marks a boundary between
   blocks *within* a file; a new single-purpose file has nothing to
   delineate from). Contrast with `encoding` (step 00), which merges into
   `utils.go`'s existing content and does get a header.
2. Since the new `secret.go` would carry no build tag, check that neither
   `main.go`'s nor `ops.go`'s build-tag exclusivity is accidentally
   broken — the moved file itself has no tag, so it compiles into both
   binaries, which is correct (both need `Resolver` etc.).
3. Update `main.go` and `ops.go` to drop the `secret.` prefix and the
   `"syrinx/secret"` import.
4. Delete the `secret/` directory (including `secret/*_test.go` — move
   its test cases to a root test file first).

## Verification

`go build -tags ops -o bin/ops .` and the normal `go build .` both still
succeed (this is the one package where both build-tag variants must be
checked explicitly, since it's used by both). `go test ./...` passes.
`secret/` directory no longer exists.
