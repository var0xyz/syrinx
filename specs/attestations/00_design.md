# Attestations 00 — Design, threat model, locked decisions

## Status

Proposed.

## Depends on

—

## Context

[RISKS.md H1](../../RISKS.md): the server is the sole authority binding keys
to identities. See [README](README.md).

**Blank slate — no migration, no backwards compatibility.** Pre-launch;
server and SPA ship in lockstep.

## Scope

- What a vouch asserts, and what it deliberately does not.
- Why vouches are public and key-bound.
- What the mechanism does and does not defend against.

## Non-goals

- Schema, payloads, API, UI (01–07).
- Replacing server attestation; see [README](README.md#non-goals).

## Threat model

The adversary is **the server**, or anyone who has taken it over. It is
honest-but-curious at minimum and actively malicious at worst. It:

- serves every client's public keys, so it chooses which key is "Alice's";
- countersigns identity records, so its attestation always verifies;
- sees who talks to whom, so it knows which substitutions are worth making.

It cannot:

- forge a user's detached signature (it has no private key);
- change its own identity — clients pin the server key out of band on first
  run (`serverKeyTrust.ts`);
- alter a vouch without invalidating the voucher's signature.

That last point is the whole lever. A vouch is signed by a user, so the
server can **suppress** it or **decline to serve** it, but it cannot
manufacture one. Suppression is detectable in ways forgery is not: the
voucher knows what they signed, and can see whether others see it.

### What this defends against

A server that substitutes a key for Alice now has to contend with evidence
it never controlled. Everyone who verified Alice out of band holds a signed
statement naming her *real* key fingerprint. The substituted key has no
vouches — and cannot acquire any, because acquiring one requires a human to
compare fingerprints in person. A user who has verified Alice sees the
mismatch immediately ([06](06_spa_verify_flow.md)); a user who has not sees
that a previously well-vouched contact now presents an unvouched key.

### What this does not defend against

Be explicit, because a half-understood trust system is worse than none:

- **A user who never verifies anyone.** They are exactly as exposed as
  before. This is opt-in evidence, not an automatic shield.
- **Careless vouching.** A vouch means "I compared fingerprints." If people
  vouch for accounts they have not actually verified, the graph is noise. UI
  copy matters more than usual here ([06](06_spa_verify_flow.md)).
- **A malicious voucher.** Someone can honestly verify a key that belongs to
  a person misrepresenting themselves. A vouch attests key ownership, not
  that the person is who they socially claim to be.
- **Suppression at first contact.** If the server hides all of Alice's
  vouches from a user who has never seen her before, that user sees an
  unverified account — indistinguishable from a genuinely new one. Vouches
  raise the cost of substitution; they do not make it impossible.
- **Traffic analysis.** Public vouches are a social graph. See
  [§ Why vouches are public](#why-vouches-are-public).

## A vouch binds a key

A vouch names **both** `userID` and `keyID`:

> `alice@home` asserts: `bob@peer` holds key `bob@peer/9f3c…`

Binding the person alone would be easier to live with — it would survive
rotation — but it defeats the purpose. If a vouch said only "Bob is real",
a substituted key would inherit every vouch Bob ever collected, and the
attack H1 describes would run unimpeded behind a wall of green checkmarks.
The key is the thing under attack, so the key is the thing vouched for.

### Rotation ends a vouch

A vouch does **not** transfer to a successor key. Syrinx has a predecessor
handoff chain (`verifyPublicKey`, `predRevocation.successorSignature`), and
it would be technically easy to walk it and carry vouches forward
automatically.

We deliberately do not, because the handoff chain proves *the old key
approved the new one* — which is exactly what an attacker obtains if they
compromise the old key. Carrying vouches across rotation would let one key
compromise silently inherit an entire verification history. Rotation is
also the natural disguise for substitution ([RISKS H1](../../RISKS.md)), so
it is the last place to be permissive.

The client's job is to make this bearable, not invisible: after rotating,
show the user who had vouched for their old key so they can re-verify, and
on the viewing side show "was verified by N people on a previous key" as a
distinct, weaker signal than a current vouch ([07](07_spa_trust_display.md)).
Note this concerns vouches *received*; vouches the user *made* are unaffected
by their own rotation.

### Revocation does not retract a vouch

Only the voucher retracts a vouch, by signing a withdrawal. Their own key
being revoked or rotated does not: the key is the pen, not the author, and it
was the *person* who compared fingerprints. See
[04](04_revocation.md#a-vouch-belongs-to-the-person-not-the-key) for the full
argument and for the compromised-key window this deliberately leaves open.

A revoked or rotated **subject** key does make a vouch stale, since there the
key is what the statement is about. Stale rows are retained for the
"previously verified" signal, never deleted.

## Why vouches are public

Public vouches leak a social graph — who has met whom — and that is a real
cost, especially for a project whose [content privacy](../content_privacy/README.md)
direction is to keep the server ignorant.

They are public anyway because the alternative does not work. Trust chains
require reading other people's vouches; a private vouch is visible only to
the two parties, which reduces the feature to direct verification and
discards the transitivity that motivates it. There is no cryptographic
trick that lets a client walk a graph it cannot see, short of PIR or
similar, which is far out of proportion here.

Two mitigations, both deferred but worth recording:

- The server already knows most of this graph from traffic patterns, so the
  marginal leak is smaller than it first appears.
- A future "unlisted vouch" — counted in the voucher's own path computation
  but not served publicly — would let cautious users contribute to their own
  safety without publishing a contact. Not in v1.

**Trust roots stay local regardless.** Which vouchers *you* weight is
computed on your device and never uploaded ([05](05_trust_paths.md)). The
public part is the edge; the interpretation is private.

## Why not PGP's trust model

PGP has marginal/full trust with thresholds (one full or three marginal).
It is battle-tested and it is also widely regarded as the reason the web of
trust failed to reach ordinary users: the levels are opaque, the thresholds
are arbitrary, and the resulting "valid/invalid" verdict hides the reasoning
that produced it.

This spec surfaces **paths, not verdicts**: "verified by Carol and Dave, who
you verified" is something a person can evaluate. "Trust level: marginal" is
not. See [05](05_trust_paths.md#no-scores).

## Relationship to the rest of the system

- **Signatures.** Vouches use the shared `user_signatures`/
  `server_signatures` FK model ([signatures 00](../signatures/00_design.md)),
  not inline columns.
- **Likes.** The closest existing analogue — a contentless signed assertion
  about another object, countersigned once, idempotent
  ([likes 00](../likes/00_design.md)). Follow its shape where possible.
- **Content privacy.** Relay encrypts to the requester's active key
  (`relayDecrypt.ts`). A client that has a vouch for a different key for
  that user should refuse to encrypt and warn, which is where this spec
  produces concrete security benefit rather than a badge
  ([07](07_spa_trust_display.md)).
- **Single-device model.** One active key per user at a time, so a vouch has
  exactly one current key to name.
