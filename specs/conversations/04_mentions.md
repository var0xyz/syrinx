# Conversations 04 — Mentions (`@` → durable user refs)

## Status

Implemented.

## Depends on

[01](01_publish_and_refs.md) (publish already receives `content` for verify /
limits — same hook indexes mentions). Deletion cleanup mirrors echo-index
cleanup already in place for reed/account removal.

Independent of [02](02_index_and_api.md) / [03](03_spa_reed_detail.md)
except sharing the “index at countersign, never store body” pattern.

## Context

Authors want to address other users inside reed content. Mentions must:

1. Survive domain moves — signed content must **not** embed `https://this-host/...`.
2. Be clickable inside the SPA (and optionally OS/browser deep-linkable later).
3. Leave a durable **server-side index** so a future notification system
   ([proposal 11](../11_user_notifications.md)) can tell users they were
   mentioned — without re-reading reed bodies the server does not keep.

## Scope

- Composer `@` picker: search local users; insert a markdown link.
- Canonical mention link form (custom URI scheme — see below).
- SPA render/click: mention links navigate in-app to the user profile; do
  **not** treat them as external `https` links.
- At `SignReed`: parse mentions from `content`, validate, insert
  `reed_mentions` rows; discard content as today.
- On reed removal / account removal: delete matching mention rows.
- Minimal `GET` user-search endpoint for the picker (if none exists yet).

## Non-goals

- Delivering mention notifications (in-app store, badge, push, email). Out of
  scope; table is the producer substrate for [11](../11_user_notifications.md).
- Mentions of users on foreign instances beyond recording `server_id` in the
  href / index (no federation routing in v1).
- Editing already-published reeds to refresh display names.
- Autocomplete for reeds, hashtags, or arbitrary URLs.

## Design

> **Superseded:** the `web+syrinx://users/...` scheme below was the
> original mention design and is still used for hashtag/pipe links
> (`web+syrinx://pipe/...`, see `formatPipeHref` in `reedMarkdown.ts`), but
> mentions themselves moved to the `~userID@serverID` wire form — see "Wire
> form (superseded from the original markdown-link sketch)" further down.
> The reasoning here (why a custom scheme, why `web+syrinx`) still applies
> to pipe links; kept for that history and because it's still the
> in-app-navigation mechanism `MentionLink` reuses for the resolved
> `/profile/<userID>` link once a mention is resolved.

### URI scheme (standards-aligned)

**Yes — use a custom URI scheme.** That is the right tool when the referent is
an app resource whose HTTP origin may change. Prefer schemes the web platform
already knows how to handle.

| Candidate | Verdict |
|-----------|---------|
| `https://<current-domain>/...` | **Reject** — hardcodes a host into signed content. |
| `syrinx+web://...` (proposed sketch) | **Avoid** — not the HTML/PWA convention; `registerProtocolHandler` will not accept it. |
| `syrinx://...` | Fine for native apps; browsers will not let a web app register it via the standard API. |
| `web+syrinx:...` | **Prefer** — WHATWG HTML allows custom schemes of the form `web+` + lowercase ASCII for [`navigator.registerProtocolHandler`](https://developer.mozilla.org/en-US/docs/Web/API/Navigator/registerProtocolHandler) and PWA [`protocol_handlers`](https://developer.chrome.com/docs/web-platform/best-practices/url-protocol-handler). |

**Locked wire form** (markdown destination):

```text
[Display Name](web+syrinx://users/<serverID>/<userID>)
```

Notes:

- Hierarchical path `users/<serverID>/<userID>` mirrors reed refs’
  server dimension and stays domain-free.
- Link **text** is the username (or display name) at compose time — cosmetic
  only; identity is the href. Stale names after renames are acceptable.
- Visible character budget already counts link text and strips the URL
  (`CountMarkdownCharacters` / SPA twin) — no special case required.
- **Primary click path** is in-app: [`MarkdownParser`](../../spa/src/lib/components/MarkdownParser.svelte)
  already intercepts markdown links. Detect `web+syrinx:` and
  `goto(/profile/<userID>)` (or equivalent) instead of opening
  `ExternalLinkModal`. Domain never needs to appear in the reed.
- **Optional later:** register `web+syrinx` in the web app manifest /
  `registerProtocolHandler` so OS-level opens land on
  `/open?uri=%s` → parse → route. Not required for v1 SPA clicks.

Do **not** invent a second parallel format (`syrinx+web`, bare paths, etc.).
One scheme, one parser.

### Composer UX

1. User types `@` in the reed composer (or taps an `@` control).
2. Popover: typeahead against `GET /users/search?q=&limit=` (local users,
   exclude self optional, exclude account-removed).
3. On choose: insert
   `[username](web+syrinx://users/<localServerID>/<userID>)` at the cursor
   (replace the `@query` token).
4. Subsequent edits treat it as ordinary markdown; do not re-resolve unless
   the user deletes and re-@s.

### Wire form (superseded from the original markdown-link sketch)

Mentions are **not** markdown links. The composer inserts
`~<userID>@<serverID>` directly into content — no username, no link syntax,
so it's immune to spaces in usernames (Syrinx usernames may contain
spaces, which is what forced this away from the original
`[label](web+syrinx://users/...)` design). `userID`/`serverID` are
alphanumeric-only, **not fixed-length** (e.g. the root user's id is `"1"`;
foreign servers may mint IDs of any length) — parsed as a greedy
alphanumeric run, same idea as a `#hashtag`'s `\S+` run. See
`reedMarkdown.ts` `readMention()` / Go `mentions.go` `ExtractMentions`.

Rendering resolves `userID -> username` live (client-side lookup / fetch)
and links to `/profile/<userID>`; the raw ids never reach the UI except as
a last-resort fallback when resolution fails entirely (see Client render).

### Server: extract + index at publish

During `SignReed`, after content-limit checks and before/within the create
transaction (same pattern as echoes):

1. Scan `content` for the `~userID@serverID` mention form (see above).
2. Deduplicate by `(mentioned_server_id, mentioned_user_id)` per reed.
3. **Only local mentions are indexed.** For each mention whose `serverID`
   equals this instance: `MentionTargetValid(userID, serverID)` requires the
   user exists, is not account-removed, **and** `serverID` is a known row in
   `servers` (self or a federated peer) — else 400 `unknown_mentioned_user`.
   Mentions of a foreign `serverID` are silently **not stored** — `content`
   still carries the token either way, but no `reed_mentions` row is
   written for them.
   **Revised lock:** this reverses the original "accept into index, skip
   existence check" call — `mentioned_user_id` is now a hard FK to
   `users(id)`, which only foreign-mention exclusion makes possible. When
   cross-server mention notification is built, foreign mentions will need
   their own handling (a different table, or a relaxed constraint) rather
   than reusing this row shape as-is.
4. Insert rows; never store `content`.

Self-mentions: allowed in content; **do not** insert an index row for self
(skip when `mentioned_user_id == author`) — no notification target either.

### Schema

```sql
-- One row per (reed, mentioned LOCAL user). mentioning_* = reed that
-- contains the mention. mentioned_user_id is a hard FK because only local
-- mentions are ever stored (see above) — mentioned_server_id is therefore
-- always this server's own id today; kept for when cross-server mention
-- notification lands and may need a different shape.
CREATE TABLE reed_mentions (
    mentioning_user_id  VARCHAR(255) NOT NULL,
    mentioning_reed_id  VARCHAR(255) NOT NULL,
    mentioned_user_id   VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    mentioned_server_id VARCHAR(255) NOT NULL,
    PRIMARY KEY (mentioning_reed_id, mentioned_server_id, mentioned_user_id),
    FOREIGN KEY (mentioning_user_id, mentioning_reed_id)
        REFERENCES reeds(user_id, id) ON DELETE CASCADE
);

CREATE INDEX idx_reed_mentions_mentioned
    ON reed_mentions (mentioned_server_id, mentioned_user_id);

CREATE INDEX idx_reed_mentions_reed
    ON reed_mentions (mentioning_reed_id);
```

Unlike `reed_echoes`'s soft-reference pattern, `reed_mentions` has a real FK
to `reeds(user_id, id)` — deleting a reed (hard delete, not the soft
`reed_removals` cert path) cascades automatically. In practice `DeleteReed`
never hard-deletes the `reeds` row (kept for allocation catch-up), so the
explicit `DeleteMentionsForReed` cleanup call below still does the real
work; the FK is a backstop, not the primary cleanup path. No `signed_at`
column — nothing currently orders by mention recency.

### Deletion

| Event | Action |
|-------|--------|
| Reed removed | `DELETE FROM reed_mentions WHERE mentioning_reed_id = $reedID` |
| Account removed | `DELETE FROM reed_mentions WHERE mentioning_user_id = $userID OR (mentioned_server_id = local AND mentioned_user_id = $userID)` |

Same transaction / call sites as echo-index cleanup.

### APIs

**Search (composer):**

- `GET /users/search?q=&limit=` (auth required).
- Returns `{ users: [{ id, username, … }] }` — minimal fields; no keys.
- Prefix / substring match on username; cap `limit` (e.g. 20).

**Mentions inbox (optional in this step):**

- Not required for v1 if notifications are out of scope.
- Useful smoke/debug: `GET /users/me/mentions?limit=&before=` → metadata
  rows `{ authorId, reedId, signedAt }` for mentions of the caller.
- Listing does **not** return bodies; client relays reeds as usual.

**No** change to countersign response shape.

### Client render

`MarkdownInline` renders a `mention` AST node (produced by `readMention`) via
a dedicated `MentionLink` component:

- Local mention (`serverID` matches this instance): resolve
  `userID -> username` via the verified `userRepository` cache/fetch
  (`GET /users/{id}/profile`, same trust path as any other profile lookup);
  render `@username` linking to `/profile/<userID>`, no confirm modal
  (in-app navigation).
- Foreign mention, or a local lookup that fails/returns nothing: render the
  **literal wire token** `~userID@serverID` as plain text — not a styled
  placeholder, not the raw username-less guess. This is a deliberate
  **revision** from the original "muted inert span" idea: if we can't
  resolve who it is, show exactly what was signed rather than a synthetic
  UI element implying we know something we don't.
- Composer preview may seed an unsigned display hint (the username from the
  `GET /users/search` result just picked) so the picked name shows
  immediately instead of the raw id while the real verified fetch is in
  flight — never persisted, never a substitute for that fetch.
- All other `http(s)` links keep the existing external-link confirm modal.

### Hook to notifications (future)

When [11](../11_user_notifications.md) lands, a producer can:

```text
after insert into reed_mentions:
  for each row where mentioned is local and != author:
    NotifyUser(mentioned, kind="mention", metadata={authorId, reedId, …})
```

This step only guarantees the table and cleanup so that hook is trivial.

## Work items

1. DDL `reed_mentions` + indexes; wire into `createTables`.
2. Go: `ParseMentionURL` / `ExtractMentions(content) []ReedUserRef` (or
   shared `UserRef{ServerID,UserID}`).
3. `SignReed`: extract, validate, insert in create transaction.
4. Deletion paths: clear mentions with echoes.
5. `GET /users/search`.
6. SPA: `@` typeahead in `NewReedModal` (or shared composer); insert canonical
   markdown.
7. SPA: `MarkdownParser` in-app navigation for `web+syrinx://users/...`.
8. Tests: extract vectors; publish with mentions; delete reed clears rows;
   self-mention skipped; search authz.

## Risks

- **Scheme bikeshed** — locking `web+syrinx` early avoids signed content that
  cannot register with browsers later.
- **Username drift** — display text in markdown goes stale; href remains
  correct. Acceptable.
- **Spoofed display names** — `[Not Alice](web+syrinx://users/…/aliceId)` is
  possible; UI may show resolved username from profile cache on render in a
  follow-up (out of scope).
- **Search abuse** — rate-limit / auth-only search.

## Parallelism

Can land after [01](01_publish_and_refs.md) without waiting for reply-list UI.
Coordinate deletion cleanup with whatever owns reed/account removal hooks.
