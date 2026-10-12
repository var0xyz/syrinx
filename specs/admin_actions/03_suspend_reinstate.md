# Admin actions 03 — Suspend / reinstate

## Status

Proposed.

## Depends on

[01](01_requests_and_approvals.md), [02](02_ledger.md)

## Context

Suspension pauses an account without touching it. It is **local only**:
nothing is sent to peers, and nothing changes for them.

## Schema

A new column in the `users` `CREATE TABLE` (`InitDB`):

```sql
suspended_by VARCHAR(255) REFERENCES admin_requests(id)
```

It is non-NULL while the user is suspended, and points at the request that
suspended them.

## Executors

| Action | Precondition | Effect |
|--------|--------------|--------|
| `suspend` | Target not suspended | `suspended_by = request.id`; close the target's open WebSockets |
| `reinstate` | Target suspended | `suspended_by = NULL` |

Both accept any number of targets. The targets come from the invite-tree
picker in [10](10_admin_spa.md), the same picker a kick uses.

## Enforcement

- **Auth middleware** (`middlewares.go`): after the revoked-key and
  account-removal checks, a suspended user gets `403` with their
  **suspension certificate** on every signed operation, ledger reads
  included. There is no exception: a suspended user learns nothing more
  about the network.
- **WebSocket**: the handshake is refused with the same certificate.
  Suspending someone closes their live connections.

## Suspension certificate

The `AdminAuthorization` ([01](01_requests_and_approvals.md)) of the
approved suspend request: the request with its reason, the approve
decision and the server signature. It is built from the request that
`users.suspended_by` points at, so nothing new is stored. The `403` body is
`pb.Error` with the certificate in its detail, the way a block refusal
carries its block.

The certificate is how the user learns why: they may or may not hold the
ledger entry, and nothing depends on it.
- **Unauthenticated reads** (profiles, keys, reed metadata) keep working.
  Their content stays visible wherever it is already held.

Their own device can't serve their reeds while it's disconnected, so content
held only on that device is unreachable until reinstatement. This is the only
effect peers might notice.

## SPA

On `403 suspended`, the client verifies the certificate (both admin
signatures and the server signature) and shows a suspended screen with its
reason instead of the app. Local data is kept.

## Tests

- A suspended user's signed request gets 403 with the certificate; a ledger
  read gets the same 403.
- WebSocket handshake refused; an open socket is closed on suspend.
- Reinstate restores access.
- Suspending an already suspended user is rejected when the request is opened.
