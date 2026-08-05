# Account recovery 06 — Device takeover on bootstrap

## Status

Proposed.

## Depends on

[02](02_challenge_bootstrap.md),
[recovery 17](../recovery/17_device_binding.md)

## Context

Account recovery is an intentional move to a new browser. The same posture
as backup import: **this device supersedes older ones**. Device binding
(17) is the mechanism; this step wires it into bootstrap.

## Scope

- On successful `POST /api/account-recovery/bootstrap`, in the **same
  transaction** as the rehydration upsert: revoke-all active devices for
  the user and bind `X-Syrinx-Device-Id` (17 helper).
- Client sends device id header on challenge is unnecessary; required on
  bootstrap (and all later authenticated calls per 17).
- Confirm copy already shown in [04](04_spa_keys_only_restore.md) remains
  accurate once bind is live.
- Idempotent bootstrap: same device id already active → no churn.

## Non-goals

- Implementing `user_devices` itself (17).
- Multi-device concurrent use.
- Changing server-recovery claim bind rules (claim already revoke+bind
  in 17).

## Design

Reuse 17’s bind helper:

1. `UPDATE user_devices SET revoked_at = now() WHERE user_id = ? AND revoked_at IS NULL`
2. `INSERT` active row for presented device id

Call from bootstrap after key verification. Missing / malformed device header → reject
bootstrap once 17 is enforced on authenticated routes (bootstrap is the
bind entry — treat like `POST /users/device`: header required, exempt from
“must already match” check).

Until 17 lands, 02’s bootstrap ships without bind; 04’s warning copy may
say takeover will apply when the server supports it, or omit until this
step.

## Test plan

- [ ] Bootstrap with device A then B → only B active
- [ ] Repeat bootstrap with same device → single active row
- [ ] Old device authenticated request → mismatch / logout per 17
- [ ] Bootstrap without device header → reject (once 17 enforced)
