# Admin actions 02 — Public ledger

## Status

Implemented.

## Depends on

[01](01_requests_and_approvals.md)

## Context

Authority is paid for with accountability: every admin action is public to
every signed-in local member, with its reason. The only admin action left
out is a private message ([09](09_broadcasts.md)). A suspended member can't
read the ledger: like every other signed operation, their reads get the
`403 suspended` of [03](03_suspend_reinstate.md).

The ledger is an append-only list of **server-signed entries**. Every member
receives each new entry over the WebSocket and stores it locally. The server
can't quietly rewrite or drop an entry afterwards: every member holds a
signed copy and can compare it with what the server returns.

## Entry

One entry per admin action, written in the same transaction as the action.
Every entry has exactly one actor:

| Field           | Meaning                                                                          |
| --------------- | -------------------------------------------------------------------------------- |
| `id`            | The entry's ID                                                                   |
| `prevID`        | The ID of the entry before it; absent on the first entry                         |
| `prevSignature` | The server signature of the entry before it; absent on the first entry           |
| `userID`        | The admin who performed the action; absent when the server did (a moot request)  |
| `message`       | Human-readable text built by the server, never containing a username             |
| `targets`       | The user IDs the entry is about, sorted                                          |
| `content`       | A broadcast's text, or the permission set an entry grants                        |
| `signedAt`      | Server timestamp                                                                 |

```go
func buildLedgerEntryPayload(id, prevID uint64, prevSignature, userID, message,
    content string, targets []string, serverID, serverKeyFingerprint string,
    signedAt time.Time) []byte
// {"content","id","message","prevID","prevSignature","serverID","serverKeyFingerprint","signedAt","targets","type":"ledger-entry","userID"}
```

In `identity.go`, mirrored in `signing.ts`.

```sql
CREATE TABLE IF NOT EXISTS ledger_entries (
  id                  BIGSERIAL PRIMARY KEY,
  prev_id             BIGINT UNIQUE REFERENCES ledger_entries(id),
  user_id             VARCHAR(255) REFERENCES identities(id),
  message             TEXT NOT NULL,
  content             TEXT,
  signed_at           TIMESTAMP NOT NULL,
  server_signature_id INT NOT NULL REFERENCES server_signatures(id)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_ledger_entries_first
  ON ledger_entries((prev_id IS NULL)) WHERE prev_id IS NULL;

CREATE TABLE IF NOT EXISTS ledger_entry_targets (
  entry_id  BIGINT NOT NULL REFERENCES ledger_entries(id) ON DELETE CASCADE,
  user_id   VARCHAR(255) NOT NULL REFERENCES identities(id),
  PRIMARY KEY (entry_id, user_id)
);
```

Usernames change and can contain any character, so messages never carry
them. The users an entry is about are in `targets`, as user IDs, and the
SPA shows each one's current username in its own color. Data a client
needs to act on, a broadcast's text or a permission set, is in `content`,
so no client ever parses `message`.

IDs say nothing about order or count: the chain is `prev_id`. It is unique
and only one entry may have none, so the ledger is a single chain. The
writer reads the last entry under a lock in the same transaction.

Every entry signs the previous entry's signature, so changing or removing
any entry breaks every entry after it, and the server would have to re-sign
all of them, which members already hold. `prevSignature` isn't stored: the
writer reads it from the previous row, and a verifier from the previous
entry it holds or fetches.

## What gets an entry

A two-admin request produces **two** entries: one when it is opened, by the
requester, and one when it closes, by the approver (or by the requester on
a withdrawal, or by the server when it goes moot). The reason is only in
the first. The second names the request by its **request ID**, never by a
ledger entry ID.

| Event                             | Message                                                 | `targets`         | `content`           |
| --------------------------------- | ------------------------------------------------------- | ----------------- | ------------------- |
| Request opened                    | `[{requestID}] {Action}. Reason: {reason}`              | The targets       |                     |
| Rebind opened                     | `[{requestID}] Key rebind to {keyID}. Reason: {reason}` | The target        |                     |
| Promote or permissions opened     | `[{requestID}] {Action}. Reason: {reason}`              | The target        | The permission set  |
| Broadcast opened                  | `[{requestID}] Broadcast`                               |                   | The message         |
| Request approved                  | `[{requestID}] Approved`                                |                   |                     |
| Request rejected                  | `[{requestID}] Rejected`                                |                   |                     |
| Request withdrawn                 | `[{requestID}] Withdrawn`                               |                   |                     |
| Request moot                      | `[{requestID}] Closed. Reason: every target left`       |                   |                     |
| Admin invite created              | `Admin invite {inviteID} created`                       |                   | The permission set  |
| Admin invite claimed              | `Admin invite {inviteID} claimed`                       | The new admin     |                     |
| Mesh invite created               | `Server invite {invitationID} created for {name}`       |                   |                     |
| Mesh invite canceled              | `Canceled server invite {invitationID}`                 |                   |                     |
| Mesh connection approved          | `Approved connection to {serverID}`                     |                   |                     |
| Mesh connection rejected          | `Rejected connection to {serverID}`                     |                   |                     |
| Mesh disconnection                | `Disconnected from {serverID}`                          |                   |                     |
| Reserved username added / removed | `Username {name} reserved` / `Username {name} released` |                   |                     |

`{Action}` is the action's display name: Suspension, Reinstatement, Kick,
Promotion, Demotion, Permission change. Requests cover every two-admin action: kick,
suspend, reinstate, promote, demote, permission changes, key rebinds (an
admin adding a key to an account) and broadcasts.

An admin invite needs no approval, but it creates an admin, so it is
logged twice: when it is created, by its inviter, with the permissions it
grants, and when it is claimed, by the server, naming the new admin.

Every change to who is an admin and what they may do is in the ledger:
promotions, permission changes, demotions and admin invites. Replaying it
from the head gives every admin's permissions at any entry, so a client
can check that the requester and approver of every executed request held
its permission ([01](01_requests_and_approvals.md)). Root holds every
permission from the start.

A mesh connection is logged when **this** server's admin approves it.
Whether the peer has approved on their side doesn't matter.

**Not logged:** private messages, at either step, and flags (a flag is a
member's action, not an admin's).

## WebSocket

When an entry is written, the server sends `LEDGER_ENTRY` (the entry plus
its server signature) to every connected local member. Suspended members
aren't connected, so they don't receive it. The client checks
that `prevID` is the last entry it holds, verifies the signature with that
entry's signature, and stores the entry in its IndexedDB `ledger` store.

When `prevID` is an entry the client doesn't hold, because it was offline,
it backfills from the API, after the last entry it holds, before storing
anything newer.

## API

| Route                          | Auth                       | Returns                                                     |
| ------------------------------ | -------------------------- | ----------------------------------------------------------- |
| `GET /api/ledger`              | Any signed-in local member | `{head, tail}`: the first entry, then the entries after it in chain order, paginated |
| `GET /api/ledger?after={id}`   | Any signed-in local member | `{tail}`: the entries after that one in chain order, paginated |
| `GET /api/ledger/{id}`         | Any signed-in local member | One entry                                                   |

A client rebuilds the ledger on every catch-up (each `SYNC_REQUEST`,
including the first one after signup or an account recovery): it verifies
`head`, then each `tail` entry against the one before it, following `after`
with the last entry's ID until the tail is empty. Walking the whole chain
each time is what lets it compare the entries it already holds; the ledger
is small. Between catch-ups the WebSocket keeps it current.

## Verification (SPA)

`verifyLedgerRecord` (`lib/verifiers`) verifies the server signature over
`buildLedgerEntryPayload`, against the server key selected by fingerprint,
before an entry is stored. An entry that fails is dropped.

The client rebuilds each payload with the previous entry's signature from
its own copy, so an entry only verifies when the client holds every entry
before it. Its copy is always a complete chain back to the first entry. A
backfill page whose entries don't chain is refused as a whole.

When the API returns an entry the client already holds, the client compares
it with its local copy. A different message, content, targets or timestamp
for the same ID,
or an entry the client holds that the server no longer returns, means the
server rewrote its ledger. The client keeps its own copy and warns the
member.

## SPA

`routes/ledger/+page.svelte`, linked for every member just below the
version info: at the bottom of the side nav on large screens, and on the
account page on small ones:

- One row per entry: the message, the targets as usernames in their own
  color, the content (a broadcast's text, or the permissions granted), then
  a footer with `#id`, the admin (or "Server") and the relative time. Newest
  first.
- Rendered from the local `ledger` store (offline-first).
- A warning banner when the local copy and the server disagree.

## Tests

- Opening and closing a request each write one entry, the second chained
  to the first; only the opening entry carries the reason.
- A private message and a flag write no entry.
- A rolled-back executor writes no entry, and the next entry still chains
  to the last one written.
- Every entry's `prevID` and `prevSignature` are the previous entry's; a
  second first entry, or two entries with the same `prevID`, is refused.
- SPA: changing an earlier entry's message makes every later entry fail
  verification.
- SPA: an entry whose `prevID` isn't the last held entry triggers a
  backfill; a backfill page with a broken chain is refused.
- `GET /api/ledger` returns the entry with no `prevID` as `head`, and a
  `tail` where each entry's `prevID` is the entry before it.
- An unauthenticated read gets 401; a suspended member's read gets 403.
- Creating and claiming an admin invite each write one entry, the first
  with the permission set, the second with the new admin as its target.
- No message contains a username; targets are signed as user IDs.
- `LEDGER_ENTRY` reaches every connected local member.
- SPA: a tampered message fails verification; a server copy that differs
  from the local one raises the warning.
- Payload golden bytes, Go and SPA.
