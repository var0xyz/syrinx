# Bans 05 — Unban snapshot

## Status

Proposed.

## Depends on

[03](03_notices.md), [04](04_enforcement_b.md)

## Context

During the ban, Bob's replies to and echoes of A's reeds, and his mentions of
A's members, existed on B but never on A. When the ban is lifted, B sends A
everything it has, not a diff, and A brings its records of Bob in line. Being a
full snapshot, it is idempotent: a lost or repeated one is sent again, with no
state kept on either side.

## Leg

`POST /api/federation/relay/ban-snapshot`, B → A, sent after B records a lift:

```proto
message RelayBanSnapshot {
  string user_id = 1;
  uint32 as_of = 2;                       // when B read Bob's reeds
  repeated RelayNewReedPayload reeds = 3; // Bob's live reeds that reply to or
                                          // echo A's reeds, or mention A
}
```

`reeds` holds only the references `relay/new-reed` carries, so the body stays
small. B records what it owes in `peer_ban_snapshot_notices`:

```sql
-- An unban snapshot each banning peer is owed.
CREATE TABLE IF NOT EXISTS peer_ban_snapshot_notices (
  server_id VARCHAR(16) NOT NULL REFERENCES server_mesh(server_id) ON DELETE CASCADE,
  user_id   VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,

  PRIMARY KEY (server_id, user_id)
);
```

- Inserted in the transaction that records the lift.
- Deleted on a 2xx, and when a new ban of A's for Bob arrives, since A would
  refuse the snapshot with 409.
- Retried until A answers 2xx. Nothing about the snapshot is stored: B reads
  Bob's reeds when it sends it.

## Reconciling (A)

- `user_id` must belong to the calling peer and not be banned by A: a snapshot
  for a user banned again is refused with 409.
- In one transaction, for Bob:
  - upsert every reply, echo and mention row in `reeds`;
  - delete his reply, echo and mention rows on A's reeds that aren't in `reeds`
    and were signed at or before `as_of`, so a reed delivered by the stream
    after B took the snapshot is kept.
- Counts follow from the row triggers.
- No live dispatch: no mention, reply or echo notification for anything the
  snapshot adds.

A takes the references on B's word, as it does for every `relay/new-reed`.

## Tests

- After an unban, Bob's reply, echo and mention from the ban period are on A,
  counted, and nobody was notified.
- A reply Bob deleted during the ban is not added back.
- A row in A that isn't in the snapshot is deleted; one newer than `as_of`
  stays.
- The same snapshot twice leaves the same state.
- A snapshot for a user A banned again is refused with 409.
