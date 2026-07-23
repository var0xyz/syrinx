# Deletion 05 — SPA author queue (reed removal)

## Status

Implemented (SPA `pendingRemoval` / `removedReeds`, author DELETE queue).

## Depends on

[03](03_reed_api.md)

## Context

Authors remove reeds offline-first: durable pending → server → local cert
store → drop reed → clear pending. See [README](README.md).

## Scope

- IndexedDB (or existing DB service) stores:
  - `pendingRemoval` — keyed by reed id; fields: serverID, userID, reedID,
    user signature (and any local bookkeeping).
  - `removedReeds` — full countersigned certs (`type: "reed"`).
- Author UX/API: create pending, sign, DELETE, on success:
  1. `put` `removedReeds`
  2. delete from `reeds` (and related local indexes)
  3. delete `pendingRemoval`
- On app start / online: flush `pendingRemoval` (idempotent DELETEs).
- Verify server block before step 1–3 commit locally.

## Non-goals

- Holder path (06).
- Account queues (09).

## Design

Never clear `pendingRemoval` before the reed is gone locally and the cert
is stored — otherwise a crash loses the attestation or leaves a zombie reed.

On `reed_fork` / tip interactions: removing the tip updates local tip to the
previous non-removed reed (coordinate with tip-check client work).

## Test plan

- [ ] Offline: pending survives reload; flush when online
- [ ] Success path order: removedReeds → reeds delete → pending clear
- [ ] Retry returns same cert; local state converges
- [ ] Reject/abort if server countersig fails verification
