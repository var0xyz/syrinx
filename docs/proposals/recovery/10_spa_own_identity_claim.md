# Recovery 10 — SPA own-identity claim

## Status

Proposed.

## Depends on

[09](09_spa_recovery_ledger_ui.md)

## Context

The owner cannot use signature-auth until their key is on record. They claim via
challenge-response and a nested key chain. See [04](04_own_identity_claim.md)
and [README](README.md) *Phase 1a*.

## Scope

- Execute the **own identity** ledger step:
  1. `GET /api/recovery/identity/claim` → challenge.
  2. Sign challenge with active private key.
  3. Build nested key chain from IndexedDB (`publicKeys`, `revocations`,
     predecessor links) for the claimant only.
  4. `POST /api/recovery/identity/claim` with profile + nest.
- On success: initialize `requestSigner` with the restored session (same as
  post-signup). Mark claim step done in persisted ledger.
- Surface errors; allow retry without clearing unrelated ledger progress.

## Non-goals

- Peer identity report-back (11).
- Reeds, follows, `complete` (12).
- Import-gate UI mirror (13) — server enforces gate after claim; client mirror
  lands in 13.
- Re-submitting pending revocations or other pending stores during claim.
- Playwright coverage (deferred).

## Design

Claim endpoints are already on the API unauthenticated allowlist
(`api.ts`). Key-chain builder may live in `recoveryLedger` or a dedicated
helper shared with 11.

## Test plan

- [ ] Successful claim against recovery-mode server; request signer works
- [ ] Stale challenge rejected; retry fetches fresh challenge
- [ ] Incomplete own nest → clear error before POST
