# Attestations 06 — SPA: QR exchange, fingerprint compare, vouch

## Status

Proposed.

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

### The link

Bob's client shows a QR encoding a URL to his own origin:

```
https://home.example/verify#v1.<base64url(userID)>.<base64url(keyID)>.<base64url(fingerprint)>
```

**Everything identifying is in the fragment.** A fragment is never sent to
the server — not in the request line, not in `Referer` — so the key Alice
scans reaches her client without the server seeing which verification is
happening or being able to tamper with the payload in transit. The path
(`/verify`) is a static route that the existing service worker already
serves from precache.

`v1.` prefixes the payload so a future format change is detectable rather
than silently misparsed. Base64url avoids `+`/`/` needing escaping inside a
fragment.

The QR is rendered with the existing `qrCodeDataURL` / `QRCodeModal`, which
already takes a URL and shows a copyable text form beside it — useful when
scanning fails or when the two people are on a call rather than in a room.

Scanning uses the device camera via the browser; a paste field is the
fallback, and both land in the same handler.

### The comparison

Alice's client parses the fragment and compares the scanned `keyID` and
fingerprint against what it holds for that `userID` — the locally cached
public key, or a fresh fetch if it has none. Three outcomes, and the whole
feature turns on distinguishing them:

| Outcome | Condition | What it means |
|---|---|---|
| **New** | No local key for this user | First contact. Nothing to contradict. |
| **Same** | Scanned key matches the held key | The server has been telling the truth about Bob. |
| **Differs** | Scanned key ≠ held key | **Alarm.** Either Bob rotated, or the server substituted. |

**New** and **Same** both proceed to vouching, but the copy differs:
"Same" can say the key matches what this app already had, which is genuine
reassurance. "New" cannot, and must not imply verification has occurred
beyond what Alice actually did.

**Differs** must stop the flow and explain, because this is the H1 detection
event. Two innocent causes and one hostile one:

- Bob rotated his key and Alice's cache is stale → refetch; if the server's
  current key for Bob matches the scanned one and carries a valid predecessor
  handoff, this is a legitimate rotation and the flow continues as "New" for
  the new key ([04](04_revocation.md) — the old vouch goes stale, not void).
- Alice scanned the wrong person's code → the userID will not match either.
- **The key the server served Alice is not the key Bob holds.** Nothing
  reconciles. This is key substitution, and the UI must say so in plain
  language, refuse the vouch, and tell Alice her view of Bob is compromised.

Do not offer "vouch anyway" on an unreconciled mismatch. If the two devices
disagree about Bob's key, a signed statement from Alice about which one is
real is worse than nothing — she would be attesting to a key she has not
actually confirmed.

### Signing and submitting

Once Alice accepts, her client:

1. Builds `buildVouchUserPayload(voucherKeyID, subjectUserID, subjectKeyID)`
   ([02](02_payload.md)) — using the **scanned** key id, not the served one.
   The point is to attest what Bob showed her.
2. Signs it through the service worker (`requestSigner.sign`), like every
   other signature in the app.
3. Queues it durably in a `pendingVouches` outbox, then `POST`s
   ([03](03_api.md)), reconciling on success — the offline-first pattern used
   by `pendingLikes` and `pendingRemoval`. A vouch made in a basement with no
   signal must survive until there is signal.
4. Adds Bob to local trust roots ([05](05_trust_paths.md)).

Vouching is one-directional. Bob vouching for Alice is a separate act on his
device; the UI should prompt for it ("Ask Alice to scan yours too") but never
fabricate it.

### Copy

More load-bearing than usual. A vouch means **"I compared fingerprints with
this person"** — not "I like them", not "they seem legitimate". If the
wording lets people vouch for accounts they have not physically verified,
the graph fills with noise and every path built on it becomes misleading.

- Button: **"Verify in person"**, not "Trust" or "Endorse".
- Confirmation names what is being asserted, with the fingerprint visible.
- The result is described as *"You verified Bob's key"* — an act Alice
  performed, not a property Bob has.

### What is not built here

No offline/mutual-attestation protocol, no NFC, no numeric comparison
(safety-number style). A URL in a fragment plus the existing QR component is
the smallest thing that works, and it reuses code that already ships.

## Testing

- Fragment round-trips: encode → QR → scan → parse, with ids containing `@`
  and `/`.
- Fragment is absent from any network request the flow makes.
- New / Same / Differs each produce the right branch.
- Differs-but-legitimate-rotation reconciles after refetch.
- Differs-unreconciled refuses to vouch and surfaces the alarm.
- `v2.` payload is rejected cleanly by a v1 client.
- Offline: vouch queues and submits on reconnect.
