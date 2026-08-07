# Federation 03 — Second-admin approval + `federation_established`

## Status

Proposed.

## Depends on

[02](02_connect_handshake.md)

## Context

After handshake, both servers hold **`federation_attempt`** rows in
`pending_approval`. Link must not be live until a **different** local admin
approves. Audit chain: **established → attempt → invitation**.

## Scope

- DDL: **`federation_established`** with **`attempt_id`** FK
- Admin API: list pending attempts, accept, reject
- SPA: Admin → Mesh → Pending connections
- Status transitions on invitation + attempt (rows retained)

## Non-goals

- Notifying remote server on accept (optional later)
- De-establish / rotate (see [05](05_revoke_established.md))
- Deleting audit rows

## Design

### `federation_established`

```sql
CREATE TABLE federation_established (
    server_id VARCHAR(16) PRIMARY KEY,
    attempt_id VARCHAR(255) NOT NULL UNIQUE
        REFERENCES federation_attempt(attempt_id),
    base_url TEXT NOT NULL,
    fingerprint VARCHAR(255) NOT NULL,
    approved_by VARCHAR(255) NOT NULL REFERENCES users(id),
    established_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    revoked BOOLEAN NOT NULL DEFAULT FALSE,
    revoked_at TIMESTAMP,
    revoked_by VARCHAR(255) REFERENCES users(id)
);
```

**Audit queries:**

- Who invited: `federation_invitation.created_by` via
  `attempt.invite_id → invitation`
- Who pasted (responder): `federation_attempt.responded_by`
- Who approved: `federation_attempt.approved_by` (= `established.approved_by`)

### Approval rules

Function `CanApproveFederationAttempt(approver, attempt, invitation)`:

- Approver is **`admin` or `root`**
- Approver **≠** `invitation.created_by` (invite creator cannot approve on
  initiator side)
- Approver **≠** `attempt.responded_by` (paste admin cannot approve)
- **Single-admin instance:** if count(admins) == 1, allow sole admin to approve
  (log structured warning)

Ideal: ≥2 admins per instance so creator/paster ≠ approver.

### API

| Method | Path | Auth | Purpose |
|--------|------|------|---------|
| `GET` | `/api/federation/attempts` | Admin | List pending (all admins) |
| `POST` | `/api/federation/attempts/{id}/approve` | Admin | Accept |
| `POST` | `/api/federation/attempts/{id}/reject` | Admin | Reject |

**Approve:**

1. Validate rules
2. Set `attempt.approved_by = approver`, `attempt.status = approved`,
   `attempt.approved_at = now()`
3. Insert **`federation_established`** (`attempt_id` FK)
4. Linked invitation → **`status = approved`**, `approved_at = now()`

**Reject:**

1. `attempt.status = rejected`
2. Invitation → **`status = new`** (handshake may be retried with same invite
   if operators coordinate) **or** stay **`accepted`** with rejected attempt —
   **locked: invitation returns to `new`**, `accepted_at` cleared, so a fresh
   connect can run. Prior attempt row stays `rejected` for audit.

### SPA — Admin → Mesh

- **Invites:** all invitations + status (`new` / `accepted` / `approved` /
  `revoked`) — all admins see full list
- **Pending attempts:** remote server id, URL, fingerprint, creator (via
  invite), responder, approver slot empty until approved
- **Accept** / **Reject** (disabled for invite creator and paste admin)
- **Established:** live peers with audit links (who invited / responded /
  approved)

### Tests

- Creator cannot approve own attempt
- Second admin approves → invitation `approved`, attempt `approved`,
  `federation_established` row with correct FK chain
- Reject → attempt `rejected`, invitation back to `new`; no established row
- All admins see same invite and attempt lists
