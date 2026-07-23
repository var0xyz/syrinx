# Signatures 08 — Nested `userSignature` / `serverSignature` wire blocks

## Status

Complete (shapes locked; implementation TBD).

## Depends on

[00](00_design.md); preferably after entity FK switches that already expose
`signedFields` ([03](03_migrate_users.md)–[06](06_migrate_reed_removals.md)),
but can be specified in parallel.

## Context

Today user attestation fields are **flattened** on the resource root
(`signature`, `signatureFingerprint`, root `signedFields`), while the
server countersignature is nested under `server`. That asymmetry is
awkward once both sides carry field manifests, and it fights the trust-tier
story already called out on `User` in `db.go` (user-signed vs
server-signed vs unsigned hints).

**Blank slate — no migration, no backwards compatibility** (see
[README](README.md) / [00](00_design.md)).

## Scope

- Nest all **user** attestation wire fields under `userSignature`.
- Rename nested **server** countersignature block from `server` to
  `serverSignature`.
- Apply across identity, keys (server only), revocations, reed/account
  removals, recovery wire, and SPA consumers — exact inventory TBD.
- Keep unsigned hints (e.g. `activeKeyFingerprint`) **outside** both
  blocks unless decided otherwise.

## Non-goals

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
    "algorithm": "md+PGP+base64",
    "armor": "…",
    "fields": ["bio", "type", "username", "fingerprint", "avatarURL"]
  },
  "serverSignature": {
    "serverID": "…",
    "fingerprint": "…",
    "algorithm": "md+PGP+base64",
    "armor": "…",
    "timestamp": "…",
    "fields": ["bio", "type", "userID", "username", "fingerprint", "avatarURL", "memberSince", "serverID", "serverKeyFingerprint", "signedAt", "userSignature"]
  }
}
```

### `fields` array order (normative)

`BytesToSign` builds:

```text
---
<header>: <value>
…
---
<content>
```

Headers are named; **content is the single unnamed body** after the
closing `---`. The wire `fields` list must encode which logical field
was that body: **index 0 is the content field name; the rest are the
header names** (same names as in the envelope / signed-fields manifests).

Examples:

| Payload | `fields[0]` (content) | Remaining (headers) |
|---------|----------------------|---------------------|
| User identity | `bio` | `type`, `username`, `fingerprint`, `avatarURL` |
| Profile (server) | `bio` | `type`, `userID`, …, `userSignature` |
| Public key | `armor` | `fingerprint`, `serverID`, … |
| User revocation | `reason` | `type`, `userID`, `fingerprint` |

Payloads with empty content still need a convention at implementation
time (omit `fields[0]` sentinel vs empty-string content field) — TBD if
any signed resource has no body today.

### Locked block shapes

**`userSignature`**

| Field | Type | Notes |
|-------|------|--------|
| `fingerprint` | string | Key that produced `armor` (was root `signatureFingerprint`) |
| `algorithm` | string | `md+PGP+base64` (was `PGP+base64`; marks BytesToSign markdown envelope + PGP + base64 wire) |
| `armor` | string | Base64(armored PGP) detached signature (was root `signature`) |
| `fields` | string[] | Cover list (was root `signedFields`); **`fields[0]` = envelope content**, rest = headers |

**`serverSignature`**

| Field | Type | Notes |
|-------|------|--------|
| `serverID` | string | Serving server id (was `server.id`) |
| `fingerprint` | string | Server signing-key fingerprint |
| `algorithm` | string | `md+PGP+base64` (same rename as user) |
| `armor` | string | Base64(armored PGP) detached countersignature (was `signature`) |
| `timestamp` | string/time | Authoritative countersign time (unchanged name) |
| `fields` | string[] | Cover list (was `signedFields`); **`fields[0]` = envelope content**, rest = headers |

### Field moves (identity)

| Today (root / `server`) | After |
|-------------------------|--------|
| `signature` | `userSignature.armor` |
| `signatureFingerprint` | `userSignature.fingerprint` |
| root `signedFields` | `userSignature.fields` |
| *(none)* | `userSignature.algorithm` |
| `server` | `serverSignature` |
| `server.id` | `serverSignature.serverID` |
| `server.signature` | `serverSignature.armor` |
| `server.signedFields` | `serverSignature.fields` |
| `server.fingerprint` / `algorithm` / `timestamp` | same names under `serverSignature` |
| `activeKeyFingerprint` | stays at root (unsigned hint) |

### Other resources (TBD)

| Resource | User block | Server block |
|----------|------------|--------------|
| Key | none today | `server` → `serverSignature` (+ inner renames) |
| KeyRevocation | root `signature` → `userSignature` | `server` → `serverSignature` |
| ReedRemoval / AccountRemoval | root `signature` → `userSignature` | `server` → `serverSignature` |
| Recovery `Profile` / nests | same as identity | same rename |
| Recovery reed request | `userSignature` string today — **conflict / rename TBD** | `server` → `serverSignature` |

## Open questions

1. ~~Does `userSignature` also grow `algorithm` (mirror server), or stay
   signature + fingerprint + signedFields only?~~
   **Resolved:** yes — `userSignature.algorithm` mirrors server.
2. ~~Algorithm string stays `PGP+base64`?~~
   **Resolved:** rename to `md+PGP+base64` (markdown envelope + PGP +
   base64). Blank-slate; update `identity.Algorithm`, DB defaults, and
   request/WS `X-Syrinx-Algorithm` / query checks in the same step.
3. ~~How does `fields` mark which name is envelope content vs headers?~~
   **Resolved:** `fields[0]` is the content field; the rest are headers.
4. Recovery `ReedRequest.userSignature` is currently a **string** (the
   armor). Nested object would collide on the name — rename the string
   field, or nest as `userSignature.armor`?
5. Exact SPA / client update checklist and landing order vs 03–06.
6. Whether `signing.UserWire` / `ServerWire` should emit these nested
   shapes directly (and main/recovery structs embed them).

## Test plan

- [ ] Spec complete (shapes locked for every signed resource)
- [ ] Server + recovery JSON tags / marshal tests
- [ ] SPA parse/verify updated in lockstep
