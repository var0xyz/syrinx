# Recovery 16 — Device binding (single active device)

## Status

Proposed.

## Depends on

[09](09_user_status.md)–[15](15_spa_import_gate_mirror.md)
(land device binding **after** those SPA changes).

## Context

Backups move identity material between browsers manually. Until device binding
exists, nothing stops two browsers from holding the same keys and acting as the
same user. This proposal introduces a **server-stored device id** so only one
active client is accepted at a time. It is **not** part of the first recovery
SPA cut — spec now, implement after 09–15.

## Scope

### Client

- On first launch of an empty origin, generate a random **device id** (UUID) and
  store it only in that origin’s local storage.
- **Device id is never included in backups.** Restoring a backup onto a new
  browser leaves that browser with its own device id (or generates one if
  missing).
- Send the device id on **signup** (first bind) and on **every authenticated
  request** thereafter (dedicated header, e.g. `X-Syrinx-Device-Id`).
- If the server rejects a request because of a **device mismatch**, the client
  **wipes all local Syrinx data** on that device (localStorage + IndexedDB) for
  security.

### Server

- Persist the user’s current `device_id` (nullable until first bind).
- **Signup:** accept and store the presented device id as the bound device.
- **Authenticated requests:** if the user has a bound device id and the request
  header does not match → reject (e.g. **403** with a distinct error the client
  can detect).
- **`POST /api/users/device`:** authenticated. Body carries the new device id.
  **Overwrites** the previous binding (intentional device move). After success,
  only the new device id is valid.
- **Recovery claim:** on successful own-identity claim, bind the requesting
  device id (same as first bind) so the recovering browser becomes the active
  device. Detail the header/body field in implementation; claim is otherwise
  unchanged ([04](04_own_identity_claim.md)).

### Intentional new device

1. New browser restores backup via [10](10_spa_unified_restore.md) (or already
   holds keys).
2. If status is `complete` but requests fail device mismatch → wipe would be
   wrong for the *new* device mid-migration; the new device must call
   `POST /api/users/device` **before** relying on normal routes (or the restore
   success path does this once). Spec the exact UX in implementation notes:
   after import of a claimed account, register this device via
   `POST /api/users/device`, overwriting the old binding.
3. Old device’s next authenticated call fails mismatch → old device wipes
   itself.

## Non-goals

- Multi-device concurrent sessions.
- Putting device id inside encrypted backups.
- Shipping this before recovery SPA slices 09–15.

## Design

Device id is a server-side lock on “which browser may speak as this user,” not a
cryptographic identity. Private keys remain the auth root; device id is an
extra gate after signature-auth succeeds (or part of the same middleware once
the user is known).

Recovery and import remain backup-based; device binding only constrains which
restored client remains active.

## Test plan

- [ ] Signup stores device id; matching header succeeds
- [ ] Mismatched header → reject; client wipe hook invoked
- [ ] `POST /api/users/device` overwrites; old device then rejected
- [ ] Backup file does not contain device id; restore keeps target device’s id
- [ ] Claim binds device id for the recovering browser
