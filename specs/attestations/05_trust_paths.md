# Attestations 05 — Trust roots and depth-1 reachability

## Status

Proposed.

## Depends on

[03](03_api.md)

## Context

"Who is trusted by people I already trust?" — the transitive part, which v1
answers one hop out. Computed entirely on the client, from vouches it has
verified itself and roots that never leave the device.

## Scope

- Trust roots and where they live.
- The depth-1 reachability check, and why v1 stops there.
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

### Depth 1 only in v1

```
depth 0: you
depth 1: your roots (people you verified in person)
```

A user is reachable when one of the vouches naming their current key was
signed by a root. That is the green check
([07](07_spa_trust_display.md#the-checkmark)), and it is the whole of trust
propagation in v1.

The computation needs no traversal and no extra endpoint. A client already
holds the subject's verified vouches from reconciliation
([07](07_spa_trust_display.md#verifying-what-the-server-reports)), and its
roots are local, so the question "did a root vouch for this user?" is a set
intersection over data already on the device. Nothing is fetched to answer
it, so it costs nothing on a feed row.

Only vouches that are live, independently verified ([02](02_payload.md)) and
naming the subject's *current* key count. Withdrawn and stale vouches
([04](04_revocation.md)) do not — but a vouch signed by a key its owner has
since rotated away from or revoked **does**, since the vouch survives their
key changes.

### Why not deeper

Depth 2 would mean "verified by Erin, who Carol verified", and it needs the
opposite edge direction: not *who vouched for this user* but *who did my root
vouch for*. That is a second, per-contact list read, and the cost compounds
with each hop — `O(roots × branching^depth)` fetches, which is why any
deeper version needs a depth cap, caching and a rule keeping it off feed
rows.

It also leaks more than depth 1 does. Asking for a contact's outbound vouches
tells the server which contact you are exploring; the depth-1 check asks
nothing it was not already going to fetch to draw the profile.

Deferred rather than rejected. Multi-hop paths are the natural next step once
the marks are proven, and nothing here forecloses them: the vouch is already
a directed edge, and adding the other direction is additive.

### No scores

The output is **names**, not a number:

> Verified by **Carol** and **Dave**, who you verified in person.

Never "trust: 87%" or "marginally trusted". [00](00_design.md#why-not-pgps-trust-model)
argues the case: a number hides the reasoning that produced it, and the
reasoning is the only part a human can actually evaluate. If a user does not
recognize the names on the path, the path should not reassure them — and a
score would.

When several roots vouched for the same user, name them all, up to a handful.

### What "no path" means

Nothing. Most users will have no path to most other users, and the UI must
not imply suspicion — an unverified account is the **normal** state, not a
warning ([README](README.md#non-goals)).

The states worth distinguishing:

| State | Meaning |
|---|---|
| Verified by you | You compared fingerprints in person |
| Verified by a root | A person you verified vouched for them — the green check ([07](07_spa_trust_display.md#the-checkmark)) |
| Verified by others | Vouched for, but by nobody you verified — grey |
| Nothing | No information — the default, not a negative |
| **Key mismatch** | You hold a vouch for a *different* key for this user |

Only the last is an alarm, and it is the one this whole feature exists to
raise ([06](06_spa_verify_flow.md), [07](07_spa_trust_display.md)).

### Why not server-side

The server could answer "is this user reachable from your contacts?" far more
efficiently — it holds the whole graph. It must not, because a server that
reports reachability controls what you see, and it could vouch for a key it
substituted itself. That is H1 reintroduced one level up, wearing the uniform
of the feature meant to answer it.

This is also why roots stay local. Uploading them would hand the server the
one input it cannot otherwise guess, and the answer it computed would be
unverifiable. The client evaluates trust itself, from signatures it checked,
against roots that never left the device.

## Testing

- A vouch from a root → green; a vouch from a stranger → grey.
- A withdrawn or stale vouch ([04](04_revocation.md)) counts for neither.
- A vouch signed by a key the voucher has since rotated away from or revoked
  still counts.
- Demoted root stops colouring green while its public vouch remains.
- The check runs against local data only and issues no fetch.
- A 50-reed feed issues no vouch requests.
