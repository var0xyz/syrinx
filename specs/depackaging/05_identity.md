# Depackaging 05 — `identity`

## Status

Implemented (root's own call sites only). `identity/` still exists in
full and stays exported — `deletion`, `invites`, `recovery`, `realtime`,
and `roles` (the very next step) haven't merged yet.

**This step also completed step 04's deferral.** `signing.go`'s
`BytesToSign` moved into `utils.go` as `bytesToSign` in this step (not
04), since `identity.go`'s payload builders are its first real root
caller — see 04's Status for why it waited. `signing/signing.go` itself
is untouched and stays exported (still needed by `identity/identity.go`,
which is still a package until this directory's own external importers
clear).

**Scope turned out to be 3 new root files, not 2.** The spec's move plan
anticipated `identity.go` + `identity_id.go`. `device.go`
(`ParseDeviceID`/`ErrMissingDevice`, 24 lines) was folded into
`identity_id.go` instead of getting a third file — small enough not to
warrant its own, and conceptually adjacent (ID parsing/validation).

**13 test files needed the same treatment as production code** — not
mentioned in the original plan, which only listed the 5 production
files. Found via a real test failure, not just a build error:
`devices_test.go`'s `TestCheckActiveDevice` compared
`err != identity.ErrMissingDevice`, but `CheckActiveDevice` now returns
root's own `errMissingDevice` — same message, different `error` value,
so `!=` failed even though nothing was logically wrong. This is the
exact risk class flagged for `signing`'s row-type renames: any test
holding onto the *old* package's error/type value while production code
switches to the *new* one silently breaks equality checks without a
compile error. All 13 files updated: `mentions_integration_test.go`,
`handlers_signing_test.go`, `ripples_test.go`, `reply_counts_test.go`,
`pin_reed_test.go`, `federation_relay_test.go`, `devices_test.go`,
`follow_counts_test.go`, `handlers_signup_gate_test.go`,
`federation_test.go`, `federation_handshake_test.go`,
`key_rotation_test.go`, `signup_invite_test.go`.

## Depends on

[04](04_signing.md) (`signing`, specifically `BytesToSign`)

## Context

`identity` is 1,808 lines across 3 non-test files
(`identity.go`, `identity_id.go`, `device.go`). No DB coupling, but not a
cohesive "identity" concept either: `identity.go` is a collection of
per-feature signed-payload builders — `BuildUserIdentityPayload`,
`BuildProfilePayload`, `BuildReedPayload`, `BuildPublicKeyPayload`,
`BuildUserRevocationPayload`, `BuildRippleUserPayload`/
`BuildRippleServerPayload`, and their unexported `*Headers` helpers (e.g.
`rippleServerHeaders`, `profileHeaders`) — one pair per feature that needs
something countersigned, each only meaningful in the context of that one
feature (ripples, reeds, revocations, profiles). It's the place these
builders ended up because nothing else could hold them without an import
cycle (root can't be imported; each of `realtime`/`recovery`/`deletion`/
`invites` needs some subset of these builders but can't share code with
each other except through a package none of them owns).

`identity_id.go`'s `IdentityID` type (canonical `userID@serverID[/entity]`
IDs, `CanonicalID`/`ParseIdentityID`/`AppendEntity`/etc.) is more
genuinely cohesive — but it's used in 19 files across the whole tree
(`services.go`, `handlers.go`, `middlewares.go`, `federation_relay.go`,
`root.go`, and every package this spec merges), so its scope is really
"the whole app's identity-ID format," not something separable from root
once everything else has merged into root anyway.

## Collisions

None — no root-level `func`/`type` name conflicts for anything in
`identity/`.

## Move plan

1. Moved `identity.go`, `identity_id.go`'s contents into new root files of
   the same names, keeping the payload-builders/ID-parsing split. Every
   exported symbol went unexported and PascalCase → camelCase:
   `IdentityID`→`identityID`, `CanonicalID`→`canonicalID`,
   `ParseIdentityID`→`parseIdentityID`, `ParseKeyFingerprint`→
   `parseKeyFingerprint`, `AppendEntity`→`appendEntity`,
   `AuthorOf`→`authorOf`, every `Build*Payload`→`build*Payload`,
   `TypeReed`/`TypeAccount`/`TypeReedLike`/`TypeInviteUser`/
   `TypeInviteServer`→`identityType*` (prefixed to avoid a generic
   `type`/`typeReed` name in a 5000+ line file), `PublicKeyCountersignHeaders`→
   `publicKeyCountersignHeaders`. `device.go`'s `ParseDeviceID`/
   `ErrMissingDevice` folded into `identity_id.go` (see Status) as
   `parseDeviceID`/`errMissingDevice`. No section header on either new
   file (same reasoning as `secret`/`crypto`). `bytesToSign` (deferred
   from step 04) added to `utils.go`'s `signing` section — see Status.
2. Updated all ~19 root call sites (`services.go`, `handlers.go`,
   `middlewares.go`, `federation_relay.go`, `root.go`) to the new
   unexported names via one bulk substitution — done atomically per the
   spec's original verification note, not incrementally. Dropped
   `"syrinx/identity"` from all 5. Left `deletion`, `recovery`,
   `realtime`, `invites`, `roles` importing `"syrinx/identity"`
   unchanged — none of them merge in this step.
3. Updated all 13 affected test files the same way (see Status) —
   scope the original plan didn't anticipate, found via a real test
   failure (`TestCheckActiveDevice`), not a compile error.
4. `identity/` directory deletion deferred — `deletion` (step 07),
   `invites` (step 08), `recovery` (step 09), and `realtime` (step 10)
   still import it and haven't merged yet. It now has more remaining
   external importers than any other package in this spec until those
   steps clear them out — plus `roles` (step 06, next).

## Verification

`go build ./...`, `go vet ./...`, `go test ./...`, plus `go build -tags
ops` and `go build -tags ripplescleanup`, all pass. Did the ~19 root
production call sites as one atomic bulk substitution, not
incrementally, per the original plan's reasoning (a half-migrated
`identity` would risk the two representations silently diverging).
**The same reasoning turned out to apply to test files too, but wasn't
in the original plan** — `go build`/`go vet` passed cleanly with mixed
old/new identity references in test files (the old `identity` package
still compiles fine on its own), but `go test` caught a real behavioral
break: a test asserting `err != identity.ErrMissingDevice` against code
that now returns root's own `errMissingDevice` — same message, distinct
`error` values. `go vet` cannot catch this class of bug; only running
the tests can. Lesson for later steps: after any rename that touches an
`error` sentinel or any value compared with `==`/`!=` (not just `go
build`), always run the full test suite before considering a step done.
