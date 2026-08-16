# Federation (explicit peering)

Cross-instance trust via an **encrypted invitation handshake** between admins,
server-to-server **`/api/federation/connect/{inviteId}`** callback, and a
**second-admin approval** step before peers land in **`federation_established`**.

Each instance remains **IdP for its users** and **federation client** for
established peers. `serverID` in signed envelopes prepares for foreign
`(serverID, userID)` refs.

**Blank slate — no migration, no backwards compatibility.**

| # | Title | Depends on |
|---|-------|------------|
| [00](00_design.md) | Design + handshake + locked model | [roles 00](../roles/00_design.md) |
| [01](01_invitation_create.md) | Encrypted invite + `federation_invitation` + admin UI (create) | 00; [roles 01](../roles/01_role_store.md) |
| [02](02_connect_handshake.md) | Decrypt, verify, `POST …/connect/{inviteId}`, peer bookkeeping on `servers` | 01 |
| [03](03_approval_established.md) | Second-admin accept → `federation_established` | 02 |
| [04](04_runtime_verify_display.md) | Remote identity verify + foreign ref display | 03 |
| [05](05_revoke_established.md) | Revoke peering + 401 on incoming peer traffic | 03, 04 |

Related: [`roles/`](../roles/README.md) (admin UI + admin invites);
conversations foreign refs [`conversations/01`](../conversations/01_publish_and_refs.md).

---

## Status

**In progress.** Steps **01–02** implemented; 00, 03–05 remain Proposed.

| Step  | Status          |
|-------|-----------------|
| 00    | Proposed        |
| 01    | **Implemented** |
| 02    | **Implemented** |
| 03–05 | Proposed        |

## Locked decisions

| Topic | Decision |
|-------|----------|
| Discovery | **No** global directory — admins exchange **server public keys** OOB, then an **encrypted connection string** |
| Initiation | Admin **create invite** on initiator; payload encrypted to **remote server public key** |
| Handshake secret | Random **`secret`** in invite; initiator stores **hash only**; responder proves possession on `connect` |
| Callback | Responder POSTs to **`https://{initiator-base-url}/api/federation/connect/{inviteId}`** |
| Approval | After handshake, a **different local admin** (≠ invite creator / ≠ paste operator) must **accept** |
| Durable peers | **`federation_established`** links to **`servers`**; the handshake itself lives entirely on **`federation_invitation`** (no separate attempt table); rows retained for audit |
| Invite lifecycle | Status **`new`** → **`accepted`** → **`approved`** \| **`rejected`**; **`canceled`** (revoked while still `new`) and **`revoked`** (an `approved` connection later torn down) are distinct terminal states — never deleted |
| Visibility | **All admins** see **all** invites on the instance (not creator-only) |
| Admin UI | **Admin → Mesh** (create invite, paste connection string, approve pending) |
| Revoke peering | **`federation_established.revoked`**; revoker returns **401** to incoming peer traffic; **no auto-action on remote** |
| Non-goals (v1) | Cross-instance reed relay, federated follow, open federation, automated reciprocal revoke |

## Motivation

Operators need a concrete, auditable ceremony — not manual DB pins — that binds
two instances cryptographically and requires **two admins on each side** where
possible before the link goes live.
