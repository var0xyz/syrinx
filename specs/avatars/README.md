# Custom avatars (hash + server-processed PNG)

Display uses a **custom avatar** when the signed profile carries an
`avatarHash` and the client holds (or can fetch) the matching PNG; otherwise
the **identicon** from user ID
([`identicon.ts`](../../spa/src/lib/utils/identicon.ts)).

The server processes uploads (square check, 256×256, 256-color PNG),
attests `(userID, hash)`, and stores at most one avatar per user. The
signed identity references **only the hash**. Bytes travel on process /
profile write / `GET /avatars/<hash>` — not inside `BytesToSign`.

**Blank slate** for schema and identity headers: drop `users.avatar_url` /
wire `avatarURL`; recreate DB as needed.

| # | Title | Depends on |
|---|-------|------------|
| [00](00_design.md) | Design + locked model | — |
| [01](01_schema_and_identity.md) | `avatars` table + `avatarHash` in identity | 00 |
| [02](02_process_api.md) | Authenticated process endpoint | 01 |
| [03](03_profile_api.md) | Profile PUT: set / keep / clear | 01, 02 |
| [04](04_fetch_api.md) | `GET /avatars/<hash>` | 01 |
| [05](05_spa.md) | Crop UI, IndexedDB, fetch/GC, Avatar | 02–04 |

## Locked decisions

| Topic | Decision |
|-------|----------|
| Signed identity | `avatarHash` (hex SHA-256 of stored PNG); omit when empty |
| Bytes in signature | Never — hash only |
| Process | Client crops square; server rejects non-square; resize to 256×256; 256-color PNG; return bytes + server attestation |
| Attestation | Server signs canonical payload including **userID** + **hash** of optimized PNG |
| Profile PUT (change) | Optimized bytes + process attestation + full signed profile (hash in profile) |
| Profile PUT (unchanged) | Same `avatarHash`, **no** image bytes |
| Profile PUT (clear) | Empty hash in profile + null/absent image → delete `avatars` row |
| Storage | `avatars` PK `user_id` (one active); index on `hash`; PNG `BYTEA` |
| Fetch | `GET /avatars/<hash>`; old hash 404s after replace |
| Client cache | IndexedDB by `userId` + `hash`; after each server profile load, fetch `/avatars/<hash>` if missing; GC other hashes for that user; failed fetch retries on next profile open |
| Fallback | Identicon from user ID |
| Distribution | Server for now; hash is the id for a later peer/relay step |

## Status

**Proposed** (00–05). Prior identicon-only steps (old 01–02) are superseded
by this design; identicon remains the fallback renderer.
