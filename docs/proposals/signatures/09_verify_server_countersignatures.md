# Signatures 09 — Verify every signed resource before store (+ possession)

## Status

Proposed.

## Depends on

[08](08_wire_nested_blocks.md) (nested `userSignature` / `serverSignature`);
prerequisite [08](../08_client_signature_validation.md) (key / revoke /
deletion gates already landed). Reed form
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
or reject — one convention on `dbService.put`, stick to it). Callers must
not ACK possession / treat the resource as held on failure. No resource
is exempt because “we trust the HTTP path” or “we just published it.”

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
  (both layers). Drop call-site `validateReed`-then-store as the
  enforcement point (keep `verifyReed` in `verifiers` for `put` + tests).
- Removals: `removedReedsRepository.put` /
  `removedAccountsRepository.put` pass removal verifiers;
  `verifyAndCommit*` becomes put-then-side-effects.
- Refactor existing key / revocation `put` gates to the same shape
  (move logic into `verifiers`, pass through `dbService.put`).

## Audit (current tree)

Signed wire resources from [08](08_wire_nested_blocks.md) / `api.ts`:

| Resource | User att. | Server att. | Gated via `dbService.put(…, verifier)` today? | Gap |
|----------|-----------|-------------|-----------------------------------------------|-----|
| **PublicKey** | — | yes | Partial — verify inside repository `put`, not yet a `dbService` verifier arg | Move body to `verifiers`; pass through `dbService.put` |
| **KeyRevocation** | yes | yes | Same as PublicKey | Same |
| **ReedRemoval** | yes | yes | **No** — verify in `verifyAndCommit*`, then ungated store | `verifiers` + repository `put` → `dbService.put` |
| **AccountRemoval** | yes | yes | Same as ReedRemoval | Same |
| **User** (identity) | yes | yes | **No** | `verifyUser` + gate; add `buildProfilePayload` SPA mirror |
| **Reed** | yes (content) | yes | **No** — blind write; peer may `validateReed` (author only) outside store | `verifyReed` (both layers) on durable reed write |
| **Possession / `reed_allocations`** | n/a | server countersig over tuple | **No** — ACK-only allocation | Attested possession (below) |

Unsigned stores use `allowUnsigned` explicitly (not “no argument”).

**Recovery:** report-back sends locally held material. Once signed writes
use real verifiers, recovery only assembles verified rows.

**Backup restore:** repository `put`s with real verifiers — never
`allowUnsigned` for signed shapes.

## Scope

### 1. `dbService.put(store, data, verifier)`

- Require `verifier: (data) => Promise<boolean>` (or throw-on-fail —
  pick one and document).
- No overload without verifier.
- Export `allowUnsigned` from `verifiers`.

### 2. `verifiers` module

- One function per signed resource; owns all verification knowledge.
- Add SPA mirrors: `buildProfilePayload`, `buildReedPayload` (+ vectors).
- Migrate existing key / revocation / removal verify helpers here.

### 3. Repositories wire verifiers

- Each signed repository `put` → `dbService.put(…, verifyX)`.
- Unsigned repository writes → `dbService.put(…, allowUnsigned)`.
- Thin commit helpers for removals after gated `put`.

### 4. Attested possession (reed allocations)

After a reed’s gated `put` succeeds, report possession with the attested
tuple (`reedID`, `authorID`, nested signatures). Server verifies **its
own** countersignature and then upserts `reed_allocations`. Stop
allocating on bare `DATA_ACK` without proof.

Possession verify (server):

1. Auth → holder `userID`.
2. Rebuild countersign payload from submitted fields.
3. Load this server’s public key for `serverSignature.fingerprint`
   (historical keys allowed).
4. Verify `serverSignature.armor`.
5. Require `serverSignature.serverID ==` this server’s `serverID`.
6. Require `authorID` matches `reeds.user_id` for `reedID` when the row
   exists.
7. Upsert `reed_allocations`.

Do **not** re-verify the author signature on this path.

## Non-goals

- A second ungated `put` / `putVerified` split — one `put`, use
  `allowUnsigned` when intentional.
- A polymorphic verifier that switches on resource type inside
  `dbService` — `dbService` only invokes the function it was given.
- Changing canonical payload bytes / how the server countersigns.
- Revocation **fanout** ([09](../09_revocation_fanout.md)) — apply via
  `revocationRepository.put` (real verifier).
- Recovery report-back protocol itself.

## Work items

1. Change `dbService.put` to require `verifier`; add `allowUnsigned`.
2. Create `verifiers` module; move/add per-resource verifiers; SPA
   mirrors + vectors for profile + reed countersign.
3. Wire every repository `put` to pass the right verifier (signed or
   `allowUnsigned`).
4. Reed durable write uses `verifyReed` (both layers); author publish +
   peer ingest both go through it.
5. Removals: gated repository `put`; thin commit helpers.
6. Backup restore through repositories; sweep leftover ungated patterns.
7. Possession endpoint / realtime message; migrate off ACK-only
   allocation.
8. Tests: mock verifier on `dbService.put`; real verifiers refuse bad
   atts.; `allowUnsigned` only on unsigned stores in repository code.

## Testing

- Unit: payload rebuild vectors (identity server, reed countersign) Go↔TS;
  `dbService.put` does not write when verifier returns false / throws.
- Integration: each signed repository `put` refuses bad sigs; possession
  endpoint.
- e2e: peer reed with broken server sig never lands; profile with broken
  identity sig never caches; publish ACK tampering does not promote.

## Risks

- **`allowUnsigned` misuse** on signed stores — mitigate with repository
  boundary + code review; optional lint later.
- **dbService API churn** — every current `put` call site must pass a
  verifier in the same PR series.
- **Failure surfacing** — verifiers / `put` must log which layer failed;
  distinguishable errors for backup / publish retry.
- **Double verify** — optional early UX checks outside `put` are not
  security; drop once trusted.

## Dependencies

- Nested wire ([08](08_wire_nested_blocks.md)); prereq
  [08](../08_client_signature_validation.md); 01/03/04/05/07.

## Parallelism

- `dbService` + `allowUnsigned` plumbing can land first; per-resource
  verifiers and possession can follow as separate PRs; all required to
  call the set done.
- Independent of invites / tip check / device binding / revocation fanout.
