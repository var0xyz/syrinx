# Server key rotation 01 — Revocation records, `ops rotate-key`, drop `.rvk`

## Status

Implemented.

## Depends on

[00](00_design.md)

## Schema (`InitDB`)

- `private_keys`: `revoked_at TIMESTAMP` marks a revoked key. No reason or
  state columns: those belong to the signed revocation.
- New `private_key_revocations`, mirroring `public_key_revocations`:

```sql
CREATE TABLE IF NOT EXISTS private_key_revocations (
  key_id                 VARCHAR(255) PRIMARY KEY REFERENCES private_keys(id) ON DELETE CASCADE,
  reason                 TEXT NOT NULL DEFAULT '',
  compromised            BOOLEAN NOT NULL,
  successor              VARCHAR(255) NOT NULL UNIQUE REFERENCES private_keys(id),
  server_signature_id    INT NOT NULL REFERENCES server_signatures(id),  -- by the revoked key
  successor_signature_id INT NOT NULL REFERENCES server_signatures(id)   -- by the successor
);
```

Both signatures live in `server_signatures` under the key that made them, with
the revocation's `signed_at`. The successor's `public_keys.predecessor_id`
points at the revoked key, as for user keys.

## `ops rotate-key ["reason"] [--compromised]`

Offline, like the other `ops` commands (the running server holds its key in
memory and picks up the successor on restart). The reason is optional;
`--compromised` marks the revoked key compromised and then requires a reason.
Both arguments may come in any order. It first asks the operator to confirm,
explaining that nobody can sign up, import an account or recover one until the
new public key is handed out (those steps prove they know the current key),
and, with `--compromised`, that users and peers must enter the new key by
hand. It refuses without a terminal; a blank answer cancels. Then it calls
`revokeServerKey`, in one transaction:

1. Decrypt the current key with the server key passphrase.
2. Mint the successor; countersign its public-key record with **itself**, as
   the first key is.
3. Build the revocation and sign it with both keys.
4. Store the successor, the revocation and `predecessor_id`; set the old key's
   `revoked_at`; set `servers.signing_key` to the successor.

A compromised revocation with a blank reason is refused. Revoking a key that is
already revoked is refused (only the current key has no successor yet). Then
the command reminds the operator to restart and re-export the identity bundle,
and after a compromise, to hand out the new key (`ops print-key`).

## Boot

- Remove `ProcessRevocations`, its call in `main.go`, and the `.rvk`
  references in `specs/recovery/README.md`.
- `InitServerKey` mints a key **only** when the server has never had one. If
  the current key is revoked without a successor, or keys exist but none is
  current (a hand-edited DB), it refuses to boot and names the `ops` commands.

## Identity bundle

`export-identity` / `import-identity` carry each key's `revoked_at` and
predecessor, and every revocation with both signatures, so a restored server
keeps its chain. Revocations are ordered by walking the chain, not by time:
two may share a second.

## Tests

- Rotate then boot: the successor signs, the old key is revoked and not
  compromised, both signatures verify and are stored under their keys.
- Compromised revocation with a blank reason → refused; stored with its reason.
- Revoking a revoked key, or booting with one as current → refused.
- Bundle round trip across a rotation and a compromise keeps the chain.
- Go/SPA parity for the revocation payload (`test:server-key-payload`).
