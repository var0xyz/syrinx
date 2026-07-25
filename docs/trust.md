# Trust model

Syrinx assumes the server is useful and usually honest—but **not** an oracle you must believe blindly. Clients verify cryptographic attestations. The goal is to make certain attacks expensive enough that they stop being worthwhile.

## Threat posture

| We aim to raise the cost of… | How |
|------------------------------|-----|
| Forging network-wide content wipes | Removals require **author** signatures + **server** countersignatures; holders verify both |
| Impersonating a user after stealing only server DB rows | User keys live on devices; publishing needs the author’s key |
| Quietly rewriting “what the server says happened” | Important records are user-signed and server-countersigned with canonical payloads |
| Open spam registration on a community instance | Invite / closed signup modes without phone/email |

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

## Client-side caution

Unsolicited broadcast content is treated carefully in local storage: the client avoids permanently poisoning IndexedDB with material the user never asked to keep. Explicit follows and opens are different from ambient flood. See [Content distribution](/content).
