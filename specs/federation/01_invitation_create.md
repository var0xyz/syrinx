# Federation 01 — Invitation create + `federation_invitation`

## Status

Implemented, with deviations from the DDL below (see
[02's implementation notes](02_connect_handshake.md#implementation-notes)
for the reasoning):

- `remote_fingerprint` is `fingerprint`, FKing into `public_keys(fingerprint)`
  instead of being a bare unreferenced column — the armor itself is stored
  once in `public_keys` rather than duplicated across federation tables.
- **Correction to a claim previously made here:** this used to say no
  `federation_attempt` table exists — wrong, see
  [02](02_connect_handshake.md)'s status note. `federation_invitation`
  itself doesn't have an `attempt_id` column, though — the initiator's
  own `federation_attempt` row (created once the handshake callback
  arrives) links back via `federation_attempt.invitation_id`, not the
  other way around. `federation_invitation.server_id` (nullable FK to
  `servers`) is set once approval (see 03) actually creates that
  `servers` row — not merely once the callback confirms the invitation.
  `approved_at` doesn't exist on this table either — `reviewed_by`/
  `reviewed_at` cover every terminal action (cancel, approve, reject,
  revoke), since `status` already says which one.
- `status` has more values than shown below — see the updated table.

## Depends on

[00](00_design.md), [roles 01](../roles/01_role_store.md)

## Context

No federation tables or admin Mesh UI exist today.

## Scope

- DDL: **`federation_invitation`**
- Canonical signed bytes for initiator invite payload
- Encrypt connection payload to remote server public key
- Admin API + SPA: **Create invite** (Mesh tab)
- Revoke invites in **`new`**
- **All admins** list all invites

## Non-goals

- Responder paste path (02)
- `connect` callback (02)

## Design

### `federation_invitation`

```sql
CREATE TABLE federation_invitation (
    id VARCHAR(255) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    secret_hash BYTEA NOT NULL,
    fingerprint VARCHAR(255) NOT NULL REFERENCES public_keys(fingerprint),  -- B's key used for encryption
    created_by VARCHAR(255) NOT NULL REFERENCES users(id),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    accepted_at TIMESTAMP,
    server_id VARCHAR(16) REFERENCES servers(id),  -- set in 02, once a callback confirms this invitation
    status VARCHAR(16) NOT NULL DEFAULT 'new'
        CHECK (status IN ('new', 'accepted', 'approved', 'rejected', 'canceled', 'revoked')),
    reviewed_by VARCHAR(255) REFERENCES users(id),
    reviewed_at TIMESTAMP,
    connection_ciphertext TEXT
);
```

There is no `federation_attempt` table and no `approved_at` column — see
[02's implementation notes](02_connect_handshake.md#implementation-notes).
`reviewed_by`/`reviewed_at` record whoever performed the terminal action,
for whichever status it ended up in — one pair of columns for every
terminal transition, since `status` already disambiguates which action it
refers to.

| Status | Meaning |
|--------|---------|
| **`new`** | Created; awaiting responder `connect` callback |
| **`accepted`** | Handshake received (see 02); pending second-admin approval (see 03) |
| **`approved`** | Admin approved the link; a `servers` row for the peer now exists with `connected = TRUE` (see [03](03_approval_established.md)'s status note — approval lives on `federation_attempt`, not a `federation_established` table) |
| **`rejected`** | Admin rejected the link after it was accepted (see 03); **`reviewed_by`**/**`reviewed_at`** set |
| **`canceled`** | Revoked while still **`new`** — never redeemed; **`reviewed_by`**/**`reviewed_at`** set |
| **`revoked`** | An established (**`approved`**) connection was later torn down (see 05); **`reviewed_by`**/**`reviewed_at`** set |

Armored **connection string** (PGP ciphertext to remote public key) is stored in
**`connection_ciphertext`** while status is **`new`**, so admins can retrieve it
from the list. Cleared when the invite is **accepted** (connect handshake) or
**revoked**.
`secret_hash` = SHA-256 of handshake secret.

Rows are **never deleted** — audit retention.

### Initiator signature

Server A signs canonical bytes (`identity.BuildFederationInvitationPayload` —
**not** user `BytesToSign`):

```
inviteId: …
serverId: …
baseUrl: …
fingerprint: …
secret: …
```

(sorted keys, same no-escape rules as `BytesToSign` **only if** we add a
separate federation helper — do **not** mix into user `BytesToSign`).

Field **`signature`** in JSON = base64 detached PGP over those bytes with
**server signing key**.

Plaintext JSON also includes **`publicKeyArmor`** — armored initiator server
signing public key — so the responder can verify `signature` after decrypt
without a separate OOB key exchange at accept.

### Encryption

Input: plaintext JSON + remote server **OpenPGP public key** (paste armor at
create time only).

Output: **connection string** — armored ciphertext (PGP message to public key).

Only Server B can decrypt (private key on B).

### API

| Method | Path | Auth | Purpose |
|--------|------|------|---------|
| `POST` | `/api/federation/invitations` | Admin | Create invite |
| `GET` | `/api/federation/invitations` | Admin | List **all** invites (any admin) |
| `POST` | `/api/federation/invitations/{id}/revoke` | Admin | Revoke if **`new`** |

**POST body:**

```json
{
  "name": "Acme staging",
  "remotePublicKeyArmor": "-----BEGIN PGP PUBLIC KEY BLOCK-----…"
}
```

**`name`** — required admin label to identify the remote party (not included in
the encrypted connection payload).

**201:** `{ "inviteId", "connectionString", "status": "new" }`.

**GET list:** every row with `inviteId`, `name`, `status`, `createdBy`, `createdAt`,
timestamps — visible to **any** admin/root (not filtered to caller). Rows in
**`new`** with stored ciphertext also include **`connectionString`**. When an
admin revokes or approves, **`reviewedBy`**, **`reviewedByUsername`**, and
**`reviewedAt`** identify who acted and when.

### SPA — Admin → Mesh → Invites

- **Create:** name + textarea for remote server public key → generate connection string
- **List:** card per invite (like signup Invites); **name** as title; status badge;
  copy + revoke on pending rows; **Revoked by** / **Approved by** when reviewed

Only visible to **`admin`/`root`**.

### Tests

- Create → row `status=new`, `created_by` set, ciphertext stored
- List → `connectionString` on **`new`** rows; omitted after accept/revoke
- Accept (step 02) → ciphertext cleared
- Any admin can list all invites; user role → 403
- Revoke → `status=canceled`; connect rejects
- Non-admin → 403
