# Depackaging 05 — `identity`

## Status

Proposed.

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

1. Move `identity.go`, `identity_id.go`, `device.go`'s contents into root.
   Given the size (1,808 lines) and internal cohesion around one theme
   (signed-payload construction + canonical ID handling), this warrants
   its own new file(s) rather than merging into an existing one — e.g.
   `identity.go` and `identity_id.go` at the repo root, keeping the
   existing internal file split since it already separates two concerns
   (payload builders vs ID parsing) cleanly. No section header needed for
   either (new single-purpose files, same reasoning as `secret` in step
   01 and `crypto` in step 03).
2. Update the ~19 call sites currently doing `identity.IdentityID`,
   `identity.CanonicalID`, `identity.BuildReedPayload`, etc. — drop the
   `identity.` prefix and the `"syrinx/identity"` import at every site in
   root itself (`services.go`, `handlers.go`, `middlewares.go`,
   `federation_relay.go`, `root.go`). Leave `deletion`, `recovery`,
   `realtime`, `invites` importing `"syrinx/identity"` until their own
   later steps in this spec.
3. Delete the `identity/` directory only once `deletion` (step 07),
   `invites` (step 08), `recovery` (step 09), and `realtime` (step 10)
   have all landed — it has more remaining external importers than any
   other package in this spec until those steps clear them out.

## Verification

`go build ./...`, `go vet ./...`, `go test ./...` pass. Given the number
of call sites (19 files), do this as a single atomic change rather than
partial — a half-migrated `identity` (some callers using
`identity.IdentityID`, others using the bare root type) would be a
correctness risk if the two ever silently diverge in shape, even though
they'd start out identical.
