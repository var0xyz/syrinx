# Federation (explicit peering)

Cross-instance trust via an **encrypted invitation handshake** between
admins, a server-to-server **`/api/federation/connect/{inviteId}`**
callback, then a manual admin **approval** step before the peer is
actually trusted. (A second-*admin* rule — approver must differ from
whoever set up the handshake — is enforced on the initiator side (root
can bypass it); a separate `federation_established` table was originally
designed but never shipped — approval state lives on
`federation_attempt`/`servers` instead. See the Status section below.)

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
before trust" intent of step 03 did ship, and so did its second-admin
rule (initiator side; root can bypass — see [03](03_approval_established.md)'s
Status). What didn't: a separate `federation_established` audit table —
approval state lives on `federation_attempt` + the peer's own `servers`
row instead. See each step's own Status header for the precise
shipped-vs-designed diff.

| Step | Status |
|------|--------|
| 00   | Superseded by the shipped design (02) |
| 01   | **Implemented** |
| 02   | **Implemented** (deviated — see doc for the exact `federation_attempt`/`federation_invitation` split) |
| 03   | **Implemented**, including the second-admin check this doc names — approval is real (on `federation_attempt`) and rejects a same-admin approval on the initiator side; root can bypass |
| 04   | **Implemented** (trust store simplified, see 03) |
| 05   | **Implemented**, with two deviations: revocation is active rather than purely local (the disconnecting server notifies the peer, which auto-revokes back), and a root-only hard purge (delete the server row and all its local data) was added beyond this doc's scope |
| 06   | **Implemented**, via purpose-built per-operation endpoints (`federation_relay.go`) rather than this doc's generic relay endpoints |
| 07   | **Out of scope for v1, by design.** Fire-and-forget (signed HTTP, short timeout, swallowed-on-failure) is the accepted permanent v1 behavior, not a gap to close later — no `servers.online`, no ping, no backlog/replay planned. |

Beyond this doc set, `federation_relay.go` also implements mentions,
federated user search, and reed-stats/like propagation — none of these
had a numbered spec when built.

**Account and reed deletion propagation — implemented.** Removal-notify
covers replies/echoes (`notifyForeignReplyRemovalToPeer`,
`notifyForeignEchoRemovalToPeer`, `notifyForeignReplyRemovalToViewer`),
whole accounts (`notifyForeignAccountRemovalToPeers`/
`AccountRemovalNotifyFromPeer`), and plain reeds
(`notifyForeignReedRemovalToPeers`/`ReedRemovalNotifyFromPeer`) — all in
`federation_relay.go`. Peer discovery for the account/reed legs is
holder-based (`reed_server_allocations`, via
`GetForeignHolderServersForAuthor`/`GetForeignHolderServersForReed`), not
follower- or subscriber-based: subscriptions are a peer-local concern
resolved once that peer has the cert, not the signal for who needs to be
told in the first place.

**Key revocation propagation — explicitly out of scope, not an oversight.**
Keys are never broadcast/pushed to peers. When a peer encounters a key
it doesn't recognize, it requests it (pull-based), same as any other
content. There is no plan to notify peers when a key is revoked.

## Locked decisions (as designed — see each step's Status for what actually shipped)

| Topic | Decision |
|-------|----------|
| Discovery | **No** global directory — admins exchange **server public keys** OOB, then an **encrypted connection string** |
| Initiation | Admin **create invite** on initiator; payload encrypted to **remote server public key** |
| Handshake secret | Random **`secret`** in invite; initiator stores **hash only**; responder proves possession on `connect` |
| Callback | Responder POSTs to **`https://{initiator-base-url}/api/federation/connect/{inviteId}`** |
| Approval | **Shipped, including the second-admin rule.** A `servers` row (hence trust) is created only when an admin calls the approve endpoint on `federation_attempt` — the handshake completing alone is not enough. On the initiator side, the approving admin must differ from the invite's creator (`errFederationSameApprover`); root can bypass this check. The responder side has no local invite-creator record to compare against, so any admin there may approve |
| Durable peers | *Simplified from the design* — approval state lives on **`federation_attempt`**, trust itself on the peer's own **`servers`** row (`connected`, `revoked`, `fingerprint`); no separate `federation_established` table |
| Invite lifecycle | Status **`new`** → **`accepted`** → **`approved`** \| **`rejected`**; **`canceled`** (revoked while still `new`) and **`revoked`** (an `approved` connection later torn down) are distinct terminal states — never deleted |
| Visibility | **All admins** see **all** invites on the instance (not creator-only) |
| Admin UI | **Admin → Mesh** (create invite, paste connection string, list attempts); approve/reject live on a per-attempt detail page (`/mesh/attempt/{attemptId}`), not inline on the list |
| Revoke peering | **Shipped**, with two deviations from the original local/asymmetric design — see [05](05_revoke_established.md)'s Status: the disconnecting server notifies the peer (which auto-revokes back), and a root-only hard purge was added beyond scope |
| Content relay | Shipped as purpose-built peer-HTTP endpoints in `federation_relay.go` (fetch, subscribe, notify for replies/echoes/mentions/account/reed removal, stats push, federated search, ...) rather than this doc's generic `POST /api/federation/relay/reed` — see [06](06_content_relay.md) |
| Presence + durable delivery | *Designed but not shipped* — no `servers.online`, no online/offline/ping endpoints, no backlog. What ships instead is fire-and-forget: a signed HTTP call at event time, silently dropped if the peer doesn't answer — see [07](07_presence_delivery.md) |
| Non-goals (v1) | Open federation/discovery, automated reciprocal revoke, live client notification of a mid-session peering revoke. **Since shipped despite being a v1 non-goal:** federated follow (`forwardFollowToPeer`/`RecordRemoteFollower`). **Not built at all, locally or federated:** blocking/muting, direct/private messages |

## Motivation

Operators need a concrete, auditable ceremony — not manual DB pins — that binds
two instances cryptographically and requires **two admins on each side** where
possible before the link goes live.
