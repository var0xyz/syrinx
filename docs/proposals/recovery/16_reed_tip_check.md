# Recovery 16 — Reed tip check (history-fork safeguard)

## Status

Proposed.

## Depends on

None strictly. Complements [17](17_device_binding.md) but does **not**
replace it: device binding is a session/UX gate; this proposal is an
**application-level publish gate** that rejects a create when the client’s
idea of the author’s tip is stale — so dual-device (or dual-tab) posting
cannot fork history even if both clients still hold valid keys.

Land whenever convenient relative to 17 — the two are orthogonal. Prefer
shipping this **before** or **with** multi-device import flows becoming
common, since import without either safeguard is how forks appear.

## Context

Backups move identity material between browsers. Until something prevents it,
two origins can hold the same keys and both successfully `POST /reeds`. Each
publish is independently countersigned; the server has no notion of “tip,” so
the author’s timeline can **fork**.

[17](17_device_binding.md) rejects non-active devices early. That is valuable
UX (logout, WS kick, confirm-on-import) but it is policy state. History
linearity should not depend on it alone.

A DB-level linked list (`previous_id` UNIQUE + self-FK) would also prevent
forks, but **hard deletes** fight that model (RESTRICT / soft-delete /
tombstones). We do not want soft-delete complexity for this. Instead the
client names the reed it believes is current tip; the server accepts the
create only if that id is actually the author’s newest reed (or the author
has none, for genesis).

## Scope

### Publish gate (`POST /reeds`)

Client sends `previousID` (form field alongside `reedID` / `signature`):

- **Omitted / empty** — allowed only when the author currently has **zero**
  reeds.
- **Set** — must be an existing reed **owned by the author**, and must equal
  the author’s **current tip**.

**Tip definition:** newest row for `user_id` by `signed_at DESC`, with
`id DESC` as tie-break (server timestamps are truncated to seconds today, so
ties are possible under concurrency).

On mismatch (unknown id, other user’s reed, or not the tip) → reject with a
**distinct** client error (e.g. `{ "error": "reed_fork" }` / **409**), not a
generic 500. Client refreshes tip (list or realtime) before retrying.

No new `reeds` column. No unique self-FK. Hard delete stays as today: after
delete, tip is simply recomputed from remaining rows.

### Concurrency (required)

A naive check-then-insert races: two requests can both observe tip `A` and
both insert. The tip check and insert **must share one transaction**, and
that transaction must serialize creates for the author, e.g.:

1. `SELECT id FROM users WHERE id = $author FOR UPDATE` (or a per-user
   advisory transaction lock), then
2. Resolve current tip (`ORDER BY signed_at DESC, id DESC LIMIT 1`),
3. Compare to client `previousID`,
4. `INSERT` the new reed (and allocation) in the same transaction.

Without the lock, the gate is advisory only under dual-tab / dual-device
races — the failure mode this proposal exists to stop.

### Client

- Tip = author’s latest **successfully countersigned** local reed id (or
  empty if none).
- On `reed_fork`, refresh tip from server / peer traffic, then re-queue;
  do not keep hammering with a stale `previousID`.
- No need to persist a chain link for recovery beyond what the client
  already stores for tip selection.

### Recovery

**No change** for v1. Recovery re-reports historical reed metadata; it does
not append under the live tip gate. Ordering remains “whatever the client
holds,” as in [06](06_reeds_follows_complete.md). Live fork prevention does
not require reconstructing a linked list at restore time.

### Relationship to device binding (17)

| Concern | 17 device binding | 16 tip check |
|---------|-------------------|--------------|
| Dual login UX | Yes — mismatch logout, WS kick | No |
| History fork under concurrent publish | Soft / racey | Hard — if transactional |
| Dual-tab same device | Not addressed | Covered |
| Delete / tombstone complexity | N/A | None — tip recomputed |
| Schema change | `user_devices` | None on `reeds` |

Ship both when possible. This proposal alone is enough to keep live history
linear; 17 alone is not.

## Non-goals

- Persisting `previous_id` / unique self-FK / soft-delete tombstones.
- Binding `previousID` into the countersignature (not required while tip is
  only a live publish gate; revisit if we later want attested chain links).
- Changing recovery reed re-submit shape.
- Multi-device concurrent authors with merged timelines.
- Replacing device binding UX.
- A `tip_reed_id` column on `users` (derivable when needed).

## Design

The fork we care about is two creates that both believe they extend the same
tip. Asking the client to name that tip and rejecting when it is stale turns
“who posted first” into a single transactional winner. Deletes stay simple
because tip is “newest remaining row,” not “successor in a preserved chain.”

Device ids never enter the reed path. The gate is author-scoped metadata
only.

## Resolved

1. **Mechanism** — application tip check on `POST /reeds` via client
   `previousID`; no `reeds.previous_id` column.
2. **Tip** — newest by `signed_at`, then `id`; empty `previousID` only when
   zero reeds.
3. **Deletes** — unchanged hard delete; tip recomputed.
4. **Concurrency** — tip check + insert in one transaction with a per-author
   lock.
5. **Not device-id-based** — orthogonal complement to 17.
6. **Recovery** — unchanged for this cut.
7. **Fork conflict** — distinct error; client refreshes tip.

## Open questions

1. **Conflict UX** — on `reed_fork`, only refresh tip + retry unsigned queue,
   or also treat it like a stale-device signal (coordinate with 17)?
2. **Form field name** — `previousID` vs `lastReedID` (same semantics; pick
   one for the API).

## Test plan

- [ ] First reed with empty `previousID` succeeds; second create with empty
      `previousID` while a reed exists → `reed_fork`
- [ ] Create with `previousID` = current tip succeeds; create naming the old
      tip after another publish → `reed_fork`
- [ ] `previousID` unknown → `reed_fork` / 400
- [ ] `previousID` owned by another user → reject
- [ ] After deleting the tip, create naming the new tip (former second-newest)
      succeeds; create naming the deleted id fails
- [ ] Concurrent dual-tab / dual-device publish with same tip → exactly one
      201, one fork error (lock holds)
- [ ] SPA sends local tip on create; on fork refreshes before retry
