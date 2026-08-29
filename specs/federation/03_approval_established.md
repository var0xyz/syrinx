# Federation 03 — Second-admin approval + `federation_established`

## Status

**Implemented — on `federation_attempt`, not a separate
`federation_established` table.** A peer's `servers` row (and with it,
`connected = TRUE`/the fingerprint pin) is created only when an admin
calls `POST /api/federation/attempts/{id}/approve`
(`ApproveFederationAttempt`, `handlers.go`/`services.go`) — not
automatically at handshake time (see [02](02_connect_handshake.md)'s
corrected status). So this doc's core idea — "the handshake completing
is not enough; a deliberate admin action is required before the link is
live" — did ship, and so did the second-admin rule its title promises:

- **No `federation_established` table.** Approval state lives on
  `federation_attempt` (`approved_by`/`approved_at`/`rejected_by`/
  `rejected_at`/`rejected_reason` columns) plus the peer's own `servers`
  row (`connected`, `revoked`) — see [05](05_revoke_established.md),
  which now sets `revoked` via a real admin revoke action.
- **Second-admin check, enforced with one deviation.**
  `ApproveFederationAttempt` compares the approving admin against
  `federation_invitation.created_by` on the initiator side (the only
  side with a local "who created this" record) and rejects a same-admin
  approval with `errFederationSameApprover`. The deviation: root can
  bypass this check (`callerIsRoot`) — a deliberate escape hatch for a
  single-admin instance, not an oversight. The responder side, which
  never created a local invitation, has no creator to compare against,
  so any local admin may approve it there.
- **API paths differ**: the real endpoints are `POST
  /api/federation/attempts/{id}/approve` and `.../reject` (one pair,
  each side approves its own local `federation_attempt` row
  independently) — not the `/api/federation/pending` +
  `/api/federation/servers/{id}/approve` pair this doc designs. The SPA
  action lives on a per-attempt detail page
  (`/mesh/attempt/{attemptId}`), not inline on the Mesh list.

Treat this doc's *intent* (a deliberate gate before trust, enforced by a
second admin) as validated and shipped; treat its schema/API sections
(table names, endpoint shape) as historical design, not current
behavior.

## Depends on

[02](02_connect_handshake.md)

## Context

After a successful handshake: on the **initiator**, the invitation is
`status = accepted` with `server_id` set; on the **responder**, the peer
`servers` row is `connected = TRUE` with no local invitation. On neither
side is the link live for runtime verify (04) until a **different** local
admin approves. Audit chain: **established → invitation** (initiator) /
**established → servers row** (responder) — there's no attempt table in
between anymore.

## Scope

- DDL: **`federation_established`**
- Admin API: list pending (accepted-but-unapproved) links, approve, reject
- SPA: Admin → Mesh → Pending connections
- Status transitions on invitation (rows retained)

## Non-goals

- Notifying remote server on accept (optional later)
- De-establish / rotate (see [05](05_revoke_established.md))
- Deleting audit rows

## Design

### `federation_established`

```sql
CREATE TABLE federation_established (
    server_id VARCHAR(16) PRIMARY KEY REFERENCES servers(id),
    approved_by VARCHAR(255) NOT NULL REFERENCES users(id),
    established_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    revoked BOOLEAN NOT NULL DEFAULT FALSE,
    revoked_at TIMESTAMP,
    revoked_by VARCHAR(255) REFERENCES users(id)
);
```

`base_url`/`fingerprint` are not duplicated here — they already live on
`servers`, which this table's `server_id` FKs into, same reasoning as
[02](02_connect_handshake.md#implementation-notes).

**Audit queries:**

- Who invited (initiator side only): `federation_invitation.created_by`
- Who approved: `federation_established.approved_by` (mirrors
  `federation_invitation.reviewed_by`/`reviewed_at` when the invitation
  itself transitions to `approved`)

### Approval rules

Function `CanApproveFederationInvitation(approver, invitation)` — runs on
the **initiator**, where a real local invitation row exists:

- Approver is **`admin`** or **`root`**
- Approver **≠** `invitation.created_by` (the admin who created the invite
  cannot also approve it)
- **Single-admin instance:** if count(admins) == 1, allow sole admin to
  approve (log structured warning)

On the **responder**, there's no invitation row to check a creator against
— the equivalent function there, `CanApproveFederationServer(approver,
server)`, only excludes the admin who pasted the connection string
(recorded nowhere durable today; see open question below).

Ideal: ≥2 admins per instance so creator/paster ≠ approver.

> **Open question for implementation:** the responder's "who pasted"
> action isn't currently persisted anywhere outside `federation_log`
> (an unstructured log line, not a queryable actor field) — see
> [02](02_connect_handshake.md#federation_log). If the approval rule needs
> to exclude that specific admin programmatically, `servers` (or a new
> table) will need a real column for it, not just a log message. Decide
> this when 03 is actually implemented.

### API

| Method | Path | Auth | Purpose |
|--------|------|------|---------|
| `GET` | `/api/federation/pending` | Admin | List accepted invitations / connected-unapproved peers (all admins) |
| `POST` | `/api/federation/invitations/{id}/approve` | Admin | Approve (initiator side) |
| `POST` | `/api/federation/invitations/{id}/reject` | Admin | Reject (initiator side) |
| `POST` | `/api/federation/servers/{id}/approve` | Admin | Approve (responder side) |
| `POST` | `/api/federation/servers/{id}/reject` | Admin | Reject (responder side) |

**Approve (initiator):**

1. Validate rules
2. Set `invitation.reviewed_by = approver`, `reviewed_at = now()`,
   `status = approved`
3. Insert **`federation_established`** (`server_id` = `invitation.server_id`)

**Approve (responder):**

1. Validate rules
2. Insert **`federation_established`** (`server_id` = the peer's id)

**Reject (either side):** sets the invitation's (or, on the responder,
some equivalent marker on the `servers` row — TBD, see open question above)
status to `rejected`. No established row.

### SPA — Admin → Mesh

- **Invites:** all invitations + status (`new` / `accepted` / `approved` /
  `rejected` / `canceled` / `revoked`) — all admins see full list
- **Pending:** accepted invitations (initiator) and connected-but-unapproved
  peer servers (responder), each with server id, base URL, fingerprint,
  creator/paster, approver slot empty until approved
- **Accept** / **Reject** (disabled for the admin who created the invite /
  pasted the string)
- **Established:** live peers with audit links (who invited / approved)

### Tests

- Creator cannot approve their own invitation
- Second admin approves (initiator) → invitation `approved`,
  `federation_established` row referencing the right `server_id`
- Second admin approves (responder) → `federation_established` row
- Reject → invitation `rejected`; no established row
- All admins see the same pending list
