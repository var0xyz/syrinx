# Federation 04 — Runtime verify + foreign ref display

## Status

**Implemented, with the trust store simplified.** Peer authentication
(signed request, fingerprint verified against a pinned value) shipped
exactly as designed — `authenticateAsPeer`/`VerifyFederationPeer`
(`middlewares.go`/`services.go`), pinning against `servers.fingerprint`
directly rather than a separate `federation_established` row (see
[03](03_approval_established.md)'s status note — no such table exists,
but the approval gate it names does, on `federation_attempt`).
`GetFederationUserIdentity` (`handlers.go`) is the shipped IdP endpoint.
The display rule ("foreign, peered → resolve; foreign, not peered →
opaque ref") also shipped, in `MentionLink.svelte`/`proxyIfForeign`
(`handlers.go`) — "peered" here means an admin-approved `servers` row
(`connected = TRUE`, `revoked = FALSE`), matching this doc's original
intent: a completed handshake alone is not enough, approval is required
first (see 03).

## Depends on

[03](03_approval_established.md)

## Context

With peers in **`federation_established`**, foreign `(serverID, userID)` refs
can be resolved. Same dual-role model as the earlier federation draft, but
trust roots in **establishment ceremony** (pinned fingerprint from handshake)
not manual ops paste.

## Scope

- Peer-authenticated **`GET /api/federation/users/{userID}/identity`** on
  home instance (caller must be established peer)
- Local **`VerifyRemoteUser(serverID, userID)`** using
  **`federation_established`**
- SPA proxy **`GET /api/federation/resolve`**
- Display rules for foreign refs (peered vs not)

## Non-goals

- Cross-instance reed relay — see [06](06_content_relay.md), which
  depends on this step's peer-authentication design
- Federated follow

## Design

### Peer authentication (runtime)

Established **non-revoked** peer requests include peer proof — options (lock
one at impl):

- **HMAC** of timestamp with shared secret derived at handshake (extend connect
  to exchange long-lived `peerSecret` — **future**), or
- **v1:** mTLS or signed server-to-server request with **responder’s server
  signing key** verified against **`federation_established.fingerprint`**

Minimum v1: consumer server signs request; home verifies caller fingerprint
matches a row in **`federation_established`** with **`revoked = false`**.

If the row exists but **`revoked = true`**, respond **`401 Unauthorized`**
to the caller ([05](05_revoke_established.md)).

### IdP endpoint

```
GET /api/federation/users/{userID}/identity
```

Caller: established peer only. Response: countersigned identity snapshot +
active key fingerprint + server signature over wire map.

### Client helper

```go
func (f *Service) VerifyRemoteUser(ctx, foreignServerID, foreignUserID) (*RemoteIdentity, error)
```

1. Row in **`federation_established`** for `foreignServerID` with
   **`revoked = false`** → else not peered / revoked locally
2. HTTP to `{base_url}/api/federation/users/...` with peer auth
3. Verify response signature against pinned **`fingerprint`**

### Display ([03 cross-instance display — merged here])

| `serverID` | Established & not revoked? | UX |
|------------|----------------------------|-----|
| Local | — | Unchanged |
| Foreign | yes | Resolve username via verify/resolve |
| Foreign | no or revoked | “Not connected” / opaque ref |

### Tests

- Unpeered foreign → resolve fails closed
- Established + valid identity → display name
- Removed account on home → 404
