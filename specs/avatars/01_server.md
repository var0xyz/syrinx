# Avatars 01 — Drop server avatar `HEAD`

## Status

Implemented.

## Depends on

[00](00_design.md)

## Context

`UpdateMe` validates `avatarURL` then calls **`http.Head(avatarURL)`** before
countersigning. That makes the API an outbound client of user-supplied hosts.

## Scope

- Remove the `http.Head` (and any equivalent GET) of the avatar URL.
- Keep local URL validation (`ParseRequestURI` + `http`/`https`) when a
  non-empty `avatarURL` is submitted, so re-enabling custom URLs later does
  not require reinventing basic checks.
- While custom URLs are disabled in the SPA, updates should typically send
  the existing stored value (often empty) unchanged.

## Non-goals

- Rejecting all non-empty `avatarURL` at the API (storage stays writable for
  compatibility with signed records that already carry a URL).
- SSRF allowlists.

## Design

### Handler change

In profile update, after optional local scheme checks, proceed to signature
verify / countersign without contacting `avatarURL`.

### Tests

- Update with `https://example.com/a.png` does not require a reachable host.
- Invalid URI / `ftp://` still 400 when non-empty.
- Empty `avatarURL` accepted.
