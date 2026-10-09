# Admin actions 06 — Lost-device key rebind

## Status

Proposed.

## Depends on

[01](01_requests_and_approvals.md), [02](02_ledger.md);
[account recovery](../account_recovery/README.md)

## Context

For a user who lost their only device **and** has no key backup. Rotation
today (`POST /keys`) needs the old key to sign its own revocation, and that
key is gone. Two admins sign the revocation instead, after confirming out of
band that the person is who they say.

Anyone with a `.sxi.gpg` uses account recovery, not this.

## Lost-device page

`routes/lost-device/+page.svelte`, public, no session:

1. The user types their user ID, with or without `@serverID`. A bare ID
   gets this server's ID appended. There is no username lookup and no network
   call: the page never reveals whether an ID exists.
2. The client mints a key pair and stores the private key in IndexedDB as a
   **pending rebind** for that user ID (`pendingRebind` store).
3. The user downloads `<userID>.pub.txt.gz`: the armored public key, gzip-
   compressed, user ID in the filename.
4. The page shows the key's fingerprint and tells the user to give the file
   to an admin and read them the fingerprint.

If the page is opened again on the same device with a pending rebind, it
shows the same file and fingerprint instead of minting another key.

The file is not signed. Its authenticity is the admins' out-of-band check.

## Admin upload

In [10](10_admin_spa.md), the admin uploads the file. The SPA decompresses
it, takes the user ID from the filename, and shows the user and the key
fingerprint for the admin to compare. Opening the request:

- `action = rebind`, `targets = [userID]`, `params` = the armored public key.
- Rejected when the target is root, removed or suspended, or when the key
  doesn't parse.

## Executor

`rebind`, in the approval transaction:

1. **Revoke** the target's active key with reason `lost`, through a new
   admin-signed revocation variant (below). Old reeds stay valid.
2. **Bind** the uploaded key as the user's key, with `public_keys.restricted
   = TRUE` and successor of the revoked key.
3. Fan out the revocation through the existing key-revocation events (to
   holders, and once per peer).

### Admin-signed revocation

`revocations` gains `admin_request_id`. Exactly one of the user signature or
the admin request is set, the same shape as [04](04_kick.md):

```go
func buildAdminRevocationServerPayload(userID, keyID, requestID, requestSignature,
    decisionSignature, serverID, serverKeyFingerprint string, signedAt time.Time) []byte
// {"decisionSignature","keyID","reason":"lost","requestID","requestSignature","serverID","serverKeyFingerprint","signedAt","type":"admin-revocation","userID"}
```

`KeyRevocationCert` gains `optional AdminAuthorization admin = 8`. Verifiers
(`lib/verifiers`, Go revocation-event handler) accept it under the same
rules as a kick cert ([04](04_kick.md) holder verification), with
`action == "rebind"` and the key being the target's active key at request
time.

## Restricted key

Schema: `public_keys.restricted BOOLEAN NOT NULL DEFAULT FALSE`.

- **Auth middleware**: a request signed by a restricted key gets
  `403 {"error":"restricted-key"}`, with one exception: `POST /keys` rotating
  away from that same key. Reeds, profile updates, follows, likes and
  everything else are refused.
- **WebSocket**: the handshake with a restricted key is accepted only far
  enough to tell the client to rotate. No subscriptions and no dispatch.
- `POST /keys` (`AddPublicKey`) with a restricted revoked key: the normal
  rotation, with the restricted key signing its own revocation (reason
  `rotated`) and the new key. The new key is not restricted.

## Client

When the SPA finds a `pendingRebind` for a user ID, it tries signing in with
that key. On success it immediately:

1. mints a fresh key;
2. rotates to it via `POST /keys`, signed by the restricted key;
3. deletes the pending rebind and continues like a keys-only account
   recovery: bootstrap, following list, rehydration of own reeds.

Until the rotation succeeds, the app shows only a "finishing recovery" screen.
It never opens the composer.

## Vouches

A client holding a vouch for the revoked key, given or received, that sees
an `admin-revocation` shows a warning on that profile: "This account's key
was replaced by admins". The vouch does **not** carry over to the new key.
The voucher has to verify the person again.

## Security

Add to `RISKS.md`: "Admin key rebind is an account takeover by two admins".
The mitigations are the two-person rule, the public ledger entry, the visible
`admin-revocation` and the vouch reset.

## Tests

- Rebind executor: old key revoked with `admin-revocation`, new key bound and
  restricted.
- Restricted key: a reed publish gets 403; a profile update gets 403;
  `POST /keys` rotation succeeds and clears the restriction.
- Verifier rejects an admin revocation with the wrong action or target, or
  with an approver who isn't an admin.
- Lost-device page makes no network request before the download.
