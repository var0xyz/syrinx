# Federation 11 — The peering is the signed record

## Status

Proposed. Lands after [10](10_peer_event_queue.md), and after the branches
pending merge.

## Depends on

[10](10_peer_event_queue.md): delivery to a peer no longer starts at its
`approved_at`, so a restored peer can get its real one back.

## Context

`/server/info` lists every peer to signed-in members with a **peer record**
this server countersigns, so their devices can hand it back after a wipe
and `RestorePeer` (`recovery_peers.go`) can rebuild the peering. The record
covers only `peerID`, `name`, `keyID`, `baseURL`, `frontendURL` and a derived
`status` (connected, revoked, purged).

Two problems:

- **The signature lives apart from the row.** `server_mesh.record_signature_id`
  points at the last signature and `server_mesh.record_fields` holds a
  `canonicalJSON` snapshot of what it covered, so `/server/info`
  (`currentPeerRecord`, `handlers.go`) can compare the snapshot with the
  current row on every request and sign again when they differ. The
  snapshot is a second copy of data the row already holds.
- **Most of the peering is lost in a wipe.** Who invited, approved,
  requested a disconnect of or revoked the peer, when and why, and when the
  peer approved us, are never in the record. `RestorePeer` sets
  `approved_at` and `peer_approved_at` to the restore time, `revoked_at` to
  the record's `signedAt`, and clears `revoked_by` and `revoked_reason`. The
  `approved_at` reset was required: delivery streams started there.

`server_mesh.connected` carries nothing: it is `TRUE` on every row except a
purged one, which is also revoked, and every query that reads it also
requires `revoked_at IS NULL`.

## Locked decisions

| Topic | Decision |
|---|---|
| What is signed | The peering itself: every `servers` and `server_mesh` field below, signed whenever one of them changes |
| Where the signature lives | `server_mesh.server_signature_id`, like every other countersigned row |
| When it's signed | In the transaction of every write that changes a signed field. `/server/info` never signs |
| Not signed | `down_at` (the peer's shutdown/boot state), `delivery_claimed_at` ([10](10_peer_event_queue.md)), `servers.created_at`. Live or local bookkeeping that means nothing after a wipe |
| Dropped | `server_mesh.record_signature_id`, `server_mesh.record_fields`, `server_mesh.connected` |
| Who sees it | Every signed-in member, as today. Who connected, revoked or disconnected a peer, and why, is already public in the ledger |
| Restore | Every signed field comes back as signed, the device's signature with it. A restored peer's queue starts empty |
| Blank slate | No migration; recreate the database |

## The record

`buildPeerRecordPayload` (`identity.go`), mirrored in `signing.ts`, through
`canonicalJSON`:

| Field | From | Absent when |
|---|---|---|
| `type` | `"peer-record"` | — |
| `serverID` | our id | — |
| `peerID` | `servers.id` | — |
| `name` | `servers.name` | — |
| `baseURL` | `servers.base_url` | — |
| `frontendURL` | `servers.frontend_url` | — |
| `keyID` | `server_mesh.key_id` | the pin was cleared (`ClearPeerPin`) |
| `invitedBy`, `invitedAt` | `server_mesh.invited_by`, `invited_at` | we accepted their invitation |
| `approvedBy`, `approvedAt` | `server_mesh.approved_by`, `approved_at` | — |
| `peerApprovedAt` | `server_mesh.peer_approved_at` | the peer hasn't approved us yet |
| `disconnectRequestedBy`, `disconnectRequestedAt`, `disconnectReason` | `server_mesh.disconnect_requested_*`, `disconnect_reason` | no disconnect is pending |
| `revokedBy`, `revokedAt`, `revokedReason` | `server_mesh.revoked_*` | not revoked. `revokedBy` is also absent when the peer disconnected first |
| `purgedAt` | `server_mesh.purged_at` | not purged |
| `signedAt` | the signature's time | — |

Times are `signedTime` (RFC 3339, seconds, `Z`). `canonicalJSON` drops the
absent ones. `status` goes: a reader derives it as purged (`purgedAt`),
revoked (`revokedAt`) or connected, and "established" (approved on both
sides) as connected with `peerApprovedAt`.

## Signing on write

One helper, `signPeeringTx(ctx, tx, serverID, countersign)`, in
`services.go`: reads the peer's `servers` and `server_mesh` row inside `tx`,
builds the payload, countersigns it, inserts the `server_signatures` row,
points `server_signature_id` at it and deletes the one it replaces. Every
writer below takes a `countersign` function, as `RepinPeerKey` and
`ApproveFederationAttempt` already do, and calls the helper before
committing. Writers that run outside a transaction today gain one.

| Writer | Changes | Notes |
|---|---|---|
| `ApproveFederationAttempt` | the `servers` row, the whole `server_mesh` row, and `peer_approved_at` when the peer already approved us | sign once, after both writes |
| `MarkPeerApproved` | `peer_approved_at` | called from the peer-auth middleware on every peer request; sign only when the `UPDATE` changed a row |
| `RepinPeerKey` | `key_id` | |
| `ClearPeerPin` | `key_id` → NULL | |
| `revokeFederationServerQ` (from `RevokeFederationServer` and `ConfirmFederationServerDisconnect`) | `revoked_*`, clears `disconnect_*` | `RevokeFederationServer` gains a transaction |
| `RequestFederationServerDisconnect` | `disconnect_*` | gains a transaction |
| `CancelFederationServerDisconnect` | clears `disconnect_*` | gains a transaction |
| `PurgeFederationServer` | deletes and re-inserts both rows, sets `purged_at` | keeps **every** signed field, not only the revocation it keeps today, then signs |
| `RestorePeer` | writes both rows from a device's record | stores that record's signature as-is; never signs |

A test fails if a `server_mesh` or `servers` write outside these leaves the
signature stale: after each writer, the stored signature verifies against
`buildPeerRecordPayload` of the row as it now stands.

## Wire

`FederatedServerInfo` (`identity.proto`) carries the record's fields plus
the signature, and the peer's key armor (unsigned, kept only when its
fingerprint is `key_id`'s, as today):

```proto
message FederatedServerInfo {
  string id = 1;
  string name = 2;
  string key_id = 3;
  string base_url = 4;
  string frontend_url = 5;
  string invited_by = 6;
  uint32 invited_at = 7;
  string approved_by = 8;
  uint32 approved_at = 9;
  uint32 peer_approved_at = 10;
  string disconnect_requested_by = 11;
  uint32 disconnect_requested_at = 12;
  string disconnect_reason = 13;
  string revoked_by = 14;
  uint32 revoked_at = 15;
  string revoked_reason = 16;
  uint32 purged_at = 17;
  ServerSignature server_signature = 18;
  string key_armor = 19;
}
```

`status`, `established` and `created_at` go: they were derived or unsigned.
`RecoveryServersRequest` keeps carrying `FederatedServerInfo` records.

## Steps

### 1. Schema and payload

- `InitDB`: `server_mesh` drops `connected`, `record_signature_id`,
  `record_fields`; gains `server_signature_id INT REFERENCES server_signatures(id)`.
- Every query reading `connected` drops the condition (it is implied by
  `revoked_at IS NULL`): `ListFederationServers`, `ListConnectedPeers`,
  `PeersAwaitingKey` if still present, `ListFederatedServers`,
  `ListDeliveryPeers` if still present, and the upserts in
  `ApproveFederationAttempt`, `PurgeFederationServer` and `RestorePeer`.
  `federationServerListRow.Connected` goes; the admin Mesh page derives it.
- `buildPeerRecordPayload` takes the whole peering. Golden bytes in
  `peer_record_payload_test.go` and `test:peer-record-payload`, one vector
  with every field and one with only the required ones.

### 2. Sign on write

- `signPeeringTx` and the writer changes in the table above.
- Drop `currentPeerRecord`, `FederatedServerInfo.recordFields`,
  `FederatedServerInfo.RecordFields`, `SetPeerRecord`.
- `ListFederatedServers` reads the signed fields and the signature;
  `GetServerInfo` returns them without signing.
- Tests: the stale-signature test above; `/server/info` makes no
  countersignature; a purge keeps the approval and invitation fields.

### 3. Restore

- `restorePeerRecord` verifies the full payload and refuses a record whose
  `keyID` isn't the peer's, as today.
- `RestorePeer` writes every field from the record, `approved_at` and
  `peer_approved_at` included, keeps the newest record per peer by
  `signedAt` (as today), and stores the device's signature row. It no
  longer touches delivery: the peer's queue is empty after a wipe.
- Tests: a restored peer's row equals the one signed before the wipe; an
  older record doesn't replace a newer one; a revoked record restores
  `revokedBy` and `revokedReason`.

### 4. SPA

- `signing.ts`: `buildPeerRecordPayload` mirrors the new fields.
- `verifyPeerRecord` (`lib/verifiers`) rebuilds from the wire fields.
- `federatedServers` repository: unchanged rule (keep the newest per peer
  by `signedAt`, never delete).
- Replace reads of `status` and `established` with helpers in
  `lib/services/peerServers.ts`: `peerStatus(peer)` (purged, revoked,
  connected) and `isEstablished(peer)`. `UserProfileCard.svelte` filters
  verification-link peers with them.
- `recoveryHoldings.ts` reports records as stored, unchanged.
- `npm run check`, `test:peer-record-payload`.

### 5. Docs and specs

- `specs/recovery_operational/README.md` step 04: the record is the whole
  peering, signed on write; `record_fields` gone.
- `docs/identity.md` (server recovery): peerings come back with who
  connected and revoked them, and why.
- `specs/federation/README.md`, `specs/README.md`: status rows.
