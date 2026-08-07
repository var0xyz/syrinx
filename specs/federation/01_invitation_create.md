# Federation 01 — Invitation create + `federation_invitation`

## Status

Implemented.

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
    remote_fingerprint VARCHAR(255) NOT NULL,  -- B's key used for encryption
    created_by VARCHAR(255) NOT NULL REFERENCES users(id),
    status VARCHAR(16) NOT NULL DEFAULT 'new'
        CHECK (status IN ('new', 'accepted', 'approved', 'revoked')),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    accepted_at TIMESTAMP,
    approved_at TIMESTAMP,
    reviewed_by VARCHAR(255) REFERENCES users(id),
    reviewed_at TIMESTAMP,
    connection_ciphertext TEXT
);
```

| Status | Meaning |
|--------|---------|
| **`new`** | Created; awaiting responder `connect` callback |
| **`accepted`** | Handshake received; **`federation_attempt`** pending approval |
| **`approved`** | Admin approved the link; **`federation_established`** row exists |
| **`revoked`** | Cancelled while still **`new`**; **`reviewed_by`** / **`reviewed_at`** set |

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
- Revoke → `status=revoked`; connect rejects
- Non-admin → 403
