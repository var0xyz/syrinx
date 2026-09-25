# Attestations 07 — SPA: vouch list, marks, key-change warnings

## Status

Implemented.

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

- Computing reachability ([05](05_trust_paths.md)).

## Design

### Profile

A trust section on the profile page, below identity:

- **Verified by you** — if a live vouch exists, with the date and a way to
  withdraw.
- **Vouched by N people**, where N counts only vouches this client verified
  and stored itself ([§ Verifying what the server
  reports](#verifying-what-the-server-reports)) — never a server-reported
  total. Names listed.
- **Verified by people you verified** — the roots among those vouchers,
  named ([05](05_trust_paths.md)).
- **Withdraw** control on your own vouch, signed with your current key.
- **Previously verified on an older key** — stale vouches
  ([04](04_revocation.md)), visually distinct and never summed into the
  current count.

Nothing appears for a user with no vouches — no empty state, no "unverified"
label. An unverified account is the normal state, and decorating it with a
warning trains people to ignore warnings, which would cost more than this
feature gains. Absence of a check is absence of information, not a negative
claim (see [§ The checkmark](#the-checkmark)).

### Verifying what the server reports

A count of vouches is not evidence. The server chooses which rows it serves
and in what order, so "1000 vouches" is a claim about material the client has
never seen, and verifying the first page says nothing about the rest. Worse,
a server holding a thousand accounts can mint a thousand vouches whose
signatures all verify — cryptographic validity was never what made a count
meaningful.

So the client never renders a number it has not independently checked. It
keeps its own verified set instead:

1. `/users/{userID}/info` returns `vouchIDs`, the live vouch ids for that
   user ([03](03_api.md#vouch-ids-on-usersuseridinfo)).
2. The client diffs that list against the vouches it already holds in
   IndexedDB, all of which it verified when it first stored them.
3. For each id it does not recognise, it fetches the cert
   ([03](03_api.md#get-usersuseridvouchesvouchid)), runs the full verification order
   ([02](02_payload.md#verification-order-client-on-display)), and stores it
   on success. A cert that fails is stored as rejected, not retried on a
   loop, and never counted.
4. Ids that have disappeared from the list are fetched once to obtain the
   signed withdrawal, then marked withdrawn locally.

**No mark appears until that reconciliation finishes.** A profile mid-verify
shows no check rather than a provisional one, because a check that later
downgrades is worse than a check that arrives a moment late. Verification
runs in the background and only the first visit does real work; afterwards
the diff is usually empty and the marks come straight from IndexedDB.

This is what makes the marks honest: every one of them is backed by
signatures this device checked itself, so a mark means "I verified this",
never "the server said so".

### The checkmark

Four states, ordered by how close the verification is to *you*:

| Mark | Condition | Meaning |
|---|---|---|
| **Blue** | You hold a live vouch for this user's current key | *You* verified them |
| **Green** | Someone **you** verified holds one, and you don't | Verified by someone you trust |
| **Grey** | Someone else does, and neither of the above | Verified by someone |
| none | No live vouches for the current key | Unverified — the normal state |

The ladder is strict: blue outranks green, green outranks grey. Each step
describes a shorter distance from the viewer, so the mark answers "how would
I know this?" rather than "how trustworthy is this person?".

This is deliberately the familiar platform shape, with one difference that
matters: the mark is not granted by an authority, it is an aggregate of what
users signed. A grey check is not an endorsement by the server — the server
cannot mint one, because it cannot forge a vouch
([00](00_design.md#threat-model)).

Grey and green are **not** trust verdicts, and copy must not let them read as
one. On tap either opens the vouch list ([§ Profile](#profile)) so "verified
by someone" becomes "verified by these people, and here is how they connect
to you". The mark is an entry point to evidence, not a substitute for it.

Every mark requires a live vouch for the user's **current** key. A vouch for
a superseded key is stale ([04](04_revocation.md)) and colours nothing —
otherwise a substituted key would inherit your own checkmark, which is the
exact failure this feature exists to prevent.

Once a user's vouches are reconciled, all three marks are local reads. Blue
and grey are "does a verified live vouch exist"; green additionally tests
whether any voucher is one of your own roots
([05](05_trust_paths.md#trust-roots-are-local)), a set intersection rather
than a graph walk. Nothing goes past depth 1 in v1, so marks can sit on feed
rows, drawn from IndexedDB with no fetch at all.

### What a feed costs

A feed of 50 reeds shows 50 checkmarks and issues no vouch requests: each
mark is read from the verified set already in IndexedDB. Reconciliation runs
per profile visited, not per row rendered
([05](05_trust_paths.md#depth-1-only-in-v1)).

### Your vouches, chronologically

A settings page listing every vouch **you** made, newest first, always
available — not only after a revocation.

Each row: who, which key, when the server countersigned it, which of your keys
signed it, and its current state (live / stale / withdrawn).
Each row has a withdraw control.

Chronological order is the point. A user scanning this list is asking "did I
do all of these?", and a burst of vouches on a date they were not verifying
anyone is the signal that their key was used without them. This list is the
**only** remedy for that: nothing in the system detects a compromised key, so
nothing prompts the review ([04](04_revocation.md#the-compromise-window)).

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

After revoking a key, show the user every vouch that key signed so they can
withdraw what was not theirs — some may be an attacker's rather than theirs
([04](04_revocation.md#the-compromise-window)). Nothing prompts this
automatically, because nothing detects a compromise.

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
- **Green check** — someone you verified did; tap for who.
- **Grey check** — someone else did; tap for who.
- **Stale** — muted, labelled *previous key*, never a check.
- **Mismatch** — the app's error treatment, never a badge.

Never a lock icon (borrowed meaning from transport security, a different
promise) and never a colour-only distinction: blue, green and grey must
differ in shape or carry a text label, or the three states are invisible to
a colour-blind user and identical in a screenshot.

## Testing

- Vouch list counts only independently verified, non-void vouches.
- A server-reported id whose cert fails verification is never counted, and
  the failure does not retry on a loop.
- No mark renders until reconciliation completes; a half-verified profile
  shows no check rather than a provisional one.
- A second visit with an unchanged id list performs no verification work.
- An id that vanished from the list is fetched once for its signed
  withdrawal, and is not treated as withdrawn on the omission alone.
- A thousand-vouch profile verifies each id once, then reads from local
  storage on later visits.
- A server-supplied `void: true` on a vouch the client can verify as live is
  ignored ([04](04_revocation.md)).
- Stale vouches never merge into the current count.
- Legitimate rotation and unexplained change produce different warnings.
- Relay refuses on a contradicting vouch and proceeds when none exists.
- No vouch fetching or verification runs while scrolling a feed.
- Blue only for a live vouch on the *current* key; a stale vouch yields no
  mark.
- Grey when others vouch and you do not and none of them is a root.
- Green when a voucher is someone you verified; grey when none is.
- The ladder holds: blue beats green, green beats grey.
- Green needs a live vouch on the current key from a live root vouch; a
  stale vouch on either edge yields no green.
- Checkmarks render on feed rows without triggering path searches.
- Blue, green and grey are distinguishable without colour.
- The vouch list orders by server timestamp and survives a key rotation,
  grouping by the signing key.
- Bulk withdrawal signs one withdrawal cert per vouch.
