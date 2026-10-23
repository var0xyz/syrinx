# Admin actions 12 — Admin records on admins' devices

## Status

Implemented.

## Depends on

[01](01_requests_and_approvals.md), [08](08_flags.md),
[recovery](../recovery/README.md)

## Context

A rebuilt server gets back what devices hold. Today an admin's device holds
only closed requests, and only once the admin has opened the requests page;
a reporter's device holds their own flags. So a recovery loses pending
requests, flag dismissals, and every flag raised by another server's members
(they report to their own server, never to us).

Every admin and root now keeps a copy of every admin record they can see:
requests in any state, flags, and flag dismissals. Each is immutable once
signed, except a request, whose closed copy replaces its pending one. The
server pushes each one as it changes; a device that missed some catches up
on every sync. All of them go back to a rebuilt server like any other
signed record.

## Records

| Record | Signed by | Stored on the device as |
|--------|-----------|-------------------------|
| Request, any state | requester, decider (if any), server on opening and closing | `adminRequests`, keyed by ID; a closed copy replaces a pending one, never the reverse |
| Flag | reporter, the reed's server | `flaggedReeds`, keyed by reed and reporter; every member keeps their own there too |
| Dismissal | the dismissing admin, the server | `flagDismissals`, keyed by ID, naming the reporters whose flags it closed |

`flagOpenCases` is derived, never signed or reported: one row per reed with
flags no dismissal names, by its latest such flag, so the Pending list pages
newest first without walking dismissed flags. The Dismissed list pages
`flagDismissals` by time, each with the flags it names. A flag raised on a
reed after a dismissal opens a new case; it never joins the dismissed one.

Visibility is the API's: an admin never gets flags or dismissals on their
own reeds, and requests for actions their permissions hide only when they
opened them.

## Signed dismissals

A dismissal gets a client-minted UUIDv7 ID, like a request, and the admin
signs it:

```go
buildFlagDismissalPayload(dismissalID, reedID, reason, keyID string, reporters []string) []byte
// {"dismissalID","keyID","reason","reedID","reporters","type":"flag-dismissal"}

buildFlagDismissalServerPayload(dismissalID, reedID, serverKeyFingerprint, userSignature string, signedAt time.Time) []byte
// {"dismissalID","reedID","serverKeyFingerprint","signedAt","type":"flag-dismissal","userSignature"}
```

`reporters` is sorted and names the open flags the admin saw; reporter and
reed identify a flag, since a member flags a reed once. The server
closes exactly those, and refuses the dismissal with `409
{"error":"flags-changed"}` if any of them isn't open, so a flag raised while
the admin was reading stays open. The countersignature's time is the
dismissal's time.

```sql
CREATE TABLE IF NOT EXISTS flag_dismissals (
  id           VARCHAR(255) PRIMARY KEY,  -- client-minted UUIDv7
  reed_id      VARCHAR(255) NOT NULL REFERENCES reed_identities(id) ON DELETE CASCADE,
  author_id    VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
  dismissed_by VARCHAR(255) REFERENCES identities(id) ON DELETE SET NULL,
  reason       TEXT NOT NULL,
  public_key_id       VARCHAR(255) NOT NULL REFERENCES public_keys(id),
  user_signature_id   INT NOT NULL REFERENCES user_signatures(id),
  server_signature_id INT NOT NULL REFERENCES server_signatures(id),
  dismissed_at TIMESTAMP NOT NULL  -- when the server countersigned it
);
-- The signed list, kept even once a removed reporter's flag is gone.
CREATE TABLE IF NOT EXISTS flag_dismissal_reporters (
  dismissal_id VARCHAR(255) NOT NULL REFERENCES flag_dismissals(id) ON DELETE CASCADE,
  reporter_id  VARCHAR(255) NOT NULL,
  PRIMARY KEY (dismissal_id, reporter_id)
);
```

## Removed reporters

An account removal, the user's own or a kick, deletes their flags, and
every dismissal left with none of its flags. A dismissal keeping others
stays, with its signed list. Every connected peer is told of a removal, and
on each server every local user who follows the removed account or holds
one of its keys: verifying a flag fetched the reporter's key, so every
admin who kept it hears, including of a reporter with no reeds. The device
drops that reporter's flags with the rest of the account.

## WebSocket

`ADMIN_RECORD` carries one record: a request, a flag or a dismissal. The
server sends it to every connected admin and root who may see it, whenever
a request opens or closes, a flag is taken, or a dismissal is made. Nothing
is queued for an admin who is offline: their next sync catches up.

## Catch-up and backfill

On every sync the SPA asks for the viewer's permissions. For an admin or
root it then:

1. lists pending requests and stores them;
2. pages closed requests back until a page holds nothing new;
3. lists every page of open flags and stores them;
4. pages dismissals back until a page holds nothing new, storing each with
   its flags.

It then settles `flagOpenCases` for every reed it touched.

A newly promoted admin has nothing stored, so the same pass backfills
everything. A device whose user is no longer an admin or root deletes all
admin stores but for their own flags. A promotion or demotion naming the viewer in a pushed ledger
entry runs the pass at once.

## Recovery

Each store reports its records like any other device store.

- **Pending request:** the requester's signature and the opening
  countersignature are checked, and it is restored pending. A closed copy
  reported later closes it, with its effects; a pending copy never reopens
  a closed one.
- **Acting on it:** while `RECOVERY_MODE` isn't `off`, every decision on a
  request opened before `first_boot_at` is refused with `409
  {"error":"opened-before-recovery"}`, and the server doesn't close one as
  moot. A device may still hold a closed copy. Once recovery mode is `off`,
  such a request is acted on normally.
- **Flag:** restored when countersigned by this server, whoever reports it,
  unless its reporter's account was removed. A reporter of another server
  must have a key held here; the record waits until one is, as a foreign
  follow does. A dismissal already restored that names it closes it.
- **Dismissal:** the admin's signature and the countersignature are checked,
  and it closes whichever of its flags are held.

## Tests

- Opening, closing, flagging and dismissing each push one `ADMIN_RECORD` to
  every connected admin who may see it, and none to members or to the
  author of the flagged reed.
- A dismissal naming a flag that isn't open is refused; one raised after
  the admin listed stays open.
- Recovery restores a pending request; a closed copy reported after it
  closes it; a pending copy after a closed one changes nothing.
- In recovery mode a decision on a request opened before `first_boot_at` is
  refused; with recovery off it goes through.
- A remote reporter's flag waits for their key, then is restored.
- A removed reporter's flags are deleted, and a dismissal left with none of
  its flags with them; one keeping others stays with its signed list.
- A removal reaches a user who only holds the removed account's key, until
  their device acks it.
- Payload parity: Go goldens and `test:admin-payload`.
