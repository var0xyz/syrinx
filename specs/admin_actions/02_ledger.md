# Admin actions 02 — Public ledger

## Status

Proposed.

## Depends on

[01](01_requests_and_approvals.md)

## Context

Authority is paid for with accountability: every admin action is public to
every signed-in local member, with its reason. The only admin action left
out is a private message ([09](09_broadcasts.md)).

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
| `message`       | Human-readable text built by the server                                          |
| `signedAt`      | Server timestamp                                                                 |

```go
func buildLedgerEntryPayload(id, prevID uint64, prevSignature, userID, message,
    serverID, serverKeyFingerprint string, signedAt time.Time) []byte
// {"id","message","prevID","prevSignature","serverID","serverKeyFingerprint","signedAt","type":"ledger-entry","userID"}
```

In `identity.go`, mirrored in `signing.ts`.

```sql
CREATE TABLE IF NOT EXISTS ledger_entries (
  id                  BIGSERIAL PRIMARY KEY,
  prev_id             BIGINT UNIQUE REFERENCES ledger_entries(id),
  user_id             VARCHAR(255) REFERENCES identities(id),
  message             TEXT NOT NULL,
  signed_at           TIMESTAMP NOT NULL,
  server_signature_id INT NOT NULL REFERENCES server_signatures(id)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_ledger_entries_first
  ON ledger_entries((prev_id IS NULL)) WHERE prev_id IS NULL;
```

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

| Event                             | Message                                                       |
| --------------------------------- | ------------------------------------------------------------- |
| Request opened                    | `Request {requestID}: {action} {targets} for: {reason}`       |
| Broadcast opened                  | `Request {requestID}: broadcast "{message}"`                  |
| Request approved                  | `Approved request {requestID}`                                |
| Request rejected                  | `Rejected request {requestID}`                                |
| Request withdrawn                 | `Withdrew request {requestID}`                                |
| Request moot                      | `Closed request {requestID}: every target left`               |
| Mesh invite created               | `Server invite {invitationID} created for {name}`             |
| Mesh invite canceled              | `Canceled server invite {invitationID}`                       |
| Mesh connection approved          | `Approved connection to {serverID}`                           |
| Mesh connection rejected          | `Rejected connection to {serverID}`                           |
| Mesh disconnection                | `Disconnected from {serverID}`                                |
| Reserved username added / removed | `Username {name} reserved` / `Username {name} released`       |

Requests cover every two-admin action: kick, suspend, reinstate, promote,
demote, key rebind (an admin adding a key to an account) and broadcast. A
broadcast's opening entry carries its message; a rebind's carries the new
key ID. `{targets}` lists usernames separated by commas, without `@`:
usernames can contain spaces, so the SPA shows each name in its own color.

A mesh connection is logged when **this** server's admin approves it.
Whether the peer has approved on their side doesn't matter.

**Not logged:** private messages, at either step, and flags (a flag is a
member's action, not an admin's).

## WebSocket

When an entry is written, the server sends `LEDGER_ENTRY` (the entry plus
its server signature) to every connected local member. The client checks
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

A new member rebuilds the ledger right after signup: it verifies `head`,
then each `tail` entry against the one before it, following `after` with
the last entry's ID until the tail is empty. From then on it keeps the
ledger current from the WebSocket. An account recovery does the same, and
a client that was offline asks only for the tail after the last entry it
holds.

## Verification (SPA)

`lib/verifiers/ledgerEntry.ts` verifies the server signature over
`buildLedgerEntryPayload`, against the server key selected by fingerprint,
before an entry is stored. An entry that fails is dropped.

The client rebuilds each payload with the previous entry's signature from
its own copy, so an entry only verifies when the client holds every entry
before it. Its copy is always a complete chain back to the first entry. A
backfill page whose entries don't chain is refused as a whole.

When the API returns an entry the client already holds, the client compares
it with its local copy. A different message or timestamp for the same ID,
or an entry the client holds that the server no longer returns, means the
server rewrote its ledger. The client keeps its own copy and warns the
member.

## SPA

`routes/ledger/+page.svelte`, linked for every member just below the
version info: at the bottom of the side nav on large screens, and on the
account page on small ones:

- One row per entry: the message, then a footer with `#id`, the admin (or
  "Server") and the relative time. Newest first.
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
- An unauthenticated read gets 401.
- `LEDGER_ENTRY` reaches every connected local member.
- SPA: a tampered message fails verification; a server copy that differs
  from the local one raises the warning.
- Payload golden bytes, Go and SPA.
