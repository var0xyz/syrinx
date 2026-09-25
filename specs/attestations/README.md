# User attestations — out-of-band verification and a web of trust

This directory is the **user attestation** feature proposal set. Numbered
files below are independently reviewable implementation steps. Land them in
order unless a step's "Depends on" says otherwise.

**Status: Proposed.** Nothing here is implemented; this is a spec only.

## Motivation

[RISKS.md H1](../../RISKS.md) records that the server is the only directory
binding keys to people. A client checks that a key is internally consistent
and server-countersigned, but nothing ties it to the human it names except
the server's word. A compromised server can mint a key, attest it as
Alice's, and read anything encrypted to her — reed relay especially, which
encrypts to whatever key the server reports as active.

No purely server-side fix closes that: the server cannot be the thing that
proves the server honest. What *can* close it is users vouching for each
other directly, out of band, so a substituted key contradicts evidence the
server never controlled.

## What a vouch is

A vouch is a signed statement by one user about another:

> I, `alice@home`, verified out of band that `bob@peer` holds key
> `bob@peer/9f3c…`.

It is the simplest possible signed resource after a like — no content, just
an attestation binding one identity to one key. It is **public**: stored by
the server, served to anyone, and fanned out like any other cert. It has to
be, or trust chains cannot be walked at all.

## Locked decisions

| Topic | Decision |
|---|---|
| Visibility | **Public.** Server-stored, anyone can read who vouched for whom ([00](00_design.md#why-vouches-are-public)). |
| What is bound | **`userID` + `keyID` together.** A vouch is about a key, not just a person ([00](00_design.md#a-vouch-binds-a-key)). |
| Key rotation | A vouch **does not** carry to the successor key. Re-verification is required ([00](00_design.md#rotation-ends-a-vouch)). |
| Revocation | A vouch survives the **voucher's** key being revoked — only the voucher retracts it, by signing a withdrawal. A revoked or rotated **subject** key makes it stale ([04](04_revocation.md)). |
| Transitivity | **Depth-limited paths, computed client-side.** No trust scores, no thresholds ([05](05_trust_paths.md)). |
| Trust roots | **Local.** Whose vouches you weight never leaves your device ([05](05_trust_paths.md#trust-roots-are-local)). |
| Revoking a vouch | Signed `withdrawal` cert, not a bare delete ([03](03_api.md#withdrawing-a-vouch)). |
| Display | **Blue** check if you verified them, **grey** if someone else did, none otherwise ([07](07_spa_trust_display.md#the-checkmark)). |
| Audit | A chronological list of every vouch you made, always available, with withdraw ([07](07_spa_trust_display.md#your-vouches-chronologically)). |

## Protocol sketch

1. Alice and Bob meet out of band. Bob shows a QR encoding
   `userID` + `keyID` + key fingerprint (the existing `QRCodeModal` /
   `qrCode.ts` already does QR; this adds a payload shape).
2. Alice's client scans it and compares the fingerprint against the key the
   **server** served for Bob. A mismatch is the H1 alarm — surfaced loudly,
   and the vouch is refused.
3. On match, Alice's client signs a vouch payload and `POST`s it.
4. Server verifies Alice's signature, countersigns, stores, returns the
   cert. It cannot forge one: it does not hold Alice's key.
5. Bob's client learns of the vouch via the existing notification/WS path.
6. Any client viewing Bob can fetch his vouches, verify each signature
   independently, and show who vouched for him — a **grey** check if anyone
   has, **blue** if the viewer did it themselves
   ([07](07_spa_trust_display.md#the-checkmark)).
7. A client viewing Bob computes whether a path exists from its **own**
   verified contacts to Bob, and surfaces the path — not a score.

## Steps

| #                              | Title                                                | Depends on |
|--------------------------------|------------------------------------------------------|------------|
| [00](00_design.md)             | Design, threat model, locked decisions                | —          |
| [01](01_schema.md)             | `user_vouches` schema                                 | 00         |
| [02](02_payload.md)            | Vouch + withdrawal canonical payloads, countersign    | 00         |
| [03](03_api.md)                | Create / withdraw / list API                          | 01, 02     |
| [04](04_revocation.md)         | Withdrawal, revocation, and what survives             | 01, 02     |
| [05](05_trust_paths.md)        | Client-side path finding and trust roots              | 03         |
| [06](06_spa_verify_flow.md)    | SPA: QR exchange, fingerprint compare, vouch button   | 03         |
| [07](07_spa_trust_display.md)  | SPA: checkmarks, vouch audit list, key-change warnings | 05, 06     |

## Non-goals

- **Replacing the server's attestation.** Vouches are a second opinion
  layered on it, not a substitute. A user with no vouches is not "untrusted";
  they are unverified, which is the normal state.
- **Trust scores or automatic decisions.** Nothing is blocked, hidden, or
  ranked by trust. The app surfaces evidence; the user judges. See
  [05](05_trust_paths.md#no-scores).
- **Negative attestations.** No "I distrust this user" cert. The abuse
  surface (coordinated denunciation) is worse than the benefit, and
  withdrawal already covers "I no longer stand behind this".
- **Key transparency log.** A stronger, complementary answer to H1; out of
  scope here and independently specifiable.
- **Server-side path computation.** The server must never be the thing that
  tells you who to trust — that reintroduces H1 one level up.

## Open questions

1. Whether a vouch should carry an optional free-text note ("met at X"), and
   if so whether it is encrypted. Leaning no for v1: it is metadata leak with
   little benefit.
2. Whether to rate-limit vouch creation server-side. A user can only vouch
   with their own key, so the abuse ceiling is low, but a compromised account
   could spray vouches. See [03](03_api.md#rate-limiting).
3. How vouches interact with account removal — presumably cascade, matching
   `reeds_liked`, but confirm against [deletion](../deletion/README.md).
