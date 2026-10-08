# Blocking 01 — `user_blocks` schema, canonical payload, countersign

## Status

Implemented.

## Depends on

[00](00_design.md)

## Schema

In `InitDB` (`db.go`), on the `user_signatures` / `server_signatures` FK
model ([signatures 00](../signatures/00_design.md)), the same shape as
`account_removals`:

```sql
CREATE TABLE IF NOT EXISTS user_blocks (
  user_id             VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
  blocked_user_id     VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
  public_key_id       VARCHAR(255) NOT NULL REFERENCES public_keys(id) ON DELETE CASCADE,
  user_signature_id   INT NOT NULL REFERENCES user_signatures(id),
  server_signature_id INT NOT NULL REFERENCES server_signatures(id),
  PRIMARY KEY (user_id, blocked_user_id),
  CONSTRAINT user_blocks_not_self CHECK (user_id <> blocked_user_id)
);
CREATE INDEX IF NOT EXISTS idx_user_blocks_blocked_user ON user_blocks (blocked_user_id);
```

- `user_id` is the blocking user, named as account and reed removals name
  their signer.
- One table for blocks made here and blocks received from peers. On the
  blocking user's server they are local; on a peer that received the block,
  they are foreign and the blocked user is local.
- `idx_user_blocks_blocked_user` serves the hot check "is this viewer blocked
  by this author", run on every refusal path in [03](03_enforcement.md).
- No delivery state: the block is a resource, and what is delivered is an
  event about it ([04](04_realtime.md)).

## Canonical payload

In `identity.go`, one builder per side, used by signer and verifier, shaped
like the like payloads:

```go
func buildBlockUserPayload(userID, blockedUserID, keyID string) []byte
// {"blockedUserID","keyID","type":"block","userID"}

func buildBlockServerPayload(userID, blockedUserID, serverKeyFingerprint, userSignature string, signedAt time.Time) []byte
// {"blockedUserID","serverKeyFingerprint","signedAt","type":"block","userID","userSignature"}
```

- `identityTypeBlock = "block"`.
- `keyID` names the blocking user's signing key, so a rotation mid-request can't
  fail verification. No `serverID`: both user IDs and the key ID already
  carry it.
- The SPA mirrors both in `signing.ts` byte for byte; golden bytes are
  asserted by `block_payload_test.go` and `npm run test:block-payload`.

## Wire

`BlockCert` (`db.go`), nested signatures as every signed resource:

```json
{
  "type": "block",
  "userID": "k3x9@a.example",
  "blockedUserID": "p7q2@b.example",
  "userSignature":   { "id": "k3x9@a.example/<fp>", "armor": "..." },
  "serverSignature": { "id": "a.example/<fp>", "armor": "...", "timestamp": "2026-10-08T12:00:00Z" }
}
```

`proto/websocket.proto` gains a `BlockCert` message with the same fields,
carried by `USER_BLOCKED` ([04](04_realtime.md)).

## Countersign

`DataService.InsertBlock` (`blocks.go`) verifies nothing itself; the handler
([02](02_api.md)) has already verified the user signature and refused a
revoked signer. It inserts the user and server signature rows and the
`user_blocks` row in one transaction. On a primary key conflict it returns
the stored certificate unchanged: blocking twice is idempotent and yields
the same countersignature.

## Tests

- Go/SPA payload parity vectors for both builders.
- Insert twice returns the identical certificate; self-block violates the
  check constraint.
