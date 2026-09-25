# Attestations 05 — Client-side path finding and trust roots

## Status

Proposed.

## Depends on

[03](03_api.md)

## Context

"Who is trusted by people I already trust?" — the transitive part. Computed
entirely on the client, from vouches it has verified itself.

## Scope

- Trust roots and where they live.
- The path search and its limits.
- What is displayed.

## Non-goals

- Rendering ([07](07_spa_trust_display.md)).
- Any server involvement. See [§ Why not server-side](#why-not-server-side).

## Design

### Trust roots are local

A **root** is someone you verified yourself, face to face
([06](06_spa_verify_flow.md)). Creating a vouch adds a root automatically.

Roots are stored in a local IndexedDB store and **never uploaded**. The
public part of this system is the edge — "Alice vouched for Bob" — and that
is already on the server because it must be. Which edges *you* treat as
authoritative is interpretation, and interpretation stays on the device.

A user may also demote a root without withdrawing the public vouch: "I still
attest I verified this key, but I no longer want their judgment steering
mine." Those are different statements and deserve separate controls.

### The search

A breadth-first walk over outbound vouches
(`GET /users/{userID}/vouches/outbound`), from your roots toward the target:

```
depth 0: you
depth 1: your roots (people you verified in person)
depth 2: people your roots vouched for
depth 3: people they vouched for
```

**Maximum depth 3**, default 2. Beyond that, "trusted" stops meaning
anything a person can reason about — at depth 4 a typical social graph
includes most of the network, so a path proves little while looking
authoritative. The cap is a feature, not a performance compromise.

Only vouches that are live, independently verified ([02](02_payload.md)) and
naming the subject's *current* key form edges. Withdrawn, stale and
unconfirmed vouches ([04](04_revocation.md)) are not traversed — note an edge
signed by a key its owner has since rotated away from is still traversed, since
the vouch survives their rotation.

### Cost and caching

The walk is `O(roots × branching^depth)` fetches, which is why depth is
capped and results are cached. Cache vouch lists in IndexedDB keyed by
`(userID, direction)` with a short TTL, and recompute lazily on profile
view rather than eagerly for a feed. A feed of 50 reeds must not trigger 50
path searches — show trust state on profile and reed detail, not on every
list row.

Fetching outbound vouches for a contact tells the server you are interested
in that contact. The server largely knows this already from traffic, but it
is worth stating rather than discovering later.

### No scores

The output is a **path**, not a number:

> Verified by **Carol** and **Dave**, who you verified in person.

> Verified by **Erin**, who **Carol** verified. (2 hops)

Never "trust: 87%" or "marginally trusted". [00](00_design.md#why-not-pgps-trust-model)
argues the case: a number hides the reasoning that produced it, and the
reasoning is the only part a human can actually evaluate. If a user does not
recognize the names on the path, the path should not reassure them — and a
score would.

Ranking, when several paths exist: shortest first, then most-independent
(paths sharing no intermediate). Show at most a handful.

### What "no path" means

Nothing. Most users will have no path to most other users, and the UI must
not imply suspicion — an unverified account is the **normal** state, not a
warning ([README](README.md#non-goals)).

The states worth distinguishing:

| State | Meaning |
|---|---|
| Verified by you | You compared fingerprints in person |
| Path found | Reachable from your roots within the depth cap |
| No path | No information — the default, not a negative |
| **Key mismatch** | You hold a vouch for a *different* key for this user |

Only the last is an alarm, and it is the one this whole feature exists to
raise ([06](06_spa_verify_flow.md), [07](07_spa_trust_display.md)).

### Why not server-side

The server could compute paths far more efficiently — it holds the whole
graph. It must not, because a server that reports paths controls which ones
you see, and it could report a flattering path for a key it substituted
itself. That is H1 reintroduced one level up, wearing the uniform of the
feature meant to answer it.

The client must therefore fetch edges and verify signatures itself. The
inefficiency is the security property.

## Testing

- Direct root → depth-1 path.
- Two-hop path via one intermediate.
- Withdrawn or stale edge ([04](04_revocation.md)) is not traversed; a path
  that depends on it disappears.
- An edge signed by a key the voucher has since rotated away from is still
  traversed.
- An unconfirmed edge (voucher's key revoked as compromised) is not traversed.
- Depth cap respected; a depth-4 path is not reported.
- Demoted root stops seeding the search while its public vouch remains.
- Cycles terminate.
- 50-reed feed triggers no path searches.
