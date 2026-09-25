# Attestations 04 — Withdrawal, revocation, and what survives

## Status

Proposed.

## Depends on

[01](01_schema.md), [02](02_payload.md)

## Context

A vouch names two keys — the voucher's and the subject's. This step defines
what each key's revocation does and does not do to the vouch. The short
version: only the voucher retracts a vouch, and they do it by signing a
withdrawal.

## Scope

- When a vouch is void, stale, or unaffected.
- Why the voucher's key state does not void their vouches.
- The compromised-key window and how it is surfaced instead.
- Where the rules are evaluated.

## Non-goals

- UI for any of it ([07](07_spa_trust_display.md)).

## Design

### The rules

A vouch is **void** when:

1. The voucher **withdrew** it (`withdrawn_at` set) — the only thing that
   retracts a vouch.

A vouch is **stale** (not void, but not current) when:

2. The **subject key** is revoked or superseded by a rotation.

A vouch is **unaffected** by:

3. The **voucher's** own key being revoked or rotated.

### A vouch belongs to the person, not the key

Rule 3 is the load-bearing decision. The key is the pen, not the author: it
was the *user* who compared fingerprints in a room, and rotating or revoking
their signing key does not unmake that act. Only the user can retract what
they asserted, by signing a withdrawal ([02](02_payload.md)) with whatever
key is current for them at the time.

The alternative — auto-voiding every vouch a revoked key ever made — was
considered and rejected. It punishes every honest rotation to defend against
a rare compromise: a user who rotates for hygiene would silently invalidate
their entire verification history, and every contact would have to re-verify
in person. That cost is certain and recurring; the attack it prevents is
occasional. Worse, a mechanism that regularly destroys legitimate trust
teaches users that trust state is noise.

So a vouch signed by key A stays valid after A is revoked and B becomes
current. Verification of the vouch still uses **A** — the key named in
`voucher_key_id` ([01](01_schema.md)) and in the signed payload — because
that is the key that produced the signature. `verifyPublicKey` resolves
revoked keys fine; a revoked key verifies its old signatures correctly, it
just may not make new ones.

### The compromise window

There is a residual risk, and it is worth stating rather than losing.

If a key was revoked *because it was stolen*, some vouches attributed to that
key may have been minted by the attacker rather than the user — for keys the
attacker controls. Under rule 3 those survive until manually withdrawn, and
the user has no reliable way to remember which vouches were theirs.

This is **not** handled by auto-voiding, for the reasons above. It is handled
by making it visible and easy to act on:

- `KeyRevocation.reason` is user-signed and already exists. When a revocation
  indicates compromise, clients viewing a vouch signed by that key show it as
  **unconfirmed**: still displayed, not counted toward a current trust path
  ([05](05_trust_paths.md)) until the voucher re-affirms or withdraws it.
- After revoking a key, the app shows the user every vouch that key signed
  and asks them to confirm or withdraw each ([07](07_spa_trust_display.md)).
  Re-affirming is a fresh vouch signed by the new key; withdrawing is a
  withdrawal cert.
- A compromise-flagged revocation is the one case where the user is prompted
  rather than merely informed.

The distinction from auto-voiding matters: the default is that trust
survives, and only a *signed statement of compromise by the user themselves*
downgrades it — never the server, and never a mechanism the server can
trigger.

### Subject key revoked or rotated → stale

Rule 2 is about the other side of the edge, and here the key genuinely is the
subject of the assertion. A vouch says "this person holds *this key*"
([00](00_design.md#a-vouch-binds-a-key)); once that key is revoked or
superseded, the statement no longer describes the person's current key.

Stale vouches are displayed as a weaker, clearly separate signal —
"previously verified on an older key" — and never summed into the current
count. The distinction matters because rotation is the natural cover for
substitution ([RISKS H1](../../RISKS.md)); a UI that rolled stale vouches
into the current number would erase exactly the anomaly a user needs to see.

Revoked and rotated are both stale rather than void because both leave the
*person* verified-at-some-point, which is real information. The difference is
that a rotation has a cryptographic handoff linking old key to new
(`predRevocation.successorSignature`), so "the same person, new key" is
provable; a bare revocation with no successor is not.

### Where the rules run

**On the client, always.** The server computes `void`/`voidReason` as a hint
on `GET` ([03](03_api.md)), but a client that trusts that hint has handed the
server the power to nullify inconvenient vouches by marking them void — a
suppression attack cheaper than the key substitution this whole feature
exists to detect.

A client evaluating a vouch already holds what it needs: it fetched and
verified both keys via `verifyPublicKey`, which resolves revocations
(`resolvePredecessorRevocation`) as part of its normal path. Applying these
rules costs nothing extra.

The server still refuses at **create** time to store a vouch naming a
revoked *subject* key ([02](02_payload.md)) — not as a trust boundary, but to
avoid storing rows that are stale on arrival. It does not care about the
voucher's key state beyond it being able to sign.

### No cascade job

Revoking a key does **not** sweep `user_vouches`. Void is derived
([01](01_schema.md#void-is-derived-not-stored)), so there is nothing to
update, and a sweep would introduce a second source of truth that can drift
from the revocation table. Revocation already fans out
([09_revocation_fanout](../09_revocation_fanout.md)); clients recompute on
next read.

## Testing

- Voucher's key revoked → vouch still valid and still counted.
- Voucher's key revoked, signature verified against the **old** key → passes.
- Voucher rotates, then withdraws with the **new** key → withdrawal accepted.
- Subject key revoked → stale, not void; excluded from current count,
  present as "previously verified".
- Subject rotates with valid handoff → stale, and the handoff chain is shown.
- Revocation whose signed `reason` indicates compromise → vouches signed by
  that key render as unconfirmed and do not seed trust paths.
- Withdrawn vouch never counts regardless of key state.
- Client ignores a server `void: true` hint it cannot independently confirm.
