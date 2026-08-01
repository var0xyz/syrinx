# Avatars 05 — SPA crop, cache, display

## Status

Proposed.

## Depends on

[02](02_process_api.md), [03](03_profile_api.md), [04](04_fetch_api.md)

## Context

Identicon-only `Avatar.svelte` must show custom PNGs when available, and
profile edit needs crop + process + PUT orchestration.

## Scope

- Profile editor: pick image → if not square, crop UI → send to process →
  hold optimized bytes + attestation → include hash in signed profile and
  send bytes+attestation on PUT when hash changed; hash-only when
  unchanged; null image when clearing.
- IndexedDB `avatars` store: index `userId`, index `hash`; store PNG blob.
- Profile routes already refresh the signed profile from the server on
  open. After that profile is in hand: if hash empty → identicon; if blob
  for hash present → use it; else `GET /avatars/<hash>`, store, GC other
  hashes for that `userId`. Failed avatar fetch → identicon this visit;
  next profile open retries via the same path.
- `Avatar.svelte`: prefer local blob for `(userId, hash)` from profile;
  else identicon.

## Non-goals

- Server-side crop.
- Relay-based fetch.

## Work

1. Crop UX (square, min width ≥ 80px guidance; prefer ~500px source before
   process).
2. Repository helpers for put/get/delete-by-user.
3. Hook profile refresh / WS profile updates into fetch+GC.
4. Clear-avatar control on edit.
5. Tests: identicon stable; cache hit skips network; GC drops old hash.

## Acceptance

- Set / keep / clear work from the profile UI.
- Feed and profile surfaces show custom avatar once cached.
- Missing/404 hash falls back to identicon without breaking the page.
