# Avatars 00 — Design + algorithm + UX

## Status

Implemented.

## Depends on

—

## Context

`users.avatar_url` is optional and signed into the identity payload as
`avatarURL` (omitted from signed bytes when empty). Call sites that show a
face today use either that URL or a generic emoji.

Profile update validates a non-empty URL then **`http.Head`**s it before
accepting the change.

## Scope

- Client-side avatar generation from **user ID** for all display surfaces.
- Keep `avatar_url` / `avatarURL` in DB, API JSON, and identity signatures.
- **Disable** custom avatar URLs in the product: no editor, no `<img src={avatarURL}>`.
- Drop server-side remote fetch of avatar URLs on update.
- One shared SPA component for all avatar surfaces.

## Non-goals

- Hosting or proxying avatar bytes on the Syrinx server.
- Uploading image files to the instance.
- Removing `avatarURL` from the signed identity shape.
- Gravatar / DiceBear / other third-party avatar HTTP APIs.
- Re-enabling custom URLs in this step (leave a clear “disabled” note for a
  future step).

## Design

### Display rule (current)

```
always show GeneratedAvatar(userID)
```

Do not read `avatarURL` for rendering. Profile edit must not expose an
avatar URL field. When saving profile (username / bio), **pass through** the
existing stored `avatarURL` into the signed update payload so the field is
unchanged and signatures stay valid.

### When custom URLs return

A future step may flip display to:

```
if avatarURL non-empty → <img src={avatarURL}>
else → GeneratedAvatar(userID)
```

and re-show the editor. Storage and signing already support that.

### Algorithm (identicon)

Input: `userID` string (as stored).

1. `digest = SHA-256(UTF-8 bytes of userID)`.
2. From the digest pick a **root** HSL (tonic) and which **color scale** to
   use. Scales are fixed tables of relative intervals
   `{ Δhue, Δsat, Δlight }` — like scale degrees relative to a root note,
   not absolute colors. Modes include analogous, complementary/split,
   triadic, and warmer/cooler step sets.
3. Take a **subset** of the scale (2–5 active colors) and pick a **motif**
   (speckle, blocks, H/V stripes, diamond bands, quarters, orbit rings)
   plus a density. Structure varies per user — not only a recolored noise field.
4. **Grid**: 11×11 cells, horizontally mirrored.
5. Output **inline SVG** (square viewBox), sized by CSS like current circles.

Same `userID` → same SVG everywhere. Username changes do not change it.

### UX

- Circular crop / `border-radius: 50%` consistent with current avatar CSS.
- `alt` / accessible name: username when known.
- Render generated avatar on first paint (no emoji flash).

### Operator note

Custom URL enablement later still means the browser loads user-chosen hosts;
the instance does not verify reachability.
