# Admin actions 04 — Kick

## Status

Proposed.

## Depends on

[01](01_requests_and_approvals.md), [02](02_ledger.md)

## Context

A kick is final. The account and all its content leave the network and the
username is freed. It reuses the account-removal path, with one change: the
user's signature is replaced by two admins' signatures.

## Invite tree

`GET /api/admin/users/{userID}/invite-tree` (admin or root): the target's
inviter, the target, and every user below the target in the invite tree,
nested. Built with a recursive CTE over `users.invite_id →
invites.created_by`. Each node has user ID, username, role, suspended
flag and removed flag. Root appears but can't be selected. Admins appear but
can be selected only by root ([10](10_admin_spa.md) shows the lock).

The same endpoint feeds the suspend picker ([03](03_suspend_reinstate.md)).

## Schema

`account_removals` gains the admin variant. Exactly one of user signature or
admin request is set:

```sql
CREATE TABLE IF NOT EXISTS account_removals (
  user_id             VARCHAR(255) PRIMARY KEY REFERENCES identities(id) ON DELETE CASCADE,
  note                VARCHAR(140) NOT NULL DEFAULT '',
  public_key_id       VARCHAR(255) REFERENCES public_keys(id) ON DELETE CASCADE,
  user_signature_id   INT REFERENCES user_signatures(id),
  admin_request_id    VARCHAR(255) REFERENCES admin_requests(id),
  server_signature_id INT NOT NULL REFERENCES server_signatures(id),
  CONSTRAINT account_removals_note_len CHECK (char_length(note) <= 140),
  CONSTRAINT account_removals_signer CHECK (
    (admin_request_id IS NULL) = (public_key_id IS NOT NULL AND user_signature_id IS NOT NULL))
);
```

A kick's `note` is empty. The reason lives in the request.

## Payload

```go
func buildAccountKickServerPayload(serverID, userID, requestID, requestSignature,
    decisionSignature, serverKeyFingerprint string, signedAt time.Time) []byte
// {"decisionSignature","requestID","requestSignature","serverID","serverKeyFingerprint","signedAt","type":"account-kick","userID"}
```

A different `type` from self-deletion, so neither signature can be passed
off as the other.

## Wire

`AccountRemovalCert` (`common.proto`) gains `optional AdminAuthorization
admin = 6`. A kick cert has `admin` set and no `user_signature`.

## Executor

`kick`, per remaining target, in the approval transaction:

1. Insert the `account_removals` row (admin variant). The existing insert
   already clears the username, so it can be claimed again.
2. Revoke the target's unclaimed invites.
3. Mark the target `dropped` in every other pending request ([01](01_requests_and_approvals.md) moot handling).

After commit, for each target, the same fan-out as a self-deletion:
`fanoutAccountRemoval` locally, and `notifyForeignAccountRemovalToPeers`,
once per peer.

A kicked user's devices get `ACCOUNT_REMOVED` like any holder. The auth
middleware already refuses an account with a removal.

## Holder verification

`lib/verifiers` (account removal) and the Go peer verifier
(`AccountRemovalNotifyFromPeer`) accept a kick cert only if:

1. the request signature verifies against the requester's key `keyID`, and
   the decision is `approve` and verifies against the approver's key;
2. the target is in `request.targets`, and `request.action == "kick"`;
3. requester and approver both have a server-countersigned admin or root
   role, and they are different people unless the requester is root;
4. the server signature over `buildAccountKickServerPayload` verifies
   against the target's home server key, selected by fingerprint.

On success the holder purges exactly as for a self-deletion: reeds, profile,
key. On failure it keeps the data and ignores the event.

## Re-signup

Nothing blocks it. The username is free, and a new invite gives the person a
new user ID. They don't inherit anything from the old account.

## Security

Add to `RISKS.md`: "Account purge without the author's signature". The
mitigation is two admin signatures plus the public ledger. The residual risk
is a compromised server minting its own admins ([00 open questions](00_design.md#open-questions)).

## Tests

- Kick of user + two invitees: three removal certs, three fan-outs, usernames
  cleared, invites revoked.
- Holder rejects a cert where requester == approver (non-root), where the
  approver isn't an admin, where the action isn't `kick`, and where the
  target isn't in `targets`.
- A pending suspend on a kicked target goes moot.
- Peer path: one notify per peer, verified on the peer.
