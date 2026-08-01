# Protobuf 04 — HTTP endpoints

## Status

Proposed.

## Depends on

[03](03_http_codec.md)

## Context

All `/api/` handlers and `spa/src/lib/services/api.ts` (plus auth/signup
callers that hit HTTP) must speak protobuf in one cutover.

## Scope

- Define `*Request` / response messages for every body-carrying route
  (signup, profile update, SignReed, keys, revocations, invites,
  deletions, recovery, stats, probes, …).
- Replace form-urlencoded and multipart field scraping with protobuf
  request messages (SignReed content is a field on the request, not a
  multipart part).
- Switch handlers and SPA clients together.
- Keep idempotency and status-code behavior
  ([AGENTS](../../AGENTS.md) SignReed / removal / invite rules).

## Non-goals

- WS (05).
- Changing canonical signing payloads.

## Work

1. Extend protos with per-route request messages where the resource alone
   is not enough (e.g. SignReed: reed fields + user signature).
2. Migrate handlers in coherent groups (identity/keys, reeds, invites,
   deletion, recovery) but land as **one** merge that flips the SPA
   client with the server.
3. Update Go and Playwright tests to send/receive protobuf.
4. Remove form-parsing paths for migrated routes.

## Acceptance

- No `/api/` success or error body uses JSON or form encoding.
- SPA `apiService` methods use the codec from 03.
- Existing behavioral tests (idempotent SignReed, invite consume, …)
  still pass under protobuf.
