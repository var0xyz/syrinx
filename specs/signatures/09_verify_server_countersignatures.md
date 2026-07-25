# Signatures 09 — Verify every signed resource before store

## Status

Implemented (`dbService.put` requires a verifier; `$lib/verifiers`;
repositories wire signed verifiers vs `allowUnsigned`; SPA
`buildProfilePayload` / `buildReedPayload`).

**Cancelled:** attested possession / proof-based `reed_allocations`.
Allocation stays ACK-after-delivery. A client can still ACK (or submit
any proof) and discard local content — same fake-holder outcome with
extra ceremony. Content integrity is verify-before-store; relay miss
remains the backstop for holders who cannot serve.

## Depends on

[08](08_wire_nested_blocks.md) (nested `userSignature` / `serverSignature`);
prerequisite [08](../08_client_signature_validation.md) (key / revoke /
deletion gates). Reed form
([01](../01_reed_countersig_canonical_form.md) /
[03](../03_reed_server_block.md)), identity
([04](../04_signed_identity_records.md) /
[05](../05_signed_profile_updates.md)), keys
([07](../07_server_signed_distributed_keys.md)).

## Rule

**Every durable store of a signed resource must verify every attestation
that resource carries before IndexedDB write.**

| Present on resource | Required check |
|---------------------|----------------|
| `userSignature` | Rebuild user payload; verify with author’s public key (`userSignature.fingerprint`) |
| `serverSignature` | Rebuild server payload; verify with that server signing key (`serverSignature.fingerprint`) |

Failure → do not write; log locally; surface failure to the caller (throw
from `dbService.put`). Callers must not `DATA_ACK` / treat the resource
as held on failure. No resource is exempt because “we trust the HTTP
path” or “we just published it.”

Server-only resources (distributed public keys) have no `userSignature`;
verify the server attestation (and existing armor/fingerprint /
predecessor checks). Do not invent a user signature where the wire has
none.

## Architecture — verifier argument on `dbService.put`

**Locked decision:** one `dbService.put` API. Every write passes a
**verifier** callback. `dbService` calls it before writing; on failure it
does not store. Entity crypto knowledge does **not** live in `dbService`
or in repositories.

```text
feature code
    │
    ▼
repository.put(resource)          ← picks which verifier to pass
    │
    ▼
dbService.put(store, data, verifier)   ← always required; no ungated put
    │
    ├─ await verifier(data)
    ├─ on failure → refuse write, signal caller
    └─ on success → IndexedDB put
```

### Layers

| Layer | Owns |
|-------|------|
| **`verifiers` module** | How to verify each signed entity (payload rebuild, user + server checks). Export `verifyUser`, `verifyReed`, `verifyPublicKey`, … and **`allowUnsigned`**. |
| **Repository** (e.g. `UserRepository`) | Which verifier applies to this store; `put` calls `dbService.put(store, data, verifyUser)`. No crypto logic inline. |
| **`dbService`** | Call `verifier(data)` then write or refuse. No knowledge of identity / reeds / keys. |

Tests mock the verifier argument (or swap `allowUnsigned`) without
mocking OpenPGP.

### `allowUnsigned`

There is **no** second put API. Unsigned stores (follows, pending queues,
private keys, `unsignedReeds`, tombstone markers, etc.) pass
**`allowUnsigned`** — a named verifier from the same module that always
succeeds.

```ts
// signed
await dbService.put('users', user, verifyUser);

// intentionally unsigned
await dbService.put('pendingFollows', record, allowUnsigned);
```

Using `allowUnsigned` on a signed store is a visible, reviewable mistake;
prefer repositories so feature code never chooses the verifier for
`users` / `reeds` / … itself. Backup restore goes through repositories
(real verifiers), not raw `dbService.put(..., allowUnsigned)`.

### Call-site consequences

- Feature code: `await userRepository.put(user)` only — repository
  supplies `verifyUser`.
- Reed ingest / author publish: reed write path passes `verifyReed`
  (both layers).
- Removals: `removedReedsRepository.put` /
  `removedAccountsRepository.put` pass removal verifiers;
  `verifyAndCommit*` is put-then-side-effects.
- Key / revocation gates live in `verifiers`, passed through
  `dbService.put`.

## Landed resources

| Resource | User att. | Server att. | Gate |
|----------|-----------|-------------|------|
| **PublicKey** | — | yes | `verifyPublicKey` |
| **KeyRevocation** | yes | yes | `verifyKeyRevocation` |
| **ReedRemoval** | yes | yes | `verifyReedRemoval` |
| **AccountRemoval** | yes | yes | `verifyAccountRemoval` |
| **User** (identity) | yes | yes | `verifyUser` |
| **Reed** | yes (content) | yes | `verifyReed` |

Unsigned stores use `allowUnsigned` explicitly.

**Recovery:** report-back sends locally held material from gated stores.

**Backup restore:** repository `put`s with real verifiers (public keys
before dependent rows).

**Allocations:** unchanged — author at publish; peers on `DATA_ACK` after
delivery of a pending event. No spontaneous claim API.

## Non-goals

- **Attested possession** (cancelled — see Status).
- A second ungated `put` / `putVerified` split — one `put`, use
  `allowUnsigned` when intentional.
- A polymorphic verifier that switches on resource type inside
  `dbService` — `dbService` only invokes the function it was given.
- Changing canonical payload bytes / how the server countersigns.
- Revocation **fanout** ([09](../09_revocation_fanout.md)) — apply via
  `revocationRepository.put` (real verifier).
- Recovery report-back protocol itself.

## Work items

- [x] `dbService.put` requires `verifier`; `allowUnsigned`
- [x] `verifiers` module + SPA mirrors (`buildProfilePayload`,
  `buildReedPayload`)
- [x] Repositories wire signed vs `allowUnsigned`
- [x] Reed durable write both layers; author publish + peer ingest
- [x] Removals gated in repository `put`
- [x] Backup restore through repositories
- [ ] ~~Possession endpoint~~ **cancelled**

## Testing

- Unit: payload rebuild vectors (identity server, reed countersign) Go↔TS;
  `dbService.put` does not write when verifier returns false.
- Integration / e2e: signed repository `put` refuses bad sigs; peer reed
  with broken server sig never lands; profile with broken identity sig
  never caches; publish ACK tampering does not promote.

## Risks

- **`allowUnsigned` misuse** on signed stores — mitigate with repository
  boundary + code review.
- **Failure surfacing** — verifiers / `put` log which layer failed;
  distinguishable errors for backup / publish retry.

## Dependencies

- Nested wire ([08](08_wire_nested_blocks.md)); prereq
  [08](../08_client_signature_validation.md); 01/03/04/05/07.
