# Trust model

Syrinx assumes the server is useful and usually honest—but **not** an oracle you must believe blindly. Clients verify cryptographic attestations. The goal is to make certain attacks expensive enough that they stop being worthwhile.

## Threat posture

| We aim to raise the cost of… | How |
|------------------------------|-----|
| Forging network-wide content wipes | Removals require **author** signatures + **server** countersignatures; holders verify both |
| Impersonating a user after stealing only server DB rows | User keys live on devices; publishing needs the author’s key |
| Quietly rewriting “what the server says happened” | Important records are user-signed and server-countersigned with canonical payloads |
| Open spam registration on a community instance | Invite / closed signup modes without phone/email |
| Stealing or hijacking **pending** invite links from server storage | Server stores **SHA-256(secret)** only; redeem needs the preimage; secret is fragment-only at share time |
| Pushing unsolicited reed bodies onto clients | Pending-event ledger + client request correlation; clients only keep what they agreed to hold |

| We do **not** claim to stop… | Why |
|------------------------------|-----|
| Physical coercion of a key holder | [Wrench attacks](https://xkcd.com/538/) exist; no crypto fixes that |
| A holder who already saw content keeping a copy | Permanence after display is honest physics |
| Perfect anonymity against a global network observer | That’s a different product class |

## Two signatures, two trust domains

Many sensitive resources carry **both**:

| Layer | Who signs | Meaning |
|-------|-----------|---------|
| **User signature** | The author’s active user key | “I attest this profile / reed / revocation / removal” |
| **Server countersignature** | The instance’s signing key | “This server witnessed this at this time / bound these IDs” |

Clients **must** verify both where the protocol requires it. Either failure → ignore the event; keep local data. A compromised server that invents deletion events without user keys cannot cheaply empty every device.

## Authentication is possession of keys

Day-to-day use is **not** “password against the API.”

- Your **private key** lives on the device.
- A **passphrase** unlocks that key for signing (requests, reeds, certificates).
- API calls that need auth carry **detached PGP signatures** over canonical request material.
- Losing the key (and backups) loses the account. There is no central password reset that recreates encrypted material you no longer hold.

Backup files use their own password; that is separate from day-to-day unlock.

## Invites

Closed communities need a gate that does not depend on email or phone verification. Invites are **opaque capabilities**: whoever holds the secret can redeem once. The design question is what an attacker with **database access** learns.

### Secret on the client, hash on the server

When an inviter creates an invite, the browser mints a ≥256-bit secret and sends only `tokenHash = SHA-256(secret)` to `POST /api/invites`. The raw secret never appears in that request. The inviter keeps it in IndexedDB and shares it out-of-band in a link:

```
/signup?iid={id}&uid={creatorId}#{secret}
```

The fragment is not sent to the server on navigation, so normal request logs do not capture the redeem credential at click time.

The server row holds `token_hash`, claim/revoke state, and who created the invite—not the secret. When a client presents a secret at redeem or preflight check time, the server hashes it and compares to that column.

### Why that blocks hijack from DB theft

An attacker who exfiltrates Postgres sees hashes, not invite URLs. They cannot:

- Reconstruct a valid secret from `token_hash` (SHA-256 preimage resistance for full-entropy tokens).
- Redeem a pending invite without the secret that hashes to the stored value.
- Create an invite as another user without that user’s signing key.

So “invites are stored on the server” does **not** mean “the operator can take any unused invite and use it.” Operational rows are redeem **slots**; the capability stays with whoever received the link.

Remaining risks are the usual ones outside this model: the inviter leaks the link, malware on the inviter’s device reads IndexedDB, or an attacker with **write** access revokes or disrupts bookkeeping. Those are different threats from hash-only storage defeating passive DB compromise.

When someone opens an invite link, the signup page may call `/api/invites/check` before key generation so bad links fail fast. Signup itself is authoritative—a failed check does not replace redeem validation, and signup still rejects invalid tokens if the check was skipped or unreachable.

### Single-use redeem

Signup consumes an invite in the same step as user creation. If signup fails, the invite is not burned. Two simultaneous redeems of one link race; only one wins. After claim, the row stays for audit but cannot be used again.

### What is signed vs what is not

Invite **create** is user-signed and server-countersigned over id, `createdAt`, and `tokenHash`—attesting that a specific member minted a specific slot. The **redeem secret** itself is not a signed envelope; possession is the gate. The durable outcome after signup is **`invitedBy`** on the countersigned identity record.

See [Invites](/invites) for the full lifecycle, UI surfaces, and mode matrix.

## What the server is trusted for

The server **is** trusted to:

- Allocate identifiers and timestamps that enter countersigned records
- Route realtime events and in-transit relay
- Enforce signup policy and invite quotas
- Hold **public** key material and metadata needed for discovery and recovery bookkeeping
- Countersign records so peers share a common “this instance witnessed X” anchor

The server is **not** trusted to:

- Be the sole copy of your content
- Unilaterally prove that a deletion is legitimate without an author signature
- Recover your private key if you lose it

## Recovery and distrust of empty databases

If an instance is destroyed or taken over, a new host can import the **server identity** (key history) and enter **recovery mode**. Peers then **report back** signed identities and holdings they already verified. The empty database is not trusted; the network’s signed evidence is. See [Identity, invites & recovery](/identity).

## Content consent and relay

Reed bodies are not served from a CDN. They move holder → server (in transit) → requester, under a **pending-event** ledger:

- The server creates an event row **before** it asks anyone to relay.
- `RELAY_RESPONSE` / `RELAY_MISS` only matter if they cite a real `event_id`. A forged response with a made-up id is ignored.
- The client keeps the **`request_id`** it minted. Acks and data for unknown ids are dropped—the client never asked for them.

**Agree** means an action: follow someone, or open a reed. Broadcast may be watched as ephemeral session data; it does not automatically enter IndexedDB. If something unsolicited still arrives, a correct client silently refuses to keep it.

Signatures still gate trust: verify before store. See [Content distribution](/content) for the full path and guardrail steps.
