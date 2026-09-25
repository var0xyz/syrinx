# Attestations 07 — SPA: vouch list, paths, key-change warnings

## Status

Proposed.

## Depends on

[05](05_trust_paths.md), [06](06_spa_verify_flow.md)

## Context

Where trust state appears once vouches exist. The guiding constraint: the
app shows **evidence**, never a verdict, and absence of evidence is never
styled as suspicion.

## Scope

- Profile trust section.
- Key-change warnings.
- The relay refusal, which is the one place trust blocks an action.

## Non-goals

- Computing paths ([05](05_trust_paths.md)).

## Design

### Profile

A trust section on the profile page, below identity:

- **Verified by you** — if a live vouch exists, with the date and a way to
  withdraw.
- **Vouched by N people**, listing names, each independently verified by
  this client ([02](02_payload.md)) before being counted.
- **Paths from your contacts**, shortest first, at most three
  ([05](05_trust_paths.md)).
- **Withdraw** control on your own vouch, signed with your current key.
- **Previously verified on an older key** — stale vouches
  ([04](04_revocation.md)), visually distinct and never summed into the
  current count.

Nothing appears for a user with no vouches — no empty state, no "unverified"
label. An unverified account is the normal state, and decorating it with a
warning trains people to ignore warnings, which would cost more than this
feature gains. Absence of a check is absence of information, not a negative
claim (see [§ The checkmark](#the-checkmark)).

### The checkmark

Two states, and the distinction is who did the verifying:

| Mark | Condition | Meaning |
|---|---|---|
| **Blue** | You hold a live vouch for this user's current key | *You* verified them |
| **Grey** | Someone else does, and you don't | Verified by someone |
| none | No live vouches for the current key | Unverified — the normal state |

This is deliberately the familiar platform shape, with one difference that
matters: the mark is not granted by an authority, it is an aggregate of what
users signed. A grey check is not an endorsement by the server — the server
cannot mint one, because it cannot forge a vouch
([00](00_design.md#threat-model)).

Grey is **not** a trust verdict, and copy must not let it read as one. On tap
it opens the vouch list ([§ Profile](#profile)) so "verified by someone"
becomes "verified by these people, and here is how they connect to you". The
mark is an entry point to evidence, not a substitute for it.

Blue requires a vouch for the user's **current** key. A vouch for a
superseded key is stale ([04](04_revocation.md)) and does not colour the
mark — otherwise a substituted key would inherit your own checkmark, which
is the exact failure this feature exists to prevent.

Because the marks need only "does a live vouch exist", they are cheap: one
indexed read per user ([01](01_schema.md)), no path walking. So unlike trust
*paths*, checkmarks can appear on list rows and feed items.

### Where paths do *not* appear

Path computation stays on profile and reed detail. A feed of 50 reeds shows
50 checkmarks (cheap) but runs no path searches
([05](05_trust_paths.md#cost-and-caching)).

### Your vouches, chronologically

A settings page listing every vouch **you** made, newest first, always
available — not only after a revocation.

Each row: who, which key, when the server countersigned it, which of your keys
signed it, and its current state (live / stale / withdrawn / unconfirmed).
Each row has a withdraw control.

Chronological order is the point. A user scanning this list is asking "did I
do all of these?", and a burst of vouches on a date they were not verifying
anyone is the signal that their key was used without them. That is the same
review the compromise prompt forces
([04](04_revocation.md#the-compromise-window)), except the user can perform it
whenever they are suspicious rather than only when they already know.

Group by signing key, so vouches made with a key the user has since rotated
away from are visually separable — the natural unit of "everything signed
during the window I am worried about".

Sort key is the **server** countersignature timestamp, not a client clock:
it is the only time in the record an attacker does not control
([02](02_payload.md#vouch-user-payload)).

Withdrawing from this list signs a withdrawal with the user's current key
([02](02_payload.md#withdrawal-payload)), and bulk withdrawal — select a
range, withdraw all — is worth having here, because the realistic use is
"everything from that week was not me".

### Key-change warning

When a contact's active key changes and this client holds a vouch for the
previous one, say so where the user will see it, once, with the three cases
kept distinct:

- **Legitimate rotation** (valid predecessor handoff, old key revoked by its
  owner): *"Bob rotated his key. Your previous verification no longer
  applies — verify again when you next see him."* Informational.
- **Unexplained change** (no valid handoff chain): *"Bob's key changed and
  the change is not signed by his previous key. Do not treat this account as
  verified."* This is the H1 alarm ([06](06_spa_verify_flow.md)) arriving
  passively rather than during a scan, and it deserves the loudest treatment
  in the app.
- **Revocation** ([09_revocation_fanout](../09_revocation_fanout.md)): the
  existing revocation path already notifies; add that vouches naming the
  revoked key are now stale ([04](04_revocation.md)).

Vouches the user *made* survive their own key changes
([04](04_revocation.md#a-vouch-belongs-to-the-person-not-the-key)), so no
re-vouching prompt is needed after an ordinary rotation.

The exception is a revocation the user signed as a **compromise**. Then show
every vouch that key signed and ask them to confirm or withdraw each, because
some may be the attacker's rather than theirs
([04](04_revocation.md#the-compromise-window)). Until confirmed they render as
unconfirmed and seed no trust paths. This is the one prompt, and it fires only
on the user's own signed statement that the key was stolen.

### Relay refusal

The one place trust changes behaviour rather than decorating it.

Reed relay encrypts content to whatever key the server reports as the
requester's active key (`relayDecrypt.ts`). If this client holds a **live,
non-void vouch naming a different key** for that user, it must refuse to
encrypt and surface the mismatch instead.

This is the concrete payoff of the whole spec. Everything else is display;
this is the step that stops a substituted key from receiving plaintext the
[content privacy](../content_privacy/README.md) model promises the server
will never see.

Refuse only on a **contradiction** — a vouch for a different key. Absence of
a vouch is not grounds to refuse; that would break relay for the majority
who have verified nobody.

### Visual language

- **Blue check** — you verified this key.
- **Grey check** — others have; tap for who.
- **Stale** — muted, labelled *previous key*, never a check.
- **Unconfirmed** — a vouch signed by a key later revoked as compromised
  ([04](04_revocation.md)); shown in the list, excluded from the marks.
- **Mismatch** — the app's error treatment, never a badge.

Never a lock icon (borrowed meaning from transport security, a different
promise) and never a colour-only distinction: blue and grey must differ in
shape or have a text label, or the two states are invisible to a
colour-blind user and identical in a screenshot.

## Testing

- Vouch list counts only independently verified, non-void vouches.
- A server-supplied `void: true` on a vouch the client can verify as live is
  ignored ([04](04_revocation.md)).
- Stale vouches never merge into the current count.
- Legitimate rotation and unexplained change produce different warnings.
- Relay refuses on a contradicting vouch and proceeds when none exists.
- No path computation runs while scrolling a feed.
- Blue only for a live vouch on the *current* key; a stale vouch yields no
  mark.
- Grey when others vouch and you do not; blue takes precedence over grey.
- Checkmarks render on feed rows without triggering path searches.
- Blue and grey are distinguishable without colour.
- The vouch list orders by server timestamp and survives a key rotation,
  grouping by the signing key.
- Bulk withdrawal signs one withdrawal cert per vouch.
