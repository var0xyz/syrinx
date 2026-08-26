# Federation 05 — Revoke established peering

## Status

**The check exists; nothing can ever trigger it.** `servers.revoked`
(no separate `federation_established` table — see
[03](03_approval_established.md)'s status note) is read everywhere it
should be: `VerifyFederationPeer` (`services.go`) rejects incoming
peer-authenticated calls from a revoked peer, and
`GetServerByID`/`ListConnectedPeers` (`services.go`) exclude revoked
peers from outbound calls and the search-fanout target list. But grep
confirms `revoked` is never written as `TRUE` anywhere in the codebase —
no admin API, no SPA button, no code path of any kind sets it. This is a
**real gap, not just a missing UI**: today there is no way to actually
revoke a peer at all; a compromised or untrusted server, once
federated, stays trusted forever. `/api/federation/established/{serverId}/revoke`
and everything else in this doc's Design section is unbuilt.

## Depends on

[03](03_approval_established.md), [04](04_runtime_verify_display.md)

## Context

Communities need to cut trust without deleting audit history. Revocation is
**local and asymmetric** in v1: revoking on Server A does not change Server B
automatically.

## Scope

- **`federation_established.revoked`** boolean (+ audit columns)
- Admin API + SPA: revoke / show revoked state
- **401 Unauthorized** on all incoming federation traffic from a revoked peer
- Local outbound: treat revoked peer as disconnected (do not call)

## Non-goals

- Auto-revoke on peer (no callback/webhook to B)
- Forcing B to reciprocate
- Deleting **`federation_established`** rows
- Re-establish flow (future — new invite ceremony)

## Design

### Schema addition

On **`federation_established`**:

```sql
revoked BOOLEAN NOT NULL DEFAULT FALSE,
revoked_at TIMESTAMP,
revoked_by VARCHAR(255) REFERENCES users(id)
```

Row stays forever; **`revoked = true`** means this instance no longer trusts
or serves that peer. Invite/attempt rows keep **`approved`** for audit.

### Admin revoke

| Method | Path | Auth | Purpose |
|--------|------|------|---------|
| `POST` | `/api/federation/established/{serverId}/revoke` | Admin | Revoke peering |
| `GET` | `/api/federation/established` | Admin | List all (include `revoked`) |

**Revoke:**

1. Load row by `server_id`; must exist and `revoked = false`
2. Set `revoked = true`, `revoked_at`, `revoked_by`
3. Idempotent second call → **409** or **200** no-op (prefer **409**)

Any admin may revoke (same visibility as invite list). Optional: second-admin
rule for revoke — **not** required v1 (unlike approve).

### Incoming traffic (Server A revokes B)

All federation endpoints that accept **server-to-server** calls from peers
(e.g. **`GET /api/federation/users/{userID}/identity`**, future peer routes)
must:

1. Identify caller peer (`serverId` / fingerprint from peer auth — see 04)
2. Load **`federation_established`** for that `server_id`
3. If row missing **or** **`revoked = true`** → **`401 Unauthorized`**
   (empty body or `{ "error": "federation_revoked" }`)

Example: B still considers A live and calls A → A returns **401**. B's admin
sees failures in logs/metrics; must **manually revoke on B** if they want
symmetry.

### Outgoing traffic (Server A revokes B)

Local federation client (`VerifyRemoteUser`, resolve proxy, any outbound peer
HTTP):

- If **`federation_established.revoked`** for target → fail closed locally
  (`ErrFederationRevoked`); **do not** send request to B

Display: foreign refs for revoked peer → same as unpeered (“not connected”).

### SPA — Admin → Mesh → Established

- **Revoke** button on active (`revoked = false`) rows
- Revoked rows shown separately or greyed with `revoked_at` / `revoked_by`
- No “un-revoke” in v1 (new invite required to re-peer)

### Tests

- Revoke on A → incoming peer request from B → 401
- Revoke on A → outbound verify to B → local error, no HTTP
- B unchanged after A revokes (still `revoked = false` on B's row for A until
  B admin acts)
- Established row retained with `revoked = true`
