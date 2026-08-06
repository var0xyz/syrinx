# Federation 01 — Invitation create + `federation_invitation`

## Status

Proposed.

## Depends on

[00](00_design.md), [roles 01](../roles/01_role_store.md)

## Context

No federation tables or admin Network UI exist today.

## Scope

- DDL: **`federation_invitation`**
- Canonical signed bytes for initiator invite payload
- Encrypt connection payload to remote server public key
- Admin API + SPA: **Create invite** (Network tab)
- Revoke invites in **`new`**
- **All admins** list all invites

## Non-goals

- Responder paste path (02)
- `connect` callback (02)

## Design

### `federation_invitation`

```sql
CREATE TABLE federation_invitation (
    invite_id VARCHAR(255) PRIMARY KEY,
    secret_hash BYTEA NOT NULL,
    remote_fingerprint VARCHAR(255) NOT NULL,  -- B's key used for encryption
    created_by VARCHAR(255) NOT NULL REFERENCES users(id),
    status VARCHAR(16) NOT NULL DEFAULT 'new'
        CHECK (status IN ('new', 'accepted', 'approved', 'revoked')),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    accepted_at TIMESTAMP,
    approved_at TIMESTAMP
);
```

| Status | Meaning |
|--------|---------|
| **`new`** | Created; awaiting responder `connect` callback |
| **`accepted`** | Handshake received; **`federation_attempt`** pending approval |
| **`approved`** | Admin approved the link; **`federation_established`** row exists |
| **`revoked`** | Cancelled while still **`new`** |

Plaintext connection JSON is **not** stored — only shown once at create.
`secret_hash` = SHA-256 or bcrypt of handshake secret (lock one in impl).

Rows are **never deleted** — audit retention.

### Initiator signature

Server A signs canonical bytes (new helper in `signing/` or `federation/` —
**not** `BytesToSign` user envelope):

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

### Encryption

Input: plaintext JSON + remote server **OpenPGP public key** (paste armor).

Output: **connection string** — armored ciphertext (PGP message to public key).

Only Server B can decrypt (private key on B).

### API

| Method | Path | Auth | Purpose |
|--------|------|------|---------|
| `POST` | `/api/federation/invitations` | Admin | Create invite |
| `GET` | `/api/federation/invitations` | Admin | List **all** invites (any admin) |
| `POST` | `/api/federation/invitations/{inviteId}/revoke` | Admin | Revoke if **`new`** |

**POST body:**

```json
{
  "remotePublicKeyArmor": "-----BEGIN PGP PUBLIC KEY BLOCK-----…"
}
```

**201:** `{ "inviteId", "connectionString", "status": "new" }` — show
`connectionString` once; not retrievable later.

**GET list:** every row with `inviteId`, `status`, `createdBy`, `createdAt`,
timestamps — visible to **any** admin/root (not filtered to caller).

### SPA — Admin → Network → Invites

- **Create:** textarea for remote server public key → generate connection string
- **List:** all invites for the instance (creator username shown; all admins
  see the same list)
- **Revoke** on rows in **`new`** only

Only visible to **`admin`/`root`**.

### Tests

- Create → row `status=new`, `created_by` set
- Any admin can list all invites; user role → 403
- Revoke → `status=revoked`; connect rejects
- Non-admin → 403
