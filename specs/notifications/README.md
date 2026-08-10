# Notifications

Umbrella spec for every way this app tells a user something. Three
distinct mechanisms, defined here together so they're never confused with
each other:

- **Alert** — client-only, ephemeral, about the current app/client state.
- **Mailbox message** — server → one specific user, private, encrypted.
- **Admin mention** — server/admin → everyone, public, repliable.

**Supersedes [prerequisite 11](../11_user_notifications.md)**, whose
original motivation (telling a username-collision "loser" they'd been
renamed) is stale — see that file's superseded notice.

| # | Title | Depends on | Status |
|---|-------|------------|--------|
| [00](00_glossary_and_design.md) | Glossary + design + locked model | — | Proposed |
| [01](01_everyone_handle.md) | `@everyone` admin broadcast handle | 00 | Proposed |
| [02](02_mentions_tab.md) | Mentions tab (list API + SPA) | 00, 01 | Proposed |
| [03](03_mailbox_message_schema_and_producers.md) | Mailbox message schema + `SendMailboxMessage` + `ops mailbox-send` | 00 | Proposed |
| [04](04_mailbox_message_ws_delivery.md) | Mailbox message WS delivery + ACK-and-delete | 03 | Proposed |
| [05](05_mailbox_message_spa_bell.md) | SPA bell + `/mailbox/[id]` detail | 04 | Proposed |

## Glossary

| Term | What | Trigger | Storage | Delivery | Reply? | Lifetime |
|------|------|---------|---------|----------|--------|----------|
| **Alert** *(existing, unchanged)* | Client-only UI feedback about current app/client state (e.g. "copied to clipboard", "signup failed") | Any client code, no server round-trip | None — in-memory Svelte store | Immediate, local only | n/a | Auto-dismiss, ephemeral |
| **Mailbox message** | Server → one specific user, private | Internal server code (errors/detail) or an admin via `ops` CLI | Server DB, row **encrypted** to the recipient's active public key | WS push + client ACK; catch-up on reconnect | **No** — one-way | Deleted from the server once delivered + ACKed |
| **Admin mention** | Server/admin → every local user, public | An admin authors a reed containing the literal `@everyone` token | A normal signed reed + `reed_mentions` rows (one per local user) | Same as any reed — surfaced in the **mentions tab** | **Yes** — it's a real reed; anyone can reply | Same as any other reed — permanent until a deletion cert removes it |

### Which one to use

- **Alert**: the client itself has something to say about what just
  happened locally (a request succeeded/failed, a copy-to-clipboard
  confirmation). Never involves the server sending anything.
- **Mailbox message**: something specific to *this one user* that doesn't
  need or want a public reply — an error detail, an account event, a
  targeted heads-up from the server or an admin. One-way by design.
- **Admin mention**: something the admin wants to be public and/or wants
  the userbase to be able to discuss — an announcement, an incident
  update, anything where replies are useful. Uses `@everyone` for "reach
  everyone" the same way a regular `~userID@serverID` mention reaches one
  user.

### Alert implementation note

The existing toast component (`spa/src/lib/components/Notifications.svelte`)
and store (`spa/src/lib/stores/notifications.ts`) — wired into
`+layout.svelte` — already implement what this glossary calls **Alert**.
This spec documents the term and its place in the taxonomy but makes
**no code change** to that system — it already works, and this proposal's
job is naming/scope clarity, not a rewrite.

## Locked decisions

| Topic | Decision |
|-------|----------|
| Mailbox message storage | No plaintext at rest server-side. Payload is JSON, marshaled, then encrypted to the recipient's current public key (`crypto.Service.Encrypt`, same pattern as the federation connection payload). |
| Mailbox message delivery | WS push while online; the DB row itself is the pending-delivery record (no separate `pending_*` table) — exists = undelivered, deleted on `DATA_ACK`-equivalent. Catch-up on reconnect queries remaining rows for that user. |
| Mailbox message producers | Primary: **internal server helper**, callable from any request/handler that needs to report user-specific detail beyond a short HTTP error. Secondary: **`ops mailbox-send`** CLI command for manual one-off admin messages — a thin wrapper over the same helper. No HTTP admin endpoint in v1. |
| Mailbox message reply | None. If a reply matters, the sender should use an admin mention instead. |
| `@everyone` mechanism | Not a `~userID@serverID` token variant (that pattern resolves to one real, existing user). A distinct, admin-gated expansion at publish time: one `INSERT ... SELECT id FROM users` into `reed_mentions`, inside the same transaction as the reed write. |
| `@everyone` authorization | `roles.IsAdmin(role)` at publish time. Non-admin authors' literal `@everyone` text is inert — stored as plain content, never expanded. |
| `@everyone` server scope | This server's own `users` table only. Federation does not extend it — see [00](00_glossary_and_design.md) for why. |
| Broadcast reliability | Transaction atomicity already prevents *partial* fanout (Postgres rolls the whole write back on any failure). It does **not** guarantee an attempted `@everyone` broadcast that failed gets retried — nothing today notices a failed attempt. A durable job-tracking sketch (`admin_mentions`, start/complete timestamps) is documented in [00](00_glossary_and_design.md) as a known gap and future design, **not implemented** by this spec. |
| Key rotation vs. pending mailbox messages | A message encrypted to a key that is later rotated before delivery becomes undecryptable on the new key. Accepted v1 gap, not solved. |

## Cross-links

- Mention extraction + indexing: [conversations 04](../conversations/04_mentions.md).
- `reed_mentions` schema, `CreateReed` transaction: [`db.go`](../../db.go), [`services.go`](../../services.go).
- Role check: [`roles.IsAdmin`](../../roles/roles.go).
- Encrypt-to-recipient precedent: [`crypto.Service.Encrypt`](../../crypto/crypto.go), used by [federation](../federation/README.md) connection payloads.
- WS ACK pattern reference: [deletion 04](../deletion/04_reed_fanout.md).
- Superseded original proposal: [`11_user_notifications.md`](../11_user_notifications.md).
