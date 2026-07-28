# Client-generated default avatars

The SPA displays a **deterministic image derived from the user ID**
(hash → unique visual) for every avatar surface. Custom `avatarURL` values
remain in the schema and identity signatures for a later enablement, but
are **disabled in product**: not shown in the UI and not used for display.

The profile-update handler also stops **outbound** validation fetches
(`HEAD` to the avatar URL).

| # | Title | Depends on |
|---|-------|------------|
| [00](00_design.md) | Design + algorithm + UX | — |
| [01](01_server.md) | Drop server avatar `HEAD` | 00 |
| [02](02_spa.md) | Shared avatar component + call sites | 00 |

---

## Status

**Implemented** (00–02).

## Locked decisions

| Topic | Decision |
|-------|----------|
| Display | Always hash-based identicon from **user ID** |
| Custom `avatarURL` | **Disabled** for now: keep DB column, wire/JSON field, and signed identity bytes; do not show an editor or use the URL in UI |
| Hash | SHA-256 over the UTF-8 user ID string |
| Visual | Motif + root/scale palette (structure and colors both from hash) |
| Render | Inline SVG built in the client |
| Server | No `http.Head` (or other fetch) of avatar URLs; local scheme checks may remain for when custom URLs are re-enabled |
| Spec home | This directory |

## Motivation

Avatars should be unique per account and work offline without depending on
remote image hosts. Keeping `avatarURL` in storage and signatures preserves
the path to turn custom URLs back on without re-signing everyone’s identity.
