# Avatars 01 — Schema + identity `avatarHash`

## Status

Proposed.

## Depends on

[00](00_design.md)

## Context

`users.avatar_url` and identity header `avatarURL` do not match the hash +
blob model. Blank-slate schema and signing helpers.

## Scope

- Drop `avatar_url` from `users`; store `avatar_hash` on the user row (or
  equivalent denormalized field loaded with the identity — lock one column
  name in implementation, wire JSON/`avatarHash`).
- Add `avatars` table: `user_id` PK → `users(id)`, `hash` indexed, `png`
  `BYTEA NOT NULL`.
- Switch `identity.BuildUserIdentityPayload` /
  `BuildProfilePayload` (and SPA `buildUserIdentityPayload` /
  `buildProfilePayload`) from `avatarURL` to `avatarHash`.
- Update verifiers, recovery upsert, handlers, and tests that touch the
  old field.

## Non-goals

- Process / PUT / GET handlers (02–04).
- SPA crop UI (05).

## Work

1. Rewrite `InitDB` `users` + create `avatars` (blank slate; recreate DB).
2. Thread `AvatarHash` through Go/TS user types and DB load/save.
3. Rename identity header key to `avatarHash`; empty omits.
4. Fix signup (empty hash), profile update plumbing stubs until 03.

## Acceptance

- No `avatar_url` / `avatarURL` in schema or signed identity headers.
- `avatars` exists with PK `user_id` and index on `hash`.
- Identity goldens use `avatarHash` when non-empty.
