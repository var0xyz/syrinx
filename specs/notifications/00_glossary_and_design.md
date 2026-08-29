# Notifications 00 — Glossary + design + locked model

## Status

Proposed.

## Depends on

—

## Context

[Prerequisite 11](../11_user_notifications.md) proposed a per-user
notification store, motivated primarily by telling a username-collision
"loser" they'd been renamed during server recovery. That motivation no
longer exists: `recovery/upsert.go`'s collision handling deletes the
losing account outright (commit `cc735d7`), and the code's own comment
explains why a rename could never have worked — *"a renamed-in-place row
would carry a username that no longer matches what its owner signed,
permanently breaking verification instead."* The server cannot mint a new
signed profile for a user; only the user's own key can do that, and
clients reject anything else.

Separately, this project has real, distinct needs for server→user
communication that proposal 11 never fully addressed:

- Server-side code often needs to tell a *specific* user something more
  detailed than a short HTTP error can carry — e.g. a background job
  processing one of their reeds fails and there's a real explanation worth
  surfacing, not just a generic 4xx/5xx.
- Admins need to reach the whole userbase for announcements — and
  sometimes want the audience to be able to discuss it, which a private
  per-user store can't offer.

This proposal is the umbrella for **every** mechanism this app uses to
tell a user something — including the existing client-side toast/alert
system, which already exists but has never had its place in a shared
vocabulary alongside the other two.

## Scope

- Define **three** terms precisely (Alert, Mailbox message, Admin
  mention) — all three are "notifications" in the umbrella sense this
  spec uses the word — and lock which one applies to which use case.
- Design the **Mailbox message**: an encrypted, store-and-forward,
  one-way, per-user message system. Internal server helper as the primary
  producer; an `ops` CLI command as a secondary, manual producer.
- Design the **`@everyone` admin mention handle**: an admin-gated
  expansion of the existing mention system, delivered as a normal reed —
  no new storage or delivery mechanism.
- Document, but do **not** implement, a fix for the one real reliability
  gap this uncovers: nothing today notices or retries a failed
  `@everyone` broadcast attempt.

## Non-goals

- No code change to the existing Alert system (`Notifications.svelte` /
  `stores/notifications.ts`) — it already works; this spec documents its
  place in the taxonomy, not a rewrite.
- No reply-to-mailbox. If a reply matters, use an admin mention.
- No `@role` or `@group` generalization beyond the single `@everyone`
  case.
- No email/push/OS notifications — in-app only, same as the original
  proposal 11 scope.
- No **server-side** durable mailbox history or read/unread log — once
  delivered and ACKed, the server's copy is gone and the server never
  learns whether the client considers a message read. The client *does*
  keep a local `isRead` flag once stored (see
  [05](05_mailbox_message_spa_bell.md)) so the SPA can distinguish
  read/unread and offer per-message delete — that's purely local state
  that never round-trips to the server.
- No implementation of the broadcast-retry mechanism sketched below — it
  is a documented design for future work.
- No cross-server `@everyone` — see "Federation boundary" below.

## Design

### Alert (one of the three; already implemented, documented here)

`spa/src/lib/components/Notifications.svelte` +
`spa/src/lib/stores/notifications.ts`, wired into `+layout.svelte`. A
`writable<Notification[]>` store of `{ type: 'error'|'warning'|'info'|'success',
message, duration }` objects with auto-dismiss timers and pause/resume on
hover. Never touches the server — purely local, ephemeral, client-state
feedback. Ten files currently import it (`NewReedModal.svelte`,
`Notifications.svelte`, `QRCodeModal.svelte`, `stores/notifications.ts`,
`delete/confirm/+page.svelte`, `invites/+page.svelte`,
`mesh/+page.svelte`, `profile/+page.svelte`,
`reed/[userID]/[reedID]/+page.svelte`, `signup/+page.svelte`). This
proposal makes no code change here — it exists to give this mechanism a
name (Alert) consistent with the other two, so all three can be discussed
without ambiguity.

### Mailbox message

**Producers**, in order of expected usage:

1. **Internal server helper** — any handler or background job can call
   `SendMailboxMessage(ctx, userID, kind, payload)` directly. This is the
   primary use case: a request returns a short, generic HTTP error to the
   caller, and the fuller explanation (what actually went wrong, what the
   user might do about it) lands in their mailbox instead of being
   crammed into the HTTP response body or lost to a server log the user
   will never see.
2. **`ops mailbox-send <userID> <message>`** — a thin CLI wrapper over the
   same helper, for a human admin to send a one-off message manually.
   Follows the existing `ops.go` `switch os.Args[1]` command-dispatch
   shape (see `export-identity`, `import-identity`, `rotate-passphrase`).

**Storage**: the server must not be able to read pending mailbox content.
`SendMailboxMessage` marshals a typed payload to JSON, then encrypts it to
the recipient's *current* public key armor
(`crypto.Service.Encrypt(plaintext, publicKeyArmor)`) — the exact pattern
already used for the federation connection payload (admin invite is
encrypted to the remote server's public key before being handed back to
the inviting admin to relay out-of-band). "Current key" means
`GetActiveKeyFingerprint`/`users.user_fingerprint` — the same "who signs
for this user right now" concept used everywhere else in the app.

**Known v1 gap**: if the recipient rotates their key between send and
delivery, the pending message becomes undecryptable with the new key
(the server never re-encrypts pending mail on rotation). Accepted, not
solved — flagged here so it isn't rediscovered as a surprise later.

**Delivery**: WS push while the recipient is online, one-way, then ACK.
There is deliberately no separate `pending_*` bookkeeping table (unlike
reed removal's `pending_reed_events`) — the `user_mailbox` row itself
already *is* the undelivered-message record. Its mere existence means
"not yet delivered and acked"; the server deletes it once the client
confirms receipt. Catch-up for an offline recipient is just "query
remaining rows for this user on reconnect," no diffing needed since there
is no separate delivered-log to diff against.

**Client**: on receipt, decrypt with the user's own private key
(client-side only — the server never has it), store the plaintext locally
(new IndexedDB store, with a local `isRead: false` default and the
decrypted payload's optional `Link`), then ACK. A failed decrypt does
**not** ACK — the
server keeps the row and will redeliver it, and the client logs the
failure. This mirrors the fail-closed-and-report pattern already used for
revoked-key handling (see `specs/09_revocation_fanout.md`): never
silently drop something the client couldn't verify/decrypt; always leave
a trail.

### Admin mention (`@everyone`)

The existing mention system (`~<userID>@<serverID>`,
[conversations 04](../conversations/04_mentions.md)) resolves a token to
exactly one real, existing user — `mentions.go`'s regex has no way to
represent "everyone," and extending the regex to somehow match a
non-existent userID doesn't make sense. So `@everyone` is not a mention
*token* at all; it's a distinct, publish-time **expansion**:

1. Author publishes a reed whose content contains the literal string
   `@everyone`.
2. At `SignReed`/`CreateReed` time, after the existing per-token mention
   validation/insertion, check: does the content contain `@everyone`, and
   is `roles.IsAdmin(author.Role)` true?
3. If both, run one additional statement in the **same transaction**
   already wrapping the reed write and its mention inserts:
   `INSERT INTO reed_mentions (mentioning_user_id, mentioning_reed_id,
   mentioned_user_id, mentioned_server_id) SELECT $1, $2, id, $3 FROM users
   ON CONFLICT DO NOTHING`. One statement, not a per-user loop — this
   matters at scale (see "Broadcast reliability" below).
4. If the author is not an admin, `@everyone` in their content is inert —
   stored as plain text, not expanded, not specially validated. No error;
   it just doesn't do anything.

The reed itself is otherwise ordinary: repliable, echoable, deletable via
the normal deletion-cert flow, subject to the normal content-length
limits. "Admin mention" describes *how it was addressed*, not a different
kind of object.

**Delivery/read**: purely through the mentions tab (see
[02](02_mentions_tab.md)) — every recipient now has a `reed_mentions` row
for this reed, same as any other mention, so the existing (proposed) list
endpoint surfaces it with no special-casing.

### Federation boundary

Federation ([`federation/`](../federation/README.md)) is only step 01
landed today — there is no cross-server user resolution yet. `@everyone`
expands against `SELECT id FROM users` on *this* instance only, by
construction: there is nothing else to expand against locally for a
foreign `mentioned_server_id`, and there is no cross-server admin
authority even once peering exists — an admin's role only means anything
on their own instance. This should remain true even after federation
matures; if a future proposal wants cross-server broadcast, it needs its
own explicit design (out of scope here).

### Broadcast reliability — documented gap, not implemented

The `@everyone` expansion runs inside `CreateReed`'s existing transaction,
so a failure *during* that transaction cannot leave partial fanout —
Postgres rolls the whole write back atomically, same as any other
mid-request DB failure. That part is already safe by construction and
needs no new mechanism.

What's missing: if that transaction fails (or the server process dies
before the HTTP client retries the publish request), the `@everyone` reed
and its entire fanout simply never happened — and nothing durable records
that an admin *attempted* an `@everyone` broadcast, so nothing prompts a
retry. An admin could believe they'd broadcast something that no user
ever received, with no signal that it failed.

**Sketch for a future proposal** (explicitly not built here):

- A durable `admin_mentions(reed_id, started_at, completed_at)` table.
  Row inserted (with `started_at`) when an admin's `@everyone` reed
  publish is accepted, `completed_at` set once the fan-out transaction
  commits successfully.
- On boot (or some periodic sweep), scan for rows where `completed_at IS
  NULL` and `started_at` is older than some threshold — these are
  attempts that never finished. Re-run the same `@everyone` expansion for
  that `reed_id` (safe to retry: the `ON CONFLICT DO NOTHING` on
  `reed_mentions` makes it idempotent).
- This intentionally does **not** track per-recipient delivery/ack state
  — the earlier per-user notification-store design (proposal 11) would
  have required checking a delivery ledger on every user's login, which
  gets slower as the userbase and notification history grow. Tracking
  only "did the *job* complete," not "did *this user* get it," avoids
  that scaling problem entirely, at the cost of only detecting
  all-or-nothing job failure, not partial delivery to some subset of
  online recipients (which shouldn't happen anyway, since the write to
  `reed_mentions` is what "delivered" means here — the *read* side, i.e.
  the mentions tab, always reflects current DB state, not a push
  timeline).

## Cross-links

- Mention extraction/validation: [`mentions.go`](../../mentions.go),
  [conversations 04](../conversations/04_mentions.md).
- `reed_mentions` schema + `CreateReed` transaction:
  [`db.go`](../../db.go), [`services.go`](../../services.go).
- Role check: [`roles.IsAdmin`](../../roles/roles.go).
- Encrypt-to-recipient precedent: [`crypto.Service.Encrypt`](../../crypto/crypto.go)
  (`handlers.go` federation connection payload).
- Fail-closed pattern precedent: [`09_revocation_fanout.md`](../09_revocation_fanout.md).
- `ops` CLI command shape: [`ops.go`](../../ops.go).
