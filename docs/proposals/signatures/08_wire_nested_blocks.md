# Signatures 08 — Nested `userSignature` / `serverSignature` wire blocks

## Status

Draft (incomplete). Further instructions TBD.

## Depends on

[00](00_design.md); preferably after entity FK switches that already expose
`signedFields` ([03](03_migrate_users.md)–[06](06_migrate_reed_removals.md)),
but can be specified in parallel.

## Context

Today user attestation fields are **flattened** on the resource root
(`signature`, `signatureFingerprint`, root `signedFields`), while the
server countersignature is nested under `server`. That asymmetry is
awkward once both sides carry `signedFields`, and it fights the trust-tier
story already called out on `User` in `db.go` (user-signed vs
server-signed vs unsigned hints).

**Blank slate — no migration, no backwards compatibility** (see
[README](README.md) / [00](00_design.md)).

## Scope (draft)

- Nest all **user** attestation wire fields under `userSignature`.
- Rename nested **server** countersignature block from `server` to
  `serverSignature`.
- Apply across identity, keys (server only), revocations, reed/account
  removals, recovery wire, and SPA consumers — exact inventory TBD.
- Keep unsigned hints (e.g. `activeKeyFingerprint`) **outside** both
  blocks unless decided otherwise.

## Non-goals (draft)

- Changing canonical signed payload bytes / `BytesToSign` headers.
- Changing DB table shapes (`user_signatures` / `server_signatures`).
- Dual-write or old-client support.

## Design (sketch)

### Before (identity / profile)

```json
{
  "id": "…",
  "username": "alice",
  "signature": "…",
  "signatureFingerprint": "…",
  "signedFields": ["username", "fingerprint", "…"],
  "activeKeyFingerprint": "…",
  "server": {
    "id": "…",
    "fingerprint": "…",
    "algorithm": "PGP+base64",
    "signature": "…",
    "timestamp": "…",
    "signedFields": ["userID", "…"]
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
    "signature": "…",
    "signedFields": ["username", "fingerprint", "…"]
  },
  "serverSignature": {
    "id": "…",
    "fingerprint": "…",
    "algorithm": "PGP+base64",
    "signature": "…",
    "timestamp": "…",
    "signedFields": ["userID", "…"]
  }
}
```

### Field moves (identity)

| Today (root / `server`) | After |
|-------------------------|--------|
| `signature` | `userSignature.signature` |
| `signatureFingerprint` | `userSignature.fingerprint` |
| root `signedFields` | `userSignature.signedFields` |
| `server` | `serverSignature` (same inner fields) |
| `activeKeyFingerprint` | stays at root (unsigned hint) |

### Other resources (TBD)

| Resource | User block | Server block |
|----------|------------|--------------|
| Key | none today | `server` → `serverSignature` |
| KeyRevocation | root `signature` → `userSignature` | `server` → `serverSignature` |
| ReedRemoval / AccountRemoval | root `signature` → `userSignature` | `server` → `serverSignature` |
| Recovery `Profile` / nests | same as identity | same rename |
| Recovery reed request | `userSignature` string today — **conflict / rename TBD** | `server` → `serverSignature` |

## Open questions

1. Does `userSignature` also grow `algorithm` (mirror server), or stay
   signature + fingerprint + signedFields only?
2. Recovery `ReedRequest.userSignature` is currently a **string** (the
   armor). Nested object would collide on the name — rename the string
   field, or nest as `userSignature.signature`?
3. Exact SPA / client update checklist and landing order vs 03–06.
4. Whether `signing.UserWire` / `ServerWire` should emit these nested
   shapes directly (and main/recovery structs embed them).

## Test plan

- [ ] Spec complete (shapes locked for every signed resource)
- [ ] Server + recovery JSON tags / marshal tests
- [ ] SPA parse/verify updated in lockstep
