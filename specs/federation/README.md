# Federation (explicit peering)

Cross-instance trust via an **encrypted invitation handshake** between
admins, a server-to-server **`/api/federation/connect/{inviteId}`**
callback, then a manual admin **approval** step before the peer is
actually trusted. (A second-*admin* rule — approver must differ from
whoever set up the handshake — and a separate `federation_established`
table were originally designed but never shipped; approval is real, just
single-admin, and lives on `federation_attempt`/`servers` instead. See
the Status section below.)

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
| [06](06_content_relay.md) | Cross-instance reed relay — extends the same-server relay machinery | 04, 05 |
| [07](07_presence_delivery.md) | Server presence (`online`/`offline`/`ping`) + durable event delivery (mentions, deletions) | 06 |

Related: [`roles/`](../roles/README.md) (admin UI + admin invites);
conversations foreign refs [`conversations/01`](../conversations/01_publish_and_refs.md).

---

## Status

**Peering has a real manual approval gate, cross-server content works —
both on a simpler/different trust store than 00/03 originally designed.**
The handshake completing (step 02) is NOT enough to trust a peer: a
`servers` row for the peer (and `connected = TRUE`) only gets created
when an admin explicitly approves the resulting `federation_attempt` row
(`POST /api/federation/attempts/{id}/approve`) — so the "deliberate gate
before trust" intent of step 03 did ship. What didn't: a separate
`federation_established` audit table (approval state lives on
`federation_attempt` + the peer's own `servers` row instead), and the
"different admin than whoever created the invite" rule — any single
admin can approve, including the one who set up the handshake. See each
step's own Status header for the precise shipped-vs-designed diff.

| Step | Status |
|------|--------|
| 00   | Superseded by the shipped design (02) |
| 01   | **Implemented** |
| 02   | **Implemented** (deviated — see doc for the exact `federation_attempt`/`federation_invitation` split) |
| 03   | **Implemented as a single-admin gate**, not the second-admin one this doc names — approval is real (on `federation_attempt`), but never checks the approver differs from the invite's creator |
| 04   | **Implemented** (trust store simplified, see 03) |
| 05   | **Implemented**, with two deviations: revocation is active rather than purely local (the disconnecting server notifies the peer, which auto-revokes back), and a root-only hard purge (delete the server row and all its local data) was added beyond this doc's scope |
| 06   | **Implemented**, via a per-operation "leg" pattern (`federation_relay.go`) rather than this doc's generic relay endpoints |
| 07   | **Gap** — shipped fire-and-forget (signed HTTP, short timeout, swallowed-on-failure), not the durable `servers.online`+backlog design here; an event to an unreachable peer is silently lost |

Beyond this doc set, `federation_relay.go` also implements mentions,
federated user search, and reed-stats/like propagation (18 legs total —
see the `// Leg N:` headers in that file) — none of these had a numbered
spec when built. Two feature areas still have **no** federation story at
all: account/plain-reed deletion (removal-notify only exists for
replies/echoes) and key revocation (no propagation to peers holding
content signed by a since-revoked key).

## Locked decisions (as designed — see each step's Status for what actually shipped)

| Topic | Decision |
|-------|----------|
| Discovery | **No** global directory — admins exchange **server public keys** OOB, then an **encrypted connection string** |
| Initiation | Admin **create invite** on initiator; payload encrypted to **remote server public key** |
| Handshake secret | Random **`secret`** in invite; initiator stores **hash only**; responder proves possession on `connect` |
| Callback | Responder POSTs to **`https://{initiator-base-url}/api/federation/connect/{inviteId}`** |
| Approval | **Shipped, single-admin only.** A `servers` row (hence trust) is created only when an admin calls the approve endpoint on `federation_attempt` — the handshake completing alone is not enough. But the "different admin than the invite's creator" rule is never enforced; any admin can approve, including the one who started the handshake |
| Durable peers | *Simplified from the design* — approval state lives on **`federation_attempt`**, trust itself on the peer's own **`servers`** row (`connected`, `revoked`, `fingerprint`); no separate `federation_established` table |
| Invite lifecycle | Status **`new`** → **`accepted`** → **`approved`** \| **`rejected`**; **`canceled`** (revoked while still `new`) and **`revoked`** (an `approved` connection later torn down) are distinct terminal states — never deleted |
| Visibility | **All admins** see **all** invites on the instance (not creator-only) |
| Admin UI | **Admin → Mesh** (create invite, paste connection string, list attempts); approve/reject live on a per-attempt detail page (`/mesh/attempt/{attemptId}`), not inline on the list |
| Revoke peering | *Designed but not shipped* — `servers.revoked` is checked everywhere (401s incoming peer traffic when true), but nothing ever sets it to `true`; there is no revoke action anywhere in the codebase |
| Content relay | Shipped as 18 purpose-built peer-HTTP "legs" in `federation_relay.go` (fetch, subscribe, notify for replies/echoes/mentions, stats push, federated search, ...) rather than this doc's generic `POST /api/federation/relay/reed` — see [06](06_content_relay.md) |
| Presence + durable delivery | *Designed but not shipped* — no `servers.online`, no online/offline/ping endpoints, no backlog. What ships instead is fire-and-forget: a signed HTTP call at event time, silently dropped if the peer doesn't answer — see [07](07_presence_delivery.md) |
| Non-goals (v1) | Open federation/discovery, automated reciprocal revoke, live client notification of a mid-session peering revoke. **Since shipped despite being a v1 non-goal:** federated follow (`forwardFollowToPeer`/`RecordRemoteFollower`). **Not built at all, locally or federated:** blocking/muting, direct/private messages |

## Motivation

Operators need a concrete, auditable ceremony — not manual DB pins — that binds
two instances cryptographically and requires **two admins on each side** where
possible before the link goes live.
