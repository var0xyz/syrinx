# Avatars 02 — Shared SPA component + call sites

## Status

Implemented.

## Depends on

[00](00_design.md)

## Context

Avatar markup is duplicated across reed detail, lists, feeds, and
[`UserProfileCard.svelte`](../../spa/src/lib/components/UserProfileCard.svelte).

## Scope

- Add `identicon` helper + `Avatar.svelte`.
- Replace emoji / `<img src={avatarURL}>` at all user-facing avatar sites
  with the generated avatar.
- Hide the profile “Avatar URL” editor; on save, pass through existing
  `user.avatarURL` into the identity update payload.

## Non-goals

- Deleting `avatarURL` from types or verifiers (still part of the signed
  profile).
- Using `avatarURL` for display while disabled.

## Design

### Component API

```svelte
<Avatar userID={id} username={name} size="2rem" />
```

- `userID` required.
- `username` optional (alt text).
- `size` optional CSS length.
- Do not accept `avatarURL` for display while custom URLs are disabled
  (avoids accidental use). When re-enabled, add an optional prop.

### Call sites

- Reed detail, `ReedsList`, feeds, `UserProfileCard`, profile edit header
  if any.
- Grep for `avatarURL` in templates and emoji avatar fallbacks.

### Profile save

- Remove Avatar URL input from the edit form.
- `saveProfile` includes `avatarURL: user.avatarURL ?? ''` (unchanged) in
  the signed tuple and API form body.

### Tests

- Unit: fixed `userID` → stable SVG serialization.
- Different userIDs → different patterns (spot-check).

### Checklist

- [x] `identicon` helper + test
- [x] `Avatar.svelte`
- [x] All display call sites
- [x] Avatar URL editor hidden; pass-through on save
- [x] Server HEAD removed ([01](01_server.md))
