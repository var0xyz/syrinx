# Signatures 08 — Nested `userSignature` / `serverSignature` wire blocks

## Status

Implemented.

## Depends on

[00](00_design.md); preferably after entity FK switches
([03](03_migrate_users.md)–[06](06_migrate_reed_removals.md)),
but can be specified in parallel.

## Context

User attestation fields were **flattened** on the resource root
(`signature`, `signatureFingerprint`), while the server countersignature
was nested under `server`. That asymmetry fights the trust-tier story
already called out on `User` in `db.go` (user-signed vs server-signed vs
unsigned hints).

**Blank slate — no migration, no backwards compatibility** (see
[README](README.md) / [00](00_design.md)).

## Scope

- Nest all **user** attestation wire fields under `userSignature`.
- Rename nested **server** countersignature block from `server` to
  `serverSignature`.
- Apply across identity, keys (server only), revocations, reed/account
  removals, recovery wire, and SPA consumers.
- Keep unsigned hints (e.g. `activeKeyFingerprint`) **outside** both
  blocks.

## Non-goals

- Changing canonical signed payload bytes / `BytesToSign` structure
  beyond dropping unused attestation metadata.
- Dual-write or old-client support.

## Why no `algorithm` or `fields` on the wire (or in the signature tables)

Earlier drafts carried informational `algorithm` and `fields` /
`signedFields` on every signature block (and matching DB columns). Nothing
consumes them for verify or display: clients already know the scheme from
out-of-band convention and rebuild `BytesToSign` from the resource fields
they care about. Keeping those columns means extra DDL, insert/load paths,
JSON on every response, and IndexedDB weight on constrained clients — for
dead weight. They are omitted entirely from wire and from
`user_signatures` / `server_signatures`. The same applies to reed markdown
and reed countersign headers: no `algorithm` header there either. Request
and WebSocket auth also do not negotiate an algorithm parameter.

## Design

### Before (identity / profile)

```json
{
  "id": "…",
  "username": "alice",
  "signature": "…",
  "signatureFingerprint": "…",
  "activeKeyFingerprint": "…",
  "server": {
    "id": "…",
    "fingerprint": "…",
    "algorithm": "PGP+base64",
    "signature": "…",
    "timestamp": "…"
  }
}
```

### After (target)

```json
{
  "id": "…",
  "username": "alice",
  "activeKeyFingerprint": "…",
  "userSignature": {
    "fingerprint": "…",
    "armor": "…"
  },
  "serverSignature": {
    "serverID": "…",
    "fingerprint": "…",
    "armor": "…",
    "timestamp": "…"
  }
}
```

### Locked block shapes

**`userSignature`**

| Field | Type | Notes |
|-------|------|--------|
| `fingerprint` | string | Key that produced `armor` (was root `signatureFingerprint`) |
| `armor` | string | Base64(armored PGP) detached signature (was root `signature`) |

**`serverSignature`**

| Field | Type | Notes |
|-------|------|--------|
| `serverID` | string | Serving server id (was `server.id`) |
| `fingerprint` | string | Server signing-key fingerprint |
| `armor` | string | Base64(armored PGP) detached countersignature (was `signature`) |
| `timestamp` | string/time | Authoritative countersign time |

### Field moves (identity)

| Today (root / `server`) | After |
|-------------------------|--------|
| `signature` | `userSignature.armor` |
| `signatureFingerprint` | `userSignature.fingerprint` |
| `server` | `serverSignature` |
| `server.id` | `serverSignature.serverID` |
| `server.signature` | `serverSignature.armor` |
| `server.fingerprint` / `timestamp` | same names under `serverSignature` |
| `activeKeyFingerprint` | stays at root (unsigned hint) |

### Other resources

| Resource | User block | Server block |
|----------|------------|--------------|
| Key | none | `server` → `serverSignature` |
| KeyRevocation | root → `userSignature` | `server` → `serverSignature` |
| ReedRemoval / AccountRemoval | root → `userSignature` | `server` → `serverSignature` |
| Recovery `Profile` / nests | same as identity | same |
| Recovery reed request | `userSignature.armor` | `serverSignature` |

## Test plan

- [x] Spec complete (shapes locked for every signed resource)
- [x] Server + recovery JSON tags / marshal tests
- [x] SPA parse/verify updated in lockstep
