# Recovery 17 — Device binding (single active device)

## Status

Implemented.

## Depends on

[09](09_user_status.md)–[15](15_spa_import_gate_mirror.md)
(land device binding **after** those SPA changes).

## Context

Backups move identity material between browsers manually. Until device binding
exists, nothing stops two browsers from holding the same keys and acting as the
same user. This proposal introduces a **server-side device binding** so only one
active client is accepted at a time. It is **not** part of the first recovery
SPA cut — spec now, implement after 09–15.

History linearity under concurrent publish is a separate concern: see
[16](16_reed_tip_check.md) (tip check on create). Device binding is the
session/UX gate; it does not by itself make forked reed chains impossible.

**Clean slate:** no migration for pre-existing accounts without a device row.
Every live account gets a binding at signup or claim.

Device id is an origin-local UUID. It is **never** part of the signed user
profile / countersigned identity record, and it is **never** included in user
backups. Private keys remain the authentication root; device binding is an
extra gate after signature-auth (or inside the same middleware once the user
is known).

## Scope

### Schema — `user_devices`

Do **not** store a single `users.device_id` column. Persist history in a
separate table:

```text
user_devices (
  user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  device_id  TEXT NOT NULL,          -- client UUID
  linked_at  TIMESTAMPTZ NOT NULL,  -- when this row became active
  revoked_at TIMESTAMPTZ NULL,      -- NULL ⇒ currently active
  PRIMARY KEY (user_id, device_id, linked_at)
)
```

- **Active device** for a user = the row with `revoked_at IS NULL`.
- Enforce **at most one active device per user** with a partial unique index
  on `user_id WHERE revoked_at IS NULL` (or an equivalent transactional
  invariant). Application code must still revoke-then-insert in one
  transaction.
- The same `device_id` may appear again later as a **new row** after a prior
  revoke (re-link ≠ revive the old row).
- `device_id` uniqueness is **per user**, not global: two accounts on one
  browser may share the same origin UUID.
- Table is **server policy state**, not reconstructed from user backups or the
  operator identity bundle (same class as `online_users` /
  `ongoing_recoveries`). After `ops import-identity` / DB wipe it is empty
  until signup, claim, or rebind.
- **Keep revoked rows** (no prune). History is the foundation for a later
  client-visible device list (and eventual multi-device support); that UX is
  **deferred** — see Non-goals / Future.

### Client

- On first launch of an empty origin, generate a random **device id** (UUID)
  and store it only in that origin’s `localStorage` (key e.g. `deviceId`).
- **Never export device id in backups.** On `writeBackup` / restore, **preserve**
  the target origin’s existing device id (generate if missing). Even if an old
  backup file somehow contained a leaked id, restore must not adopt it.
- Send the device id in the **`X-Syrinx-Device-Id` header only** on signup,
  recovery claim, and every authenticated HTTP request. WebSocket connect must
  carry the same value as a query param (WS auth is query-string today).
  Handlers read the header; no device id in JSON bodies.
- **Before starting an import** (`/import`, before backup decrypt / status
  probe): show a confirmation that continuing will **log the user out of any
  other devices**. Cancel leaves the other devices alone. Applies whether the
  eventual path is normal import (`complete`) or recovery (claim will bind).
- **Device mismatch** (distinct server error — see *Resolved*) → client
  **clears `userId` from localStorage** (and disconnects WS / clears the
  request-signer session) so the browser is logged out. **Do not** wipe
  IndexedDB or other local data in this cut. Coordinate logout across tabs if
  needed (`storage` event).
- **Import-gate 403** (“finish recovery”) must **not** clear `userId` — route
  to `/recovery` per [15](15_spa_import_gate_mirror.md).

### Server — bind helper

One transactional helper used by signup, claim, and rebind:

1. `UPDATE user_devices SET revoked_at = now() WHERE user_id = ? AND revoked_at IS NULL`
2. `INSERT` new row `(user_id, device_id, linked_at = now(), revoked_at = NULL)`

Idempotent case: if the sole active row already has this `device_id`, return
success without churn (no revoke/reinsert required).

### Server — entry points

| Entry | When | Device source | Bind semantics |
|-------|------|---------------|----------------|
| **Signup** | First account | `X-Syrinx-Device-Id` header | Bind (no prior actives on clean slate) |
| **Own-identity claim** ([04](04_own_identity_claim.md)) | Successful claim | Same header | **Revoke-all + bind** in the **same transaction** as identity upsert / `ongoing_recoveries` insert |
| **`POST /api/users/device`** | Intentional device move / post-import of a **claimed** account | Header (body optional echo; header is authoritative) | **Revoke-all + bind**. Endpoint is **exempt from the device-match check** (chicken-egg: new device is not yet active) |

### Server — authenticated requests

After signature-auth identifies the user:

1. **Import gate** ([03](03_bookkeeping_and_gates.md)) — only when
   `RECOVERY_MODE` is on **and** the user is in `ongoing_recoveries`. This is
   unchanged by device binding: the env flag installs the middleware; the row
   decides who is gated.
2. **Device check** — if the user has an active device and the presented
   header does not match → reject with a **distinct** error (see *Resolved*).
   Runs whenever the user is authenticated, **independent of** `RECOVERY_MODE`.
3. Missing / malformed device header on a route that requires binding → same
   class as mismatch (reject; do not silently pass).

No change to import-gate allowlists or `ongoing_recoveries` semantics. Device
binding is an additive check. Middleware order
(signature-auth → import gate → device) only matters when both could fire
(e.g. mid-recovery on a revoked device): distinct error codes keep the client
from logging the user out when they should resume `/recovery`.

**Exempt from device match** (still may require signature-auth where
applicable): `POST /api/users/device`, unauthenticated allowlist routes
(`/server/info`, `/server/keys/`, `/users/status`, claim challenge GET/POST,
signup), and any other path already outside signature-auth.

After claim, authenticated `/api/recovery/*` **does** require the active device
to match (the reclaiming browser just bound itself).

### Flows

#### A — Signup

Client generates device id → signup with header → server binds → normal
session.

#### B — Intentional new device (account already `complete`)

1. User confirms they understand other devices will be logged out (see Client).
2. New browser restores backup via [10](10_spa_unified_restore.md) (status
   **200 `complete`**).
3. After storage write, **before** any other authenticated call or WebSocket
   connect: `POST /api/users/device` with this origin’s device id → revoke
   previous actives + bind.
4. Old device’s next HTTP/WS attempt → mismatch → clear `userId` (logged out).

Do **not** log out the *new* device on mismatch mid-migration: ordering
(rebind first) plus the rebind exemption prevent that.

#### C — Recovery (unknown / ongoing)

1. User confirms they understand other devices will be logged out (same prompt
   as B — import UX is shared).
2. Backup write + recovery handoff ([10](10_spa_unified_restore.md)) — **no**
   `POST /users/device` yet (user may not be claimable / signature-auth may
   not apply).
3. On **successful own-identity claim**: revoke-all + bind the claiming
   device (same helper).
4. Remainder of recovery ([12](12_spa_own_identity_claim.md)–[14](14_spa_reeds_follows_complete.md))
   runs under import gate + matching device.
5. `POST /complete` clears import gate only; device binding stays.

“Successful import” in the sense of “this browser becomes the sole active
device” means: **status `complete` restore → `POST /users/device`**, or
**successful claim** on the recovery path. It does **not** mean “any backup
write.”

### WebSocket / live connections

- Pass device id on WS handshake; reject mismatch at connect (in addition to
  the existing ongoing-recovery check).
- On successful revoke-all + bind, **disconnect existing WS clients** for that
  `user_id` (kick everyone; the new device reconnects). Leaving the old socket
  up after rebind would keep a revoked device live.

### Logout, delete, mismatch

- **Logout** does **not** revoke the server binding; the same browser remains
  the active device.
- **Account delete** cascades `user_devices` via `ON DELETE CASCADE`.
- **Mismatch** → clear local `userId` (+ WS/signer) only — same end-state as
  “logged out,” not a full local data wipe.

## Non-goals

- Multi-device concurrent sessions (history may retain many revoked rows; only
  one active).
- Putting device id inside encrypted user backups or the operator identity
  bundle.
- Signing device id into the user profile / countersigned identity payload.
- Shipping this before recovery SPA slices 09–15.
- Client-visible device list / management UI (deferred — see Future).
- Returning binding state from `POST /users/status`.
- Full local wipe (IndexedDB + all `localStorage`) on mismatch — deferred;
  this cut only clears `userId`.
- Migrating accounts that predate `user_devices` (clean slate).

## Future (deferred)

- **Device list on the client:** surface linked / revoked devices (from
  `user_devices` or a thin API) so the user can see where they have been
  signed in. Useful groundwork for multi-device support later. Not in this
  proposal’s scope.
- **Multi-device concurrent sessions** and stronger mismatch handling (e.g.
  full local wipe) if product needs them.

## Design

Device id is a server-side lock on “which browser may speak as this user,” not
a cryptographic identity. Revoke-then-bind (append-only history with
`revoked_at`) replaces in-place overwrite of a single column so we keep history
for a future device list, while the runtime rule stays “exactly one active.”

`RECOVERY_MODE` still only controls recovery endpoints + whether the import-gate
middleware is installed. Device binding is orthogonal and always on once
shipped.

Recovery and import remain backup-based; device binding only constrains which
restored client remains active after claim or after a `complete`-account
import rebind.

## Resolved

1. **Storage** — `user_devices` with `linked_at` / `revoked_at`; active =
   `revoked_at IS NULL`; at most one active per user.
2. **Not in profile / backup** — device id is never countersigned and never
   exported.
3. **Revoke-then-bind** — shared helper for signup, claim, and
   `POST /users/device`.
4. **Claim revokes prior actives** — same helper as intentional move.
5. **Complete-account import** binds via `POST /users/device` after backup
   write, before normal API/WS use.
6. **Recovery backup write** does not bind; **claim** does.
7. **Rebind endpoint exempt** from device-match middleware (chicken-egg).
8. **Distinct client-visible error** for device mismatch vs import-gate
   “finish recovery” (structured body preferred, e.g.
   `{ "error": "Device mismatch: this session is not bound to the active device." }` vs `{ "error": "Finish recovery import first." }`).
   Client: mismatch → clear `userId`; finish-recovery → `/recovery` without
   clearing session identity mid-recovery.
9. **WS** carries device id and is kicked on rebind.
10. **Logout** does not revoke; **delete user** cascades devices.
11. **Last rebind wins** if two new devices race; the loser is logged out on
    its next authenticated call.
12. **Import confirmation** — starting `/import` requires an explicit confirm
    that other devices will be logged out; cancel aborts before decrypt/status.
13. **Clean slate** — no lazy auto-bind / migration path for pre-feature
    accounts.
14. **Wire format** — `X-Syrinx-Device-Id` header only.
15. **Revoked row retention** — keep history (supports a future device list).
16. **Middleware** — signature-auth → import gate (if `RECOVERY_MODE`) →
    device check. Import-gate logic unchanged; device check is additive.
17. **Mismatch reaction** — clear `userId` (+ WS/signer), not full wipe.

## Open questions

None that block the design. Implementation may still tune confirm-dialog copy.

## Test plan

- [ ] Import UI requires confirm about logging out other devices before restore
- [ ] Signup inserts active `user_devices` row; matching header succeeds
- [ ] Second device without rebind → mismatch error; client clears `userId`;
      import-gate 403 does **not** clear `userId`
- [ ] `POST /api/users/device` revokes previous active, inserts new; old device
      then rejected; WS of old device disconnected
- [ ] Idempotent rebind of already-active device → 200, single active row
- [ ] Backup export/import never contains / overwrites origin `deviceId`
- [ ] Device id absent from profile countersign payload
- [ ] Claim transaction: identity + ongoing + revoke-all + bind
- [ ] Status `complete` import path calls rebind before other authenticated use
- [ ] Recovery handoff backup write does **not** call rebind; claim does
- [ ] Account delete removes `user_devices` rows
- [ ] At most one `revoked_at IS NULL` row per user under concurrent rebind
