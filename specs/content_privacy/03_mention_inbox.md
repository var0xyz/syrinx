# Content privacy 03 — Mentioned-user pull inbox

## Status

Server implemented. Client-side consumption (poll + fetch + verify) not
yet wired up.

## Depends on

[01](01_signreed_contentless.md)

## Context

The server can't extract mentions from content it never sees, so mention
delivery becomes trust-but-verify, like tags — except a mention's target
is a single specific user, so a durable inbox row is the natural shape
rather than routing at fanout time. See [00](00_design.md) for why pure
receiving-client self-detection was rejected (it would silently drop the
"mention alone can trigger delivery to a non-follower" guarantee).

## Storage

`reed_mentions` (already existed, pre-dating this migration, previously
used only to drive `MentionTargetValid`'s existence check): one row per
(reed, mentioned user), gained a `created_at` column as the fetch cursor.
No author column — a reed's author is parsed from the canonical reed id
itself (`identity.AuthorOf`), no join needed, and this stays correct even
for a foreign mentioning reed this server has no `reeds` row for.

## Endpoints

- `GET /mentions` — the caller's own inbox, oldest first, `limit`
  (default 50, max 100) + `before` (RFC3339 cursor) query params, same
  convention as `GetReedChorus`/`ListReplies`. Cursor is a floor
  `(created_at, reed_id) > (cursor, '')`, not strict exclusion — a row at
  exactly the cursor timestamp can be re-delivered, matching the existing
  cursor convention used elsewhere in this codebase.
- `DELETE /mentions/{reedID}` — the caller reports a claimed mention of
  them isn't really present; removes only their own entry (row key is
  `reedID` + caller id, so this can never remove another user's entry for
  the same reed). Optional `?reason=` query param threads into the metric.
  Does not touch the reed itself.

## Client verification (per entry)

For each inbox entry not already held locally, fetch the reed through the
existing `REQUEST_REED`/relay machinery — no new delivery path. Once
decrypted, re-run mention extraction against the real content and confirm
the caller's own token is present. If not, call `DELETE
/mentions/{reedID}` with a reason; this both cleans up the inbox and
records a `MentionClaimRejected` metric carrying **both** the author's id
and the reporting (mentioned) user's id — a rejection attributable to a
specific author, not just an anonymous content-rejected count.

## Not yet done

The SPA has no polling/consumption loop for `GET /mentions` yet — no UI,
per the plan's stated scope ("purely backend solution atm"). The
mention-claim verification function (`verifyClaimedTags`'s sibling for
mentions) and the `DELETE /mentions/{reedID}` client call are not yet
written. Track separately before this feature is user-visible.

## Acceptance (server-side, met)

- `POST /reeds` with a `mentions` claim inserts `reed_mentions` rows.
- `GET /mentions` returns only the authenticated caller's own entries.
- `DELETE /mentions/{reedID}` only ever removes the caller's own row and
  emits a two-party metric.
