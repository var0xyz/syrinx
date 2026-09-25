# Attestations 06 — SPA: QR exchange, key compare, vouch

## Status

Implemented.

## Depends on

[03](03_api.md)

## Context

The out-of-band exchange: how two people in a room turn a face-to-face
meeting into a signed vouch. This is the only step where a human does
security work, so its failure mode is confusion, not cryptography.

## Scope

- The QR payload and the fragment-based link.
- The three-way comparison and what each outcome does.
- Signing and submitting the vouch.

## Non-goals

- Displaying trust elsewhere in the app ([07](07_spa_trust_display.md)).

## Design

### Who shows what

The two people meet. The **subject** opens their own profile and shows a QR
code from it, using the same `QRButton` the invites list uses. The
**scanner** opens that code, lands on the subject's vouch screen, and
confirms there once the comparison has run.

Only the subject's device knows which key it holds, so the subject publishes
the claim and the scanner checks it.

### The link

Bob's own profile renders a QR for:

```
https://home.example/profile/bob@home1234/vouch#9f3c…
```

The path names the subject; the fragment carries only the **fingerprint** of
the key they are claiming. A fragment is never sent to the server — not in
the request line, not in `Referer` — so the server never learns which
verification is happening and cannot tamper with the payload in transit.

**The scanner builds the key id itself**, from the subject in the path and
the fingerprint in the fragment. The fragment is checked to be a well-formed
fingerprint and nothing more, so it can never name a key of some other
account. The fingerprint is a hash of the key material, so it cannot be
produced for a key the subject does not hold — which is what makes a
mismatch proof of tampering.

The QR is rendered with the existing `qrCodeDataURL` / `QRCodeModal`, which
shows a copyable text form beside it — useful when scanning fails or the two
people are on a call rather than in a room. Scanning uses the device camera;
a paste field is the fallback, and both land in the same handler.

### The scanner derives the id it compares against

The scanner fetches the subject's current key and **derives** the id from the
armor it received, rather than reading the id the response labels it with.
`verifyPublicKey` already re-derives the fingerprint and refuses a mismatch,
so the vouch flow routes its fetch through that verifier rather than trusting
a bare `GET`.

Skipping this would defeat the whole comparison. A server could serve armor
for a key it controls while labelling it with the id the QR names; the
scanner would compare that label against the scanned id, find them equal, and
sign a vouch for the attacker's key. The comparison is only meaningful when
one side of it is computed from key material.

### The comparison

Alice's client parses the fragment for the scanned key id, fetches Bob's
current key, derives that key's id from its armor, and compares the two.
There are **two** outcomes:

| Outcome | Condition | What it means |
|---|---|---|
| **Same** | Derived id matches the scanned id | The server is telling the truth about Bob's key. |
| **Differs** | Derived id ≠ scanned id | **Alarm.** Either Bob rotated, or the server substituted. |

The scanner always fetches and always derives, so the comparison has both
sides every time and these two outcomes cover it. A locally cached key is a
convenience for other screens; the baseline here is always the freshly
derived id.

The key may also be **unresolvable** — the fetch failed, the server returned
no active key, or the key it returned failed `verifyPublicKey`. That blocks
the flow the same way `Differs` does, because a vouch signed without a
confirmed comparison asserts something Alice never checked.

**Differs** must stop the flow and explain, because this is the H1 detection
event. One innocent cause and one hostile one:

- Bob rotated his key and is showing the new one, while the server still
  serves the old. If the server's current key for Bob matches the scanned one
  and carries a valid predecessor handoff, this is a legitimate rotation and
  the flow proceeds against the new key ([04](04_revocation.md) — the old
  vouch goes stale, not void).
- Alice scanned the wrong person's code → the userID in the path will not
  match the scanned key id's owner, which is caught before any fetch.
- **The key the server served Alice is not the key Bob holds.** Nothing
  reconciles. This is key substitution, and the UI must say so in plain
  language, refuse the vouch, and tell Alice her view of Bob is compromised.

Do not offer "vouch anyway" on an unreconciled mismatch. If the two devices
disagree about Bob's key, a signed statement from Alice about which one is
real is worse than nothing — she would be attesting to a key she has not
actually confirmed.

### Signing and submitting

Once Alice accepts, her client:

1. Builds `buildVouchUserPayload(voucherKeyID, subjectUserID, subjectKeyID, note)`
   ([02](02_payload.md)) — using the **scanned** key id, not the served one.
   The point is to attest what Bob showed her. The note is optional, public
   plaintext, capped at 140 characters.
2. Signs it through the service worker (`requestSigner.sign`), like every
   other signature in the app.
3. Queues it durably in a `pendingVouches` outbox **before** posting
   ([03](03_api.md)), clearing the queue entry only once the cert comes back
   verified — the offline-first pattern used by `pendingLikes` and
   `pendingRemoval`, flushed from the same reconnect path. A vouch made in a
   basement with no signal survives until there is signal, and the screen
   says it will publish later rather than reporting a failure.
4. Adds Bob to local trust roots ([05](05_trust_paths.md)).

Vouching is one-directional. Bob vouching for Alice is a separate act on his
device; the UI should prompt for it ("Ask Alice to scan yours too") but never
fabricate it.

### The scanned key survives eviction

A vouch names the key id it was made against, and that id is stored on the
vouch row ([01](01_schema.md)), so the evidence of what Alice compared lives
with the vouch rather than in the key cache. A later substitution shows up as
the vouch naming one id while the server serves another.

Quota eviction drops a cached profile and its `publicKeys` row
(`eviction.ts`), and its protected set covers the viewer, the people they
follow and their list members. Vouch subjects and vouchers join that set, so
verifying someone is itself a statement of interest in them and their profile
stays put.

### Copy

More load-bearing than usual. A vouch means **"I compared fingerprints with
this person"** — not "I like them", not "they seem legitimate". If the
wording lets people vouch for accounts they have not physically verified,
the graph fills with noise and every path built on it becomes misleading.

- The screen and the subject's own QR are headed **"Verify in person"**, not
  "Trust" or "Endorse".
- The confirmation names what is being asserted and shows the key id being
  attested.
- The result reads *"You verified Bob's key"* — an act Alice performed, not a
  property Bob has.
- A mismatch says the app's key for Bob is not the one he showed, and that
  nothing was recorded.

### What is not built here

No offline/mutual-attestation protocol, no NFC, no numeric comparison
(safety-number style). A URL in a fragment plus the existing QR component is
the smallest thing that works, and it reuses code that already ships.

## Testing

- Fragment round-trips: encode → QR → scan → parse, with ids containing `@`
  and `/`.
- Fragment is absent from any network request the flow makes.
- Same and Differs each produce the right branch.
- A served key whose armor derives a different id than its label is refused,
  so a counterfeit key under a genuine id cannot read as Same.
- An unresolvable key blocks the flow rather than defaulting either way.
- A scanned id owned by another account is refused before any fetch.
- Differs-but-legitimate-rotation reconciles against the new key.
- Differs-unreconciled refuses to vouch and surfaces the alarm.
- Offline: the vouch queues, the screen reports it as saved rather than
  failed, and it submits on reconnect.
- A failed submission leaves the queue entry in place.
