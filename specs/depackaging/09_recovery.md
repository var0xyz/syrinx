# Depackaging 09 — `recovery`

## Status

Implemented and fully complete — `recovery/` directory **deleted** (zero
remaining external importers once its own call sites moved, same as
`invites` in step 08).

**File layout deviated from the plan's guess.** The plan's Move plan said
DB-touching code goes to `db.go` — same wrong guess this spec has made and
self-corrected on every schema-coupled step so far (`coverage`, `signing`,
`deletion`, `invites`). `db.go` stayed pure DDL. Actual layout:
- **New `recovery.go`** (untagged, no build tag): wire types
  (`recoveryProfile`, `recoveryUserSignature`, `recoveryServerSignature`,
  `recoveryKeyWire`, `recoveryRevocation`, `recoveryKeyNode`, request/response
  types), nest verification (`flattenKeysNest`,
  `verifyProfileServerCountersig`, `verifyRecoveryKeyCountersig`,
  `verifyRecoveryRevocation`, `validateChallengeAge`,
  `verifyChallengeSignature`), and bundle export/import/rotate/password
  logic (`exportFromDB`, `importIntoDB`, `marshalBundleJSON`,
  `parseBundleJSON`, `rotateServerKeyPassphrase`, `passwordStrengthWarning`,
  `staleIdentityBackupMessage`, `setIdentityBackupAt`). Untagged because
  both the normal server binary (`main.go`/`handlers.go`, `!ops`) and the
  `ops` CLI (`ops.go`) call into it — same reason `crypto.go`/`roles.go`
  ended up untagged in earlier steps.
- **`services.go`**: `unclaimed_accounts`/`ongoing_recoveries` store
  functions became `DataService` methods (`InsertUnclaimed`,
  `DeleteUnclaimed`, `InsertOngoing`, `DeleteOngoing`, `CountUnclaimed`),
  inserted directly after the pre-existing `IsUnclaimed`/`IsOngoing`
  read-side methods root already had (a real pre-existing duplication this
  merge resolved — root had read-only wrappers over the same two tables
  before this step, the write side just lived across the package boundary).
  A `// ============ //` / `// recovery //` section holds the rest:
  identity/reed/follow save logic (`saveOwnIdentity`, `savePeerIdentity`,
  `saveRecoveryReed`, `saveRecoveryFollowing`, and their unexported
  helpers), and the import-gate middleware
  (`recoveryImportGateMiddleware`, `recoveryAllowedDuringImport`).
- **`handlers.go`**: `IssueChallenge`, `ClaimIdentity`, `ReportPeerIdentity`,
  `ReportReed`, `ReportFollowing`, `CompleteImport` as plain `*Handlers`
  methods, plus `verifyRecoveryReedCountersig`. `main.go` registers routes
  directly (`api.HandleFunc(...)`, reusing `h.noop` for OPTIONS) instead of
  `recovery.RegisterRoutes(api, recovery.Deps{...})` — same pattern as the
  `invites` step's route registration change.

**A second, deeper build-tag ripple, beyond the usual "wrong destination
guess."** `recovery.go` needs to compile under all three build tags (`ops`
CLI calls the bundle functions; the normal server binary's handlers call
the nest/challenge functions), but it depends on `identity.go`,
`identity_id.go`, `utils.go`, and `roles.go` — all four were still tagged
`!ops && !ripplescleanup` from earlier steps, because nothing forced them
untagged until now. Re-audited each for genuine `!ops`-specific content
(none — all four are pure self-contained helpers/types with zero
dependency on `Handlers`/`DataService`/HTTP) and removed the tag from all
four, same fix already applied to `crypto.go`/`constants.go` in earlier
steps. The one dependency that couldn't be untagged this way was
`insertServerSignature` (in `services.go`'s large, genuinely `!ops`-coupled
signing section, interleaved with invites/mentions code) — rather than
drag that whole section's tag boundary around for one 3-line INSERT,
duplicated it locally as `insertRecoveryServerSignature` in `recovery.go`,
used only by `importIntoDB` (the one `ops`-reachable caller).

**`Services.legacyCrypto` field removed entirely**, not just its recovery
usage. It existed solely to bridge `recovery.Verifier`'s need for
`*crypto.Service`'s exported API; `handlers.go`'s two real call sites
(`UserStatus` and `BootstrapAccountRecovery` — both root's own account-
recovery-by-device feature, not identity recovery, but they already called
into the `recovery` package's verification helpers before this step) now
pass `h.services.crypto` (`*cryptoService`, unexported) directly, since
`recoveryVerifier`'s two-method shape (`verifySignature`,
`verifySignedChallenge`) is satisfied by the unexported methods.
`main.go`'s `legacyCryptoService` (`*crypto.Service`, exported) stays —
still needed by `realtime.NewService`, the last remaining consumer,
deferred to step 10. `ops.go`'s three call sites (`runExportIdentity`,
`runImportIdentity`, `runRotatePassphrase`) dropped their own
`legacyCryptoSvc := crypto.NewService()` bridges — `runImportIdentity`
already had a `newCryptoService()` instance in scope for bundle
decryption, reused directly; `runRotatePassphrase` created one where it
previously didn't need to.

**Collisions resolved exactly as the plan predicted**, confirmed at
implementation time:
- `recovery.Invite` deleted, root's own `Invite` (`db.go`) used directly —
  confirmed byte-for-byte identical before deleting.
- `recovery.UserSignature`/`ServerSignature` renamed to
  `recoveryUserSignature`/`recoveryServerSignature` (not folded into
  `signing`'s `userSignatureRow`/`serverSignatureRow` or root's own
  `UserSignature`/`ServerSignature` — genuinely a third variant, the
  split-on-decode behavior over canonical `id` is unique to this package's
  own wire contract and not needed elsewhere, so three permanently
  separate types is in fact the right end state, not a stepping stone).
- `recovery.Profile` was NOT a true duplicate of root's `User` despite
  strong overlap (same `ID`/`Username`/`Role`/`Bio`/member-since/signature
  fields) — kept as its own `recoveryProfile` type since it additionally
  carries `ActiveKeyFingerprint`/`HasReeds` and uses the split-ID
  signature variants above; forcing it into `User`'s shape would have been
  a bigger, riskier change than this step's scope.

**Test suite: 9 source files (1,045 lines) ported into 6 new root files**
(`recovery_password_test.go`, `recovery_import_test.go`,
`recovery_middleware_test.go`, `recovery_nest_test.go`,
`recovery_status_test.go`, `recovery_wire_test.go`,
`recovery_handlers_test.go`, `recovery_identity_test.go` — split slightly
differently than the original 9 files since `nest_test.go`'s
`fakeVerifier` type is shared by several others and needed one canonical
home). Handler tests rewritten (not mechanically ported) to call real
`*Handlers` methods via `newSignupGateHandlers`/real crypto instead of the
old `Deps{Crypto: fakeVerifier{...}}` struct-of-closures pattern — same
approach the `invites` step used. `TestRegisterRoutes_IncludesPhase6`
dropped (no separable `RegisterRoutes` function anymore; its OPTIONS-route
coverage is subsumed by `main.go`'s direct registration plus the
per-handler tests). No new bugs surfaced during the port (unlike `invites`,
where the fake-to-real crypto switch caught two pre-existing bugs) — every
ported test passed on the first real-crypto run.

**Three pre-existing root test files** (`recovery_bundle_test.go`,
`recovery_collision_test.go`, `recovery_following_test.go`) already existed
before this step, written against the old `syrinx/recovery` package as an
external dependency (regression tests for bugs found while integrating
root with recovery, predating this depackaging effort) — updated in place
to call the new unexported root functions/types instead of deleted
entirely, since they test real regressions (double-canonicalization,
username-collision handling) worth keeping.

**`specs/recovery/README.md` and `specs/README.md` updated** per the
user's prior explicit approval to override the "recovery must stay its own
package" design decision — four mentions total (three in
`specs/recovery/README.md`'s "Code organization" section plus the
"Resolved (decisions)" entry, one in `specs/README.md`'s intro) now point
to this file instead of asserting the package boundary. Historical
protocol/verification/table-layout design decisions in those documents are
unchanged — only the package-boundary mandate itself was superseded.

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

## Move plan (as executed)

1. Moved all 15 files' contents into root, organized by what they do, not
   dumped as one block — but into **`recovery.go` + `services.go` +
   `handlers.go`**, not `db.go` (the plan's guess was wrong again, same
   as every prior schema-coupled step — `db.go` stayed pure DDL). Wire
   types + nest/bundle verification logic → new untagged `recovery.go`
   (needed by both the `ops` CLI and the normal server binary). DB-touching
   store/save logic (`store.go`, `upsert.go`, `reeds_follows.go`) →
   `services.go`, either as new `DataService` methods (unclaimed/ongoing) or
   package-level functions in a `recovery` section. HTTP handlers
   (`handlers.go`, `routes.go`, `middleware.go`) → `handlers.go` as
   `*Handlers` methods, with the import-gate middleware landing in
   `services.go` instead (it's not itself an HTTP handler).
2. Deleted `recovery.Invite`; root's `Invite` (`db.go`) used directly at
   every former call site (`recoveryProfile.Invite` field).
3. Renamed `recovery`'s `UserSignature`/`ServerSignature` to
   `recoveryUserSignature`/`recoveryServerSignature` per the Collisions
   section — kept as permanently distinct types, not folded into
   `signing`'s or root's own signature shapes (see Status).
4. Added section headers where merging into files with pre-existing
   content (`services.go`), following the established convention; no
   header in the new standalone `recovery.go` (same convention as
   `secret.go`/`crypto.go` in earlier steps).
5. Updated all call sites (`main.go`, `handlers.go`'s pre-existing
   `UserStatus`/`BootstrapAccountRecovery`, `ops.go`); deleted the
   `recovery/` directory — zero remaining external importers once its own
   call sites moved, so no deferral needed (same as `invites` in step 08,
   unlike every schema-coupled step before that).
6. **Updated `specs/recovery/README.md`**: all four "Code organization"
   mentions (three plus the "Resolved (decisions)" entry) now note
   recovery's code lives in root per this file, linking back here.
   Historical design rationale elsewhere in that document is unchanged —
   only the package-boundary mandate itself was superseded.
7. **Also updated `specs/README.md`**'s top-of-file intro the same way.

**One additional step the original plan didn't anticipate**: `recovery.go`
being untagged forced `identity.go`, `identity_id.go`, `utils.go`, and
`roles.go` to also lose their `!ops && !ripplescleanup` tag (see Status) —
this ripple wasn't visible until the actual multi-variant build was
attempted, same category of surprise as the `crypto`/`roles` steps'
build-tag fixes, just one dependency layer deeper this time.

## Verification

`go build ./...`, `go vet ./...`, `go build -tags ops`, `go build -tags
ripplescleanup`, and `go test ./...` (full suite, real Postgres, ~72s) all
pass. All ported recovery tests pass, including the real-signature tests
(`TestVerifyProfileServerCountersig_RealSignatureAfterJSONRoundTrip`,
`TestVerifyRecoveryReedCountersig_RealSignature`) that caught real bugs in
the `invites` step — no equivalent bugs surfaced here. `recovery/`
directory is gone — `git rm -r recovery/` — confirmed via `go list ./...`
no longer showing `syrinx/recovery`. `specs/recovery/README.md` and
`specs/README.md` no longer contradict the actual code organization.
