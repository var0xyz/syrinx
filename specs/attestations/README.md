# User attestations — out-of-band verification and a web of trust

This directory is the **user attestation** feature proposal set. Numbered
files below are independently reviewable implementation steps. Land them in
order unless a step's "Depends on" says otherwise.

**Status: 00–07 implemented.** [08](08_federation.md) (federated reads) is
proposed, not built.

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
| Transitivity | **One hop, computed client-side.** A root's vouch colours the mark; deeper paths are deferred ([05](05_trust_paths.md)). |
| Trust roots | **Local.** Whose vouches you weight never leaves your device ([05](05_trust_paths.md#trust-roots-are-local)). |
| Revoking a vouch | Signed `withdrawal` cert — **both** user and server signatures — not a bare delete. Excluded from list reads; the client deletes its copy once the retraction verifies — except for its own vouches, which stay for the audit list ([03](03_api.md#delete-vouchessubjectkeyid)). |
| Re-vouching | Allowed. A new attestation with a **new id**, never a revive, behind a 24h cooldown per key ([01](01_schema.md#history-is-append-only-the-active-set-is-separate)). |
| Rendering | **Offline-first.** Marks draw from the local store immediately; reconciliation corrects them in the background ([07](07_spa_trust_display.md#verifying-what-the-server-reports)). |
| Display | **Blue** check if you verified them, **green** if someone you verified did, **grey** if anyone else did, none otherwise ([07](07_spa_trust_display.md#the-checkmark)). |
| Federation | **Cert lives on the voucher's server; the subject's server holds a reference only.** Delivery to the subject's server is confirmed before the vouch is stored, and the client retries — a deliberate break from fire-and-forget. No broadcast, no backfill ([08](08_federation.md)). |
| Audit | A chronological list of every vouch you made, always available, with withdraw — the only remedy for a compromised key, since nothing detects one. Reads the **local store**, never the server: a server list cannot be shown to be complete ([07](07_spa_trust_display.md#your-vouches-chronologically)). |

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
   has, **green** if one of them is someone the viewer verified, **blue** if
   the viewer verified Bob themselves
   ([07](07_spa_trust_display.md#the-checkmark)).
7. A client viewing Bob checks whether any of those vouchers is someone it
   verified itself, and names them — not a score.

## Steps

| #                              | Title                                                | Depends on |
|--------------------------------|------------------------------------------------------|------------|
| [00](00_design.md)             | Design, threat model, locked decisions                | —          |
| [01](01_schema.md)             | `user_vouches` schema                                 | 00         |
| [02](02_payload.md)            | Vouch + withdrawal canonical payloads, countersign    | 00         |
| [03](03_api.md)                | Create / withdraw / list API                          | 01, 02     |
| [04](04_revocation.md)         | Withdrawal, revocation, and what survives             | 01, 02     |
| [05](05_trust_paths.md)        | Trust roots and depth-1 reachability                  | 03         |
| [06](06_spa_verify_flow.md)    | SPA: QR exchange, fingerprint compare, vouch button   | 03         |
| [07](07_spa_trust_display.md)  | SPA: checkmarks, vouch audit list, key-change warnings | 05, 06     |
| [08](08_federation.md)         | Federated reads: route vouch reads to the subject's server | 03, 05, 07 |

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
- **Server-side trust computation.** The server must never be the thing that
  tells you who to trust — that reintroduces H1 one level up.
- **Multi-hop trust paths.** v1 stops at one hop; deeper paths need the
  opposite edge direction and are deferred ([05](05_trust_paths.md#why-not-deeper)).

## Resolved

- **Rate limiting.** One limit, and it is not about spraying: a 24h cooldown
  per `(voucher, subject key)` bounds withdraw/re-vouch churn now that vouch
  history is append-only ([03](03_api.md#the-re-vouch-cooldown)). General
  volume needs no limit — a user can only vouch with their own key, and a
  vouch from an account nobody trusts affects nobody's path computation.

- **The note.** A vouch carries an optional public note of at most 140
  characters, as the envelope content of the user payload. It is not
  encrypted; a private note would be invisible to exactly the people a
  public vouch exists to inform.
- **Federated account removal.** A vouch naming a subject on a peer no
  longer outlives that account's removal. Federation already notifies peers
  of account removal, and vouch cleanup rides that notify rather than adding
  a fan-out of its own ([08](08_federation.md#account-removal-across-servers)).
- **Account removal.** `voucher_user_id` references `identities` with
  `ON DELETE CASCADE`, so removing an account takes its outbound vouches
  with it, matching `reeds_liked`. Inbound vouches naming a removed local
  subject go with that subject's own identity row.
