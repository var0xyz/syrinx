# Invites 04 — SPA signup gating (home CTA + invite link)

## Status

Implemented (home Sign Up gated on `signupMode === open`; invite links
via `/signup?iid=&uid=`; closed/preamble gates; invite form field + check;
friendly errors and failed-signup cleanup).

Invite links target `/signup?iid=&uid=` directly (preamble remains for
the open-mode home CTA only).

## Depends on

[00](00_signup_mode.md) (for `signupMode` on `/api/server/info`),
[03](03_signup_consume.md) (for real invite consume + error strings)

## Context

The landing page always shows “Sign Up” today
([`spa/src/routes/+page.svelte`](../../../spa/src/routes/+page.svelte)).
Under `invite` and `closed`, that CTA must disappear. Invitees arrive via
`/signup?iid=TOKEN&uid=CREATOR` (and optionally through `/preamble` with the query
preserved).

## Scope

- Fetch `signupMode` from `/api/server/info` on the landing page (and any
  gate that currently assumes signup is always available).
- Show home “Sign Up” **only** when `signupMode === "open"`.
- Keep “Import backup” visible in all modes.
- Signup page: read `invite` from the URL query; pass it through
  `authService.signup` / `apiService.signup` as form field `invite`.
- If mode is `invite` and there is no valid invite link (`?invite=` with
  fragment secret that passes `/api/invites/check`): show “You need a valid
  invite link to join this server.” and do not offer the form. First account:
  operators deploy with `SIGNUP_MODE=open`, then switch modes.
- If mode is `closed`: `/signup` and `/preamble` show “Signups are closed”
  and do not offer the form (deep links fail closed).
- If mode is `open`: form works with or without invite query params; when present,
  send `inviteID`, `inviteCreatorID`, and `inviteSecret`.
- Preserve invite query across `/preamble` → `/signup` when the user still
  uses the preamble flow from an invite link (e.g. link to
  `/preamble?iid=TOKEN&uid=CREATOR` or `/signup?iid=TOKEN&uid=CREATOR`; if marketing entry is
  only `/signup?iid=`, preamble can be skipped or linked with the param —
  **prefer**: invite links point at `/signup?iid=&uid=` and skip
  preamble, OR preamble reads and forwards the param. Pick one in
  implementation and document in the PR; recommendation: **invite links →
  `/signup?iid=&uid=` directly**; preamble remains for the open home CTA only.
- Optional: call `GET /api/invites/check` on load when a token is present to
  fail fast with “This invite is not valid” before keygen.
- Surface server errors (`Invite required`, `Invalid or used invite`,
  closed) in the signup UI.
- Extend Playwright coverage in
  [`spa/tests/signup-and-publish.spec.ts`](../../../spa/tests/signup-and-publish.spec.ts)
  or a sibling spec for mode matrix smoke (may require test server env).

## Non-goals

- Invites management UI / toolbar (05).
- Showing `invitedBy` on profiles (can land with 05 or a small follow-up in
  the same PR as 03’s wire field — **include a minimal profile display in
  this step or 05**; recommendation: show on profile in **05** so 04 stays
  focused on entry gating).
- Changing import/backup.

## Design

### Landing

```svelte
{#if signupMode === 'open'}
  <a href="/preamble" class="btn btn-primary">Sign Up</a>
{/if}
<a href="/import" class="btn btn-secondary">Import backup</a>
```

While `server/info` is loading, do not flash the Sign Up button (avoid
layout flicker into `invite`/`closed` servers). Hide until mode is known,
or default-hide until loaded.

### `apiService.signup`

Extend `SignupInput` / username check with optional `inviteID`,
`inviteCreatorID`, and `inviteSecret`. When mode is `invite`, both signup and
`POST /check-username` require the same valid pending invite.

Unauth allowlist already includes signup; add `/invites/check` if the fail-
fast check is implemented here (middleware allowlist is server-side in 02).

### Closed / invite empty states

Copy suggestions (not brand-critical):

- Closed: “This server is not accepting new signups.”
- Invite, after server `Invite required`: “You need a valid invite link to
  join this server.”
- Invalid token: “This invite is invalid or has already been used.”

### Bootstrap UX note

First user on an `invite` server: deploy with `SIGNUP_MODE=open`, complete
bootstrap signup, then switch to `invite`. `/signup` without a valid invite
link shows the invite-required gate; the server still allows the zero-user
bootstrap on POST if configured, but the SPA does not expose the form.

## Test plan

- [x] Mock/info `open` → Sign Up visible; signup without invite works
- [x] Mock/info `invite` → Sign Up hidden; `/signup` without invite blocked;
  `/signup?iid=valid&uid=valid` works
- [x] Mock/info `closed` → Sign Up hidden; `/signup` shows closed, no form
- [x] Invite query forwarded into API form body
- [x] Invalid invite shows error; no partial local session left behind
      (if signup fails mid-flow, clean up any keys written before the POST —
      match existing failure handling on signup page)
