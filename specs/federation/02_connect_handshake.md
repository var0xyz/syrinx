# Federation 02 — Connect handshake + `federation_attempt`

## Status

Proposed.

## Depends on

[01](01_invitation_create.md)

## Context

Server A holds invitations in **`new`**; Server B must decrypt, verify, and
callback without user session auth on A’s connect route.

## Scope

- Responder decrypt + verify path (admin API)
- **`POST /api/federation/connect/{inviteId}`** on initiator (allowlisted)
- DDL: **`federation_attempt`** with **`invite_id`** FK
- Invitation **`new` → `accepted`** (row retained)

## Non-goals

- Second-admin approval (03)
- Runtime peer requests (04)

## Design

### Responder signature

Server B signs canonical bytes binding response to invite:

```
inviteId: …
serverId: …
baseUrl: …
fingerprint: …
```

(Initiator `serverId` from decrypted payload included in verify input on A —
lock exact field list in impl tests.)

### `POST /api/federation/connect/{inviteId}`

- **Auth:** none (signature + secret); add rate limit by IP + invite id
- **Body:**

```json
{
  "serverId": "...",
  "baseUrl": "https://b.example",
  "fingerprint": "...",
  "signature": "...",
  "secret": "...",
  "respondedByUserId": "..."
}
```

`respondedByUserId` — admin on B who pasted the connection string (audit on A).

Initiator validates:

1. Invitation exists and **`status = new`**
2. `secret` matches hash
3. B’s signature verifies under `fingerprint`
4. `baseUrl` HTTPS scheme; optional SSRF guard on future outbound from A

**200** `{ "status": "accepted", "attemptId": "..." }`.

**403/404** on bad secret, unknown invite, bad signature, wrong invite status.

Side effects on **A**:

1. Insert **`federation_attempt`** (`invite_id` FK, `responded_by` from body,
   `status = pending_approval`)
2. Invitation → **`status = accepted`**, set `accepted_at`, clear
   **`connection_ciphertext`**

### Responder API (Server B)

| Method | Path | Auth | Purpose |
|--------|------|------|---------|
| `POST` | `/api/federation/invitations/accept` | Admin | Paste connection string |

**Body:**

```json
{
  "connectionString": "-----BEGIN PGP MESSAGE-----…"
}
```

Server B:

1. Decrypt `connectionString`
2. Read **`publicKeyArmor`** from decrypted payload; verify initiator
   `signature` with that key (must match payload `fingerprint`)
3. POST connect to `{baseUrl}/api/federation/connect/{inviteId}` (include
   `respondedByUserId` = caller admin id)
4. On success, insert local **`federation_attempt`** (`invite_id` from payload,
   **`responded_by`** = caller, `status = pending_approval`)

### `federation_attempt`

```sql
CREATE TABLE federation_attempt (
    attempt_id VARCHAR(255) PRIMARY KEY,
    invite_id VARCHAR(255) NOT NULL
        REFERENCES federation_invitation(invite_id),
    remote_server_id VARCHAR(16) NOT NULL,
    remote_base_url TEXT NOT NULL,
    remote_fingerprint VARCHAR(255) NOT NULL,
    responded_by VARCHAR(255) REFERENCES users(id),
    approved_by VARCHAR(255) REFERENCES users(id),
    status VARCHAR(32) NOT NULL DEFAULT 'pending_approval'
        CHECK (status IN ('pending_approval', 'approved', 'rejected')),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    approved_at TIMESTAMP
);
```

**On initiator A:** `invite_id` FK → local invitation; **`responded_by`** =
remote paste admin (from connect body). Creator is
**`federation_invitation.created_by`** (via FK).

**On responder B:** same `invite_id` string (A’s id) stored for correlation;
**no FK** to a local invitation row. **`responded_by`** = local paste admin.

`approved_by` set in step 03 on approval.

At most one **`pending_approval`** attempt per `invite_id` on A.

### SPA — Admin → Mesh → Accept

- Field: connection string

### Middleware

Add `/api/federation/connect/` to signature-auth **allowlist** (like invite
check).

### Tests

- Full A→B handshake: invitation `new` → `accepted`; attempt created
- Wrong secret → 403; invitation stays `new`
- Replay connect when invitation not `new` → 409
- Invalid initiator signature on B → 400 before POST
