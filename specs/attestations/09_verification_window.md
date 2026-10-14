# Attestations 09 — Verification window

## Status

Implemented.

## Depends on

[03](03_api.md), [06](06_spa_verify_flow.md), [08](08_federation.md)

## Context

A vouch says two people compared fingerprints in person. Nothing stopped a
user from sending their verification link to strangers, who could then vouch
for them without ever meeting. The window ties a vouch to the moment the
subject is showing their code.

## Model

- Showing the verification code opens a **window** on the subject's home
  server, named by a UUID the client mints.
- The client pings it every 10 seconds while the code is showing. A window
  last pinged over a minute ago counts as **closed**, and a ping can't revive
  it: the server answers 404 and the client opens a new one.
- Opening a window first closes every other window the user has, so at most
  one is open.
- Closing the code closes the window. So does going offline: the table is
  `UNLOGGED` and cascades off `online_users`. A user who isn't online can't
  open one (409).
- A vouch is accepted only while the subject has an open window.

## Schema

```sql
CREATE UNLOGGED TABLE IF NOT EXISTS verification_windows (
  id UUID PRIMARY KEY,
  user_id VARCHAR(255) NOT NULL REFERENCES online_users(user_id) ON DELETE CASCADE,
  last_ping_at TIMESTAMP NOT NULL
);
```

## API

All plain HTTP, no bodies; the subject's client calls its own server.

| Route | Does |
|-------|------|
| `POST /api/verification-windows/{id}` | Close the caller's windows, open `id`. 409 when offline |
| `POST /api/verification-windows/{id}/ping` | Keep `id` open. 404 once it's closed, replaced or expired |
| `DELETE /api/verification-windows/{id}` | Close `id` |

## Checking a vouch

The window lives on the **subject's** server, so that server checks it:

- **Same server:** `POST /vouches` answers **412** when the subject has no
  open window.
- **Across servers:** the voucher's server relays the vouch to the subject's
  server, which answers **412** on `vouch-reference` when the window is
  closed. The voucher's server passes the 412 to its client. 412 rather than
  403, which already means a refusal block on relay legs.

A vouch queued offline and refused with 412 is dropped from the queue: the
window it needed won't reopen.

## Tests

- Opening needs the user online; a second window replaces the first; a ping a
  minute late finds the window expired; closing and going offline both close
  it.
- A relayed vouch for a subject with no open window is refused; the voucher's
  server reports a 412 from the subject's server as a closed window.
