# Recovery 12 — SPA own-identity claim

## Status

Implemented.

## Depends on

[10](10_spa_unified_restore.md), [11](11_spa_recovery_progress.md)
(backup restored and progress table initialized).

## Context

The owner cannot use signature-auth until their key is on record. They claim via
challenge-response and a nested key chain. See [04](04_own_identity_claim.md)
and [README](README.md) *Phase 1a*. Runs only after unified restore chose the
recovery path ([10](10_spa_unified_restore.md)).

## Scope

- Execute the **own identity** progress item:
  1. `GET /api/recovery/identity/claim` → challenge.
  2. Sign challenge with active private key.
  3. Build nested key chain from IndexedDB (`publicKeys`, `revocations`,
     predecessor links) for the claimant only.
  4. `POST /api/recovery/identity/claim` with profile + nest.
- On success: initialize `requestSigner` with the restored session (same as
  post-signup). Record `startTime` / `endTime` on the `own_identity` progress
  entry ([11](11_spa_recovery_progress.md)).
- Surface errors; allow retry without clearing unrelated progress.

## Non-goals

- Peer identity report-back (13).
- Reeds, follows, `complete` (14).
- Import-gate UI mirror ([15](15_spa_import_gate_mirror.md)) — server enforces
  gate after claim; client mirror lands in 15.
- Re-submitting pending revocations or other pending stores during claim.
- Device binding ([16](16_device_binding.md)).
- Playwright coverage (deferred).

## Design

Claim endpoints are already on the API unauthenticated allowlist
(`api.ts`). Key-chain builder may live alongside recovery progress helpers or a
dedicated module shared with 13.

## Test plan

- [ ] Successful claim against recovery-mode server; request signer works
- [ ] Stale challenge rejected; retry fetches fresh challenge
- [ ] Incomplete own nest → clear error before POST
