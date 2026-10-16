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

One entry per admin action, written in the same transaction as the action:

| Field           | Meaning                                                                               |
| --------------- | ------------------------------------------------------------------------------------- |
| `id`            | The entry's ID                                                                        |
| `prevID`        | The ID of the entry before it; absent on the first entry                              |
| `prevSignature` | The server signature of the entry before it; absent on the first entry                |
| `kind`          | `request`, `admin-invite`, `mesh` or `server-key`                                    |
| `userID`        | Who acted: a request's approver, an admin invite's claimer, or the admin otherwise; absent on `server-key` |
| `requesterID`   | Who opened the request, or created the admin invite; absent otherwise                 |
| `requestedAt`   | When that was, from the server's countersignature then; absent otherwise              |
| `message`       | Human-readable text built by the server, never containing a username                  |
| `targets`       | The user IDs the entry is about, sorted                                               |
| `content`       | A broadcast's text, or the permission set an entry grants                             |
| `signedAt`      | Server timestamp; for a request, when it was approved                                 |

```go
func buildLedgerEntryPayload(id, prevID uint64, prevSignature, kind, userID, requesterID string,
    requestedAt time.Time, message, content string, targets []string, serverID,
    serverKeyFingerprint string, signedAt time.Time) []byte
// {"content","id","kind","message","prevID","prevSignature","requestedAt","requesterID","serverID","serverKeyFingerprint","signedAt","targets","type":"ledger-entry","userID"}
```

In `identity.go`, mirrored in `signing.ts`.

```sql
CREATE TABLE IF NOT EXISTS ledger_entries (
  id                  BIGSERIAL PRIMARY KEY,
  prev_id             BIGINT UNIQUE REFERENCES ledger_entries(id),
  kind                VARCHAR(16) NOT NULL
    CHECK (kind IN ('request', 'admin-invite', 'mesh', 'server-key')),
  user_id             VARCHAR(255) REFERENCES identities(id),
  requester_id        VARCHAR(255) REFERENCES identities(id),
  requested_at        TIMESTAMP,
  message             TEXT NOT NULL,
  content             TEXT,
  signed_at           TIMESTAMP NOT NULL,
  server_signature_id INT NOT NULL REFERENCES server_signatures(id),
  CONSTRAINT ledger_entries_requested CHECK ((requester_id IS NULL) = (requested_at IS NULL)),
  CONSTRAINT ledger_entries_user CHECK ((user_id IS NULL) = (kind = 'server-key'))
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
and what the entry records is its `kind`, so no client ever parses
`message`.

IDs say nothing about order or count: the chain is `prev_id`. It is unique
and only one entry may have none, so the ledger is a single chain. The
writer reads the last entry under a lock in the same transaction. Under
the same lock it re-reads `servers.signing_key` and refuses to sign with
any other key, so nothing is appended with a key a rotation retired.

Every entry signs the previous entry's signature, so changing or removing
any entry breaks every entry after it, and the server would have to re-sign
all of them, which members already hold. `prevSignature` isn't stored: the
writer reads it from the previous row, and a verifier from the previous
entry it holds or fetches.

## What gets an entry

A two-admin request produces **one** entry, when it is approved: the
approver is `userID`, the requester `requesterID`, and `requestedAt` /
`signedAt` say when it was proposed and approved. A pending, rejected,
withdrawn or moot request writes nothing, so members never learn of an
action that didn't happen: a kick that is still pending, say, could push
its target to misbehave. Only admins see those, in the requests list.

| Event                             | Message                                                 | `targets`         | `content`           |
| --------------------------------- | ------------------------------------------------------- | ----------------- | ------------------- |
| Request approved                  | `[{requestID}] {Action}. Reason: {reason}`              | The targets       |                     |
| Rebind approved                   | `[{requestID}] Key rebind to {keyID}. Reason: {reason}` | The target        |                     |
| Promote or permissions approved   | `[{requestID}] {Action}. Reason: {reason}`              | The target        | The permission set  |
| Broadcast approved                | `[{requestID}] Broadcast`                               |                   | The message         |
| Admin invite claimed              | `[{inviteID}] Admin invite claimed`                     |                   | The permission set  |
| Mesh invite created               | `Server invite {invitationID} created for {name}`       |                   |                     |
| Mesh invite canceled              | `Canceled server invite {invitationID}`                 |                   |                     |
| Mesh connection approved          | `Approved connection to {serverID}`                     |                   |                     |
| Mesh connection rejected          | `Rejected connection to {serverID}`                     |                   |                     |
| Mesh disconnection                | `Disconnected from {serverID}`                          |                   |                     |
| Server key rotated                | `Server key {oldKeyID} rotated to {newKeyID}[ (compromised)][. Reason: {reason}]` | |                     |

`{Action}` is the action's display name: Suspension, Reinstatement, Kick,
Promotion, Demotion, Permission change. Requests cover every two-admin action: kick,
suspend, reinstate, promote, demote, permission changes, key rebinds (an
admin adding a key to an account) and broadcasts.

An admin invite needs no approval, but it creates an admin, so it is
logged like a request, once claimed: `requesterID` and `requestedAt` are
the inviter and the invite's server countersignature time, `userID` and
`signedAt` the new admin and the claim. An unclaimed invite never appears,
and a claimed one's ID is spent, so it is shown in full. The claim is
refused if the inviter no longer holds `admin.create` and every permission
the invite grants.

Every change to who is an admin and what they may do is in the ledger:
promotions, permission changes, demotions and admin invites. Replaying it
from the head gives every admin's permissions at any entry, so a client
can check that the requester and approver of every executed request held
its permission ([01](01_requests_and_approvals.md)). Root holds every
permission from the start.

A server key rotation is logged by `ops rotate-key`, in the rotation's own
transaction, signed by the new key and chained to the last entry signed by
the old one ([server key rotation 01](../server_key_rotation/01_ops_commands.md)).
It has no `userID`: the operator who ran the command isn't a signer, and
the ledger doesn't attribute an action to someone who didn't sign it. The
CLI can't push over the WebSocket; members get the entry on their next
catch-up.

A mesh connection is logged when **this** server's admin approves it.
Whether the peer has approved on their side doesn't matter.

**Not logged:** requests that aren't approved, private messages, and flags
(a flag is a member's action, not an admin's).

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
it with its local copy. A different message, content, targets, admin or
timestamp for the same ID,
or an entry the client holds that the server no longer returns, means the
server rewrote its ledger. The client keeps its own copy and warns the
member.

## SPA

`routes/ledger/+page.svelte`, linked for every member just below the
version info: at the bottom of the side nav on large screens, and on the
account page on small ones:

- One row per entry: `#id`, the message, the targets as usernames in their
  own color, the content (a broadcast's text, or the permissions granted),
  then "Proposed by {requester} {time}" and "Approved by {approver} {time}"
  on two lines ("Created" and "Claimed" for an admin invite), or the admin
  and the relative time for the other kinds. A server key rotation stands
  out with an accent and a "Server key rotated" badge, credited to "Server
  operator". Newest first.
- Rendered from the local `ledger` store (offline-first).
- A warning banner when the local copy and the server disagree.

## Tests

- Approving a request writes one entry, with the reason, both admins, and
  the open countersignature's timestamp as `requestedAt`; opening one writes
  none.
- A rejected, withdrawn or moot request, a private message and a flag write
  no entry.
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
- Creating an admin invite writes no entry; claiming it writes one, with
  the inviter, the new admin, the full invite ID, both timestamps and the
  permission set. A claim after the inviter lost a granted permission is
  refused and writes nothing.
- No message contains a username; targets are signed as user IDs.
- `LEDGER_ENTRY` reaches every connected local member.
- SPA: a tampered message fails verification; a server copy that differs
  from the local one raises the warning.
- Payload golden bytes, Go and SPA.
