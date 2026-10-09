# Admin actions 01 — Requests, approvals and approver eligibility

## Status

Proposed.

## Depends on

[00](00_design.md)

## Context

Every admin action except reserved usernames ([07](07_reserved_usernames.md))
and flag review ([08](08_flags.md)) goes through one request → approval
pipeline. This step builds the pipeline with no actions behind it. Steps
03–06 and 09 each plug in one action kind.

## Schema

In `InitDB` (`db.go`), on the `user_signatures` / `server_signatures` model:

```sql
CREATE TABLE IF NOT EXISTS admin_requests (
  id                    VARCHAR(255) PRIMARY KEY,
  action                VARCHAR(32) NOT NULL
    CHECK (action IN ('suspend', 'reinstate', 'kick', 'promote', 'demote',
                      'rebind', 'broadcast')),
  requester_id          VARCHAR(255) NOT NULL REFERENCES identities(id),
  requester_key_id      VARCHAR(255) NOT NULL REFERENCES public_keys(id),
  requester_signature_id INT NOT NULL REFERENCES user_signatures(id),
  reason                TEXT NOT NULL CHECK (char_length(reason) BETWEEN 1 AND 1000),
  params                BYTEA,          -- action-specific, e.g. rebind public key
  created_at            TIMESTAMP NOT NULL,
  state                 VARCHAR(16) NOT NULL DEFAULT 'pending'
    CHECK (state IN ('pending', 'executed', 'rejected', 'withdrawn', 'moot')),
  closed_by             VARCHAR(255) REFERENCES identities(id),
  closer_key_id         VARCHAR(255) REFERENCES public_keys(id),
  closer_signature_id   INT REFERENCES user_signatures(id),
  server_signature_id   INT REFERENCES server_signatures(id),
  closed_at             TIMESTAMP
);

CREATE TABLE IF NOT EXISTS admin_request_targets (
  request_id  VARCHAR(255) NOT NULL REFERENCES admin_requests(id) ON DELETE CASCADE,
  user_id     VARCHAR(255) NOT NULL REFERENCES identities(id),
  dropped     BOOLEAN NOT NULL DEFAULT FALSE,
  PRIMARY KEY (request_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_admin_request_targets_user
  ON admin_request_targets (user_id) WHERE NOT dropped;
```

- A broadcast has no targets. Every other action has at least one.
- `closed_by` is the approver (`executed`, `rejected`), the requester
  (`withdrawn`), or NULL (`moot`, closed by the server).
- `server_signature_id` is set once the request closes, for every outcome.

## Canonical payloads

In `identity.go`, one builder per signer, mirrored in `signing.ts`:

```go
func buildAdminRequestPayload(requestID, action, requesterID, keyID, reason string,
    targets []string, params string, createdAt time.Time) []byte
// {"action","createdAt","keyID","params","reason","requestID","requesterID","targets","type":"admin-request"}

func buildAdminDecisionPayload(requestID, decision, deciderID, keyID, requestSignature string) []byte
// {"decision","deciderID","keyID","requestID","requestSignature","type":"admin-decision"}
// decision ∈ approve | reject | withdraw

func buildAdminServerPayload(requestID, state, requestSignature, decisionSignature,
    serverID, serverKeyFingerprint string, signedAt time.Time) []byte
// {"decisionSignature","requestID","requestSignature","serverID","serverKeyFingerprint","signedAt","state","type":"admin-request"}
```

- `targets` is sorted before signing, so both sides produce the same bytes.
- `params` is base64 of the action-specific bytes (the rebind public key,
  the broadcast body). It is empty for most actions and then dropped,
  following the `canonicalJSON` rules.
- Embedding the request signature in the decision means an approval is
  bound to exactly one request.

## Wire

`proto/admin.proto` (new):

```proto
message AdminRequest {
  string id = 1;
  string action = 2;
  string requester_id = 3 [json_name = "requesterID"];
  string reason = 4;
  repeated string targets = 5;
  bytes params = 6;
  uint32 created_at = 7;
  UserSignature requester_signature = 8;
  string state = 9;
  repeated string dropped_targets = 10;
  optional AdminDecision decision = 11;
  optional ServerSignature server_signature = 12;
}

message AdminDecision {
  string decision = 1;
  string decider_id = 2 [json_name = "deciderID"];
  UserSignature signature = 3;
}

// Proof that two admins authorized an action. Embedded in the certificates
// actions produce (kick removal, rebind revocation).
message AdminAuthorization {
  AdminRequest request = 1;   // with its approve decision and server signature
}
```

## API

All routes need signature auth, and the caller must be admin or root.

| Route | Does |
|-------|------|
| `POST /api/admin/requests` | Open a request (body: `AdminRequest` with requester signature) |
| `GET /api/admin/requests?state=pending` | List requests, newest first, keyset cursor |
| `GET /api/admin/requests/{id}` | One request |
| `POST /api/admin/requests/{id}/decision` | Approve, reject or withdraw (body: `AdminDecision`) |

The server rejects a new request when:

- the requester isn't admin or root;
- a target is root, isn't a local user, or already has an account removal;
- the action and targets don't fit (e.g. `promote` on an admin, `reinstate`
  on someone who isn't suspended, `suspend` on someone already suspended);
- the reason is empty.

## Approver eligibility

Checked when a decision of `approve` or `reject` is submitted:

| Rule | Check |
|------|-------|
| Admin or root | `users.role` |
| Not the requester, unless root | `decider_id <> requester_id OR decider is root` |
| Not in the requester's invite subtree | recursive CTE over `users.invite_id → invites.created_by`, from the requester down |
| Not a target | `admin_request_targets` |
| Root, if any remaining target is an admin | Roles are read **at decision time**: a target promoted while the request is pending needs root |

`withdraw` is allowed only from the requester. A failed check returns 403
with the rule that failed, and the request stays pending.

## Execution

On `approve`, inside one transaction:

1. Re-check every eligibility rule and the action's own preconditions.
2. Run the action's executor (registered per action by steps 03–06, 09).
3. Countersign with `buildAdminServerPayload`, `state = executed`.

If the executor fails, the transaction rolls back and the request stays
pending.

## Moot requests

When an account removal is stored, whether a self-deletion or a kick, every
pending request targeting that user marks the target `dropped`. A request
whose targets are now all dropped closes as `moot`, with a server signature
and no decision. A broadcast never becomes moot.

A user who stops being an admin doesn't make their own pending requests
moot. The requester's role is checked again at approval, and a request
from a non-admin can only be rejected or withdrawn.

## Tests

- Payload golden bytes, Go and SPA (`admin_payload_test.go`,
  `npm run test:admin-payload`).
- Eligibility: self-approval refused for an admin and allowed for root; a
  direct invitee is refused; a grandchild invitee is refused; a target is
  refused; an admin target needs root.
- Moot: self-deletion of the only target closes the request as moot; of one
  of two targets, it drops that target and keeps the request pending.
