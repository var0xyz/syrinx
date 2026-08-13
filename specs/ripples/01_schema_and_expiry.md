# Ripples 01 — Schema + expiry sweep

## Status

Proposed.

## Depends on

[00](00_design.md)

## Context

[00](00_design.md) locks the model: a reed may have multiple independent
threads, flat ripple rendering, whole-**reed** expiry (shared across all of
a reed's threads) at `expires_at`, soft delete on self-delete, and
user-signed, server-countersigned responses whose id is a content hash of
the server payload. This step turns that into a schema and an expiry
mechanism.

## Scope

- Schema: `ripples`, `ripple_responses` tables (in the existing `db.go`
  schema-bootstrap file, alongside every other table — **not** a separate
  Go package; see [README](README.md)'s Code organization note).
- Store methods needed by 02/03 (insert + verify + hash, list,
  soft-delete, bump-activity — all as one transaction where applicable).
- The expiry sweep itself: a standalone Go binary run on a schedule by
  cron, not a goroutine inside the main server process.

## Non-goals

- The HTTP API surface (02).
- Realtime fanout (03).
- Client-side signature verification (00's Client-side verification
  section; that logic lives in the SPA, see 04).

## Design

### Schema

```sql
-- One row PER REED that has ever received a ripple (bookkeeping only, no
-- content). Created lazily on the reed's first-ever response, across any
-- of its threads. expires_at is bumped by ANY new response on ANY
-- thread under this reed — the 7-day clock is shared across all threads on
-- a reed, not per-thread (see 00's Lifetime / expiry rule).
--
-- FK to reeds(user_id, id) uses ON DELETE CASCADE. This is a deliberate
-- departure from the soft-reference (no-FK) pattern reed_replies/
-- reed_removals use — see "FK behavior" below for why.
CREATE TABLE IF NOT EXISTS ripples (
    reed_author_id   VARCHAR(255) NOT NULL,
    reed_id          VARCHAR(255) NOT NULL,
    expires_at       TIMESTAMP NOT NULL,

    PRIMARY KEY (reed_author_id, reed_id),
    FOREIGN KEY (reed_author_id, reed_id) REFERENCES reeds(user_id, id)
        ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_ripples_expires
    ON ripples (expires_at);

-- One row per ripple response (content). id is the hex-SHA256 hash of the
-- signed server payload (see 00's Signing section) — not a random id, and
-- not recomputed on soft-delete (the id is a fingerprint of what was
-- originally signed, frozen at creation).
--
-- thread_id is a client-minted UUID, signed as part of the user payload.
-- The server only validates it is a syntactically well-formed UUID and,
-- on a reply, that it matches the parent response's stored thread_id —
-- the server never invents a thread_id itself.
--
-- user_signature / server_signature: base64(armored PGP), same wire
-- convention as every other signature in this codebase. Both are
-- required — there is no partially-signed or unsigned ripple response.
--
-- deleted + content='[DELETED]' is a soft delete (see 00's Moderation
-- section). Rows are never removed by user self-delete; they only
-- disappear when the parent `ripples` row's expires_at sweep fires and
-- ON DELETE CASCADE removes them. Soft-deleting does NOT touch id,
-- user_signature, user_fingerprint, or server_signature — those continue
-- to describe the original (pre-delete) content, which is expected (see
-- 00's Client-side verification, tombstone handling).
CREATE TABLE IF NOT EXISTS ripple_responses (
    id                VARCHAR(64) PRIMARY KEY,
    reed_author_id    VARCHAR(255) NOT NULL,
    reed_id           VARCHAR(255) NOT NULL,
    thread_id         UUID NOT NULL,
    user_id           VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    content           VARCHAR(140) NOT NULL,
    replying_to       VARCHAR(64) REFERENCES ripple_responses(id) ON DELETE SET NULL,
    deleted           BOOLEAN NOT NULL DEFAULT FALSE,
    posted_at         TIMESTAMP NOT NULL,

    user_fingerprint       VARCHAR(255) NOT NULL,
    user_signature_id      INT NOT NULL REFERENCES user_signatures(id),
    server_signature_id    INT NOT NULL REFERENCES server_signatures(id),

    FOREIGN KEY (reed_author_id, reed_id) REFERENCES ripples(reed_author_id, reed_id)
        ON DELETE CASCADE
);
```

`id VARCHAR(64)` — 64 hex characters is exactly a SHA-256 digest's
length; sized precisely, not left open-ended like the random
`VARCHAR(255)` ids elsewhere in this codebase, since the format is
fully determined by the hash function rather than a configurable
alphabet/length pair (`crypto.Alphabet`/`crypto.Length`).

`user_signature_id`/`server_signature_id` reuse the existing
`user_signatures`/`server_signatures` tables and `signing.InsertUserSignature`/
`InsertServerSignature` helpers already used by every other signed entity
in this codebase (reeds, profiles, key rotations, removals) — no new
signature-storage tables. `user_fingerprint` is stored directly on the row
(not only inside the `user_signatures` join) because it's part of what was
signed (see 00) and callers rebuilding the user/server payloads for
re-verification need it without an extra join.

```sql
-- Supports both "which threads exist under this reed, oldest root first"
-- and "responses in this thread, in post order" — the two access patterns
-- the list query (02) needs.
CREATE INDEX IF NOT EXISTS idx_ripple_responses_reed_thread_posted
    ON ripple_responses (reed_author_id, reed_id, thread_id, posted_at);

-- Resolves a thread's creation time (MIN(posted_at) for that thread_id)
-- when ordering threads oldest-first.
CREATE INDEX IF NOT EXISTS idx_ripple_responses_thread_posted
    ON ripple_responses (thread_id, posted_at);
```

Naming note: `ripples.reed_author_id`/`reed_id` intentionally do **not**
reuse the column name `user_id` the way `reed_replies`/`reed_removals` do
for their equivalent reed-identifying columns. `ripple_responses` already
has its own `user_id`, meaning "the responder" — reusing `user_id` across
both tables with two different meanings (reed author vs. commenter) would
be a footgun.

There is no separate table for threads themselves — `thread_id` is
purely a grouping key stamped onto responses (client-minted, signed, and
validated — see above), not a row anywhere in the schema. A thread's
creation time is derived (`MIN(posted_at) GROUP BY thread_id`) at query
time, never stored.

### FK behavior — why `ripples` FKs to `reeds` with `ON DELETE CASCADE`

`ripples` carries a real foreign key to `reeds(user_id, id)`, with
`ON DELETE CASCADE` — enforcing referential integrity rather than
leaving a soft/dangling reference, unlike the soft-reference (no-FK)
pattern `reed_replies`/`reed_removals` use for their own equivalent
columns. Those tables avoid the FK because a reed can be soft/cert-only-
removed while its row still exists; that reasoning doesn't extend here,
because this codebase's `DeleteReed` operation genuinely, permanently
removes the `reeds` row (distinct from *account* removal, which never
touches `reeds` at all — see the Account removal section below and
[00](00_design.md)'s own Account removal section).

The accepted tradeoff: deleting an individual reed immediately removes
its ripples bookkeeping row (and, via the second cascade, every response
across every thread on it) — even if some of those responses are under
7 days old and would otherwise still have time left on the shared clock.
This is a deliberate behavior change, not a subtle side effect: ripples
do not outlive a removed reed.

### Account removal

Account removal in this codebase is **cert-only** — it inserts an
`account_removals` row and never deletes the `users` row or any `reeds`
row belonging to that user. Because the FK above only cascades on an
actual `DELETE FROM reeds`, an account removal by itself never triggers
the cascade at all. Concretely:

- A removed user's own past responses, posted on other people's reeds,
  are completely unaffected by the removal — they simply live out their
  reed's normal shared 7-day window like any other response, and their
  signatures remain valid and re-verifiable regardless of the author's
  current account-removal state (see [00](00_design.md)).
- The ripples section on a removed user's *own* reeds becomes
  inaccessible (410) immediately, but this is enforced one layer up, at
  the HTTP handler's existing parent-reed lookup (see
  [02](02_post_and_list_api.md)), not by any schema-level mechanism — the
  underlying `ripples`/`ripple_responses` rows for that reed are left
  completely alone by an account removal and still get cleaned up by the
  normal sweep on their normal schedule, same as any other reed's.

See [00](00_design.md)'s Account removal section for the full rule
statement; this section only covers why the schema itself requires no
special handling for it.

### Insert / activity-bump transaction

Posting a response (used by [02](02_post_and_list_api.md)) is one
transaction:

0. **Resolve and validate `thread_id`.** The client submits `threadID`
   (a UUID it minted — see [00](00_design.md)'s Thread shape) as part of
   its signed payload. If `replyingTo` is present, look up that
   response's stored `thread_id` and reject (400, at the handler layer —
   see [02](02_post_and_list_api.md)) if it doesn't match the
   client-submitted value. If `replyingTo` is absent, the submitted
   `threadID` is accepted as the start of a new thread with no
   cross-check needed (nothing to check it against).
1. **Rebuild the user payload** from the submitted fields (`reedAuthorID,
   reedID, rippleAuthorID, fingerprint, threadID, replyingTo?` headers +
   `content`) and verify `userSignature` against the caller's active
   public key. Reject (400) on failure — this is authentication, not
   optional.
2. **Build the server payload** (same headers plus `serverID` and a
   server-side `now()` timestamp, content = `userSignature`) and
   countersign it via the existing shared `h.countersign(...)` primitive
   — no new countersigning mechanism, see [00](00_design.md)'s Signing
   section.
3. **Compute `id` = hex-SHA256 of the server payload bytes.**
4. `INSERT INTO ripples (reed_author_id, reed_id, expires_at) VALUES
   (...) ON CONFLICT (reed_author_id, reed_id) DO UPDATE SET expires_at =
   EXCLUDED.expires_at` — creates the reed's bookkeeping row on its first
   response, bumps it on every subsequent one regardless of which thread
   the new response belongs to (shared clock across all of a reed's
   threads, per [00](00_design.md)). `expires_at` is computed from the
   same server-side `now()` reading as step 2's timestamp — one clock
   reading per request, no separate read-then-write race.
5. `INSERT INTO ripple_responses (id, reed_author_id, reed_id, thread_id,
   user_id, content, replying_to, deleted, posted_at, user_fingerprint,
   user_signature_id, server_signature_id) VALUES (..., FALSE, now,
   ...)`.

No client-supplied timestamp anywhere in this flow — `posted_at` and
`ripples.expires_at` both come from the same server-side `now()` reading
in step 4/5, matching the timestamp discipline every other signed entity
in this codebase already follows (server clock only, client clock never
trusted for anything that gets persisted or signed over).

### Soft delete

Self-delete (used by [02](02_post_and_list_api.md)) flips `deleted` to
`true` and replaces `content` with the literal string `"[DELETED]"` for
one `ripple_responses` row, gated on the caller owning it (session-
authenticated caller matches `user_id` — a self-delete is **not**
re-signed, see [00](00_design.md)'s Moderation section). It does **not**
touch `ripples.expires_at` (deleting isn't posting) and does **not**
touch `id`, `user_fingerprint`, `user_signature_id`, or
`server_signature_id` — those continue to describe the original,
pre-delete content forever, which is the whole point: the row's identity
and attestation are a record of what was originally posted, not a live
projection of current state. It also does **not** null out any other
response's `replying_to` pointer into the deleted row — a reply still
resolves through the still-present, now-tombstoned row rather than
losing its addressing target. (The `replying_to` column's own `ON DELETE
SET NULL` clause exists for a different, much rarer case — see below —
not for this one.)

### Expiry sweep

The sweep is an **external cron job**, independent of the main server
process — its schedule survives app restarts, crashes, and deploys,
since cron is a separate, always-running system service. A `time.Ticker`
goroutine inside the app would not have this property: its countdown
resets to zero on every process restart, and this codebase's own deploy
flow (`update.sh`) restarts the service on every deploy — a mechanism
whose reliability depends on uptime between restarts is the wrong choice
here.

- **`jobs/ripples-cleanup/`** — a standalone Go program, compiled to its
  own binary, independent of the main `syrinx` binary. Connects to
  Postgres using the same `DB_HOST`/`DB_PORT`/`DB_USER`/`DB_PASSWORD`/
  `DB_NAME` values the main app reads from `/etc/$APP_NAME/app.env` (see
  `deploy/scripts/syrinx/setup.sh`'s `ENV_FILE`) — no separate credential
  story. Runs exactly one statement:

  ```sql
  DELETE FROM ripples WHERE expires_at <= NOW()
  ```

  The delete target is `ripples` (the bookkeeping table), not
  `ripple_responses` — the job never touches the content table directly.
  `ripple_responses` rows cascade-delete via their own `ON DELETE CASCADE`
  FK to `ripples(reed_author_id, reed_id)`, removing every thread's
  responses on that reed in the same statement. This two-table split —
  bookkeeping row per reed, content rows FK'd to it — is the entire
  mechanism that makes "delete one small row, and an arbitrary number of
  threads/responses across that reed vanish for free" possible; it is the
  reason this schema is split into two tables at all rather than one.

  `replying_to`'s `ON DELETE SET NULL` clause is what actually fires here:
  when the job cascade-deletes a whole reed's responses, any response
  *outside* that reed whose `replying_to` happened to point at one of the
  deleted rows... cannot exist, since `replying_to` is only ever set to
  another response in the *same* thread (see [00](00_design.md)'s Thread
  shape), and the whole thread is being deleted together in the same
  cascade. This clause is therefore close to inert in steady-state
  operation; it's kept for defensive completeness and for a possible
  future moderation/admin hard-delete tool.

- **`jobs/ripples-cleanup.cron`** — the crontab fragment defining the
  job's schedule, checked into the repo next to the job it drives.
  Naming convention: `<job-dir>.cron` — every future `jobs/*` entry gets
  its own same-named `.cron` file, one schedule per file, no shared or
  aggregate crontab. Interval: **every minute** — keeps the worst-case
  overshoot (a reed's ripples living past their shared `expires_at`)
  small and makes expiry behavior easy to observe/verify operationally.
  No admin override/config for the interval in v1 — hardcode it in the
  checked-in `.cron` file, revisit only if operational experience says
  otherwise.

- **Deploy wiring**: `deploy/scripts/syrinx/update.sh` (which already
  shallow-clones the repo fresh on every run) builds and installs the
  job binary to a fixed path on the server and merges
  `jobs/ripples-cleanup.cron`'s schedule into the `$APP_USER` system
  crontab (`crontab -u $APP_USER`), replacing any previous version of
  that job's line on every deploy — the same "recreate from the repo's
  current definition, don't hand-drift" posture `update.sh` already
  takes with the systemd unit and nginx config it manages.
  `deploy/scripts/syrinx/setup.sh` (first-time install) performs the
  equivalent one-time install.

## Work items

1. `db.go` — DDL, appended to `InitDB`'s existing schema-bootstrap list
   (same file, same pattern as every other table; **not** a new package).
2. `services.go` — `PostRipple` (rewritten for the sign/countersign/hash
   transaction above), `ListRipples`, `SoftDeleteRipple`, plus the new
   sign/verify/hash helper functions this requires (rebuild-user-payload,
   rebuild-server-payload, hash-server-payload).
3. `jobs/ripples-cleanup/` (Go program) + `jobs/ripples-cleanup.cron` —
   the expiry sweep, external to the main binary (see Expiry sweep
   above; no `main.go` change).
4. `deploy/scripts/syrinx/update.sh` and `deploy/scripts/syrinx/setup.sh`
   — build/install the job binary and its crontab entry.
5. Tests: bookkeeping row creation on a reed's first response, expiry
   extension on a second response to a *different* thread on the same
   reed (confirms the shared-clock rule), self-delete of a response flips
   `deleted`/`content` in place without removing the row, recomputing its
   `id`, or altering any `replying_to` pointer into it, and a subsequent
   list fetch still renders it as a tombstone. Plus new signing-specific
   tests: post with a valid user signature but a `threadID` that doesn't
   match the `replyingTo` target's stored `thread_id` is rejected; post
   with an invalid/forged `userSignature` is rejected; the returned `id`
   is a stable, reproducible hash given fixed input bytes. The cleanup
   job gets its own coverage: run the compiled binary against a seeded
   test database and assert it deletes an expired reed's bookkeeping row,
   cascades every thread's responses, and leaves non-expired rows alone.

## Risks

- **Sweep query cost at scale** — `DELETE ... WHERE expires_at <= NOW()`
  with the `idx_ripples_expires` index should stay cheap even with many
  reeds carrying ripples; the delete only ever scans/deletes bookkeeping
  rows (bounded by distinct-reeds-with-ripples count), never content rows
  directly. Revisit batching only if it becomes a measured problem.
- **Clock skew** — server-only timestamps throughout for everything
  persisted, no client clock ever trusted for anything signed or stored;
  a non-issue by construction.
- **Reed deletion force-expires ripples early** — see "FK behavior"
  above. Accepted tradeoff, not a bug.
- **Signing/verification adds server-side CPU cost per POST** — every
  post requires a PGP signature verification (user) plus a PGP sign
  operation (server countersign) plus a SHA-256 hash. Not expected to be
  a bottleneck at this product's stated small-community scale.
- **Cron is a new operational dependency** — the app's own correctness
  (schema, signing, API) does not guarantee expiry happens; a server
  where the cron install step failed (or was skipped on a manually-
  provisioned box that didn't go through `setup.sh`/`update.sh`)
  accumulates ripples past their `expires_at` indefinitely, with nothing
  in the app itself surfacing that fact. Worth a follow-up monitoring/
  alerting item (e.g. a check for "oldest un-swept expired `ripples`
  row") if this proves to matter in practice, but out of scope here.

## Dependencies

`signing.BytesToSign`, `identity` package conventions, `crypto.Hash`,
`h.countersign`, `signing.InsertUserSignature`/`InsertServerSignature` —
all existing, reused as-is. `jobs/ripples-cleanup` depends only on the
same DB connection env vars the main app already reads — no dependency
on the main binary or any of its packages beyond what it chooses to
import directly.

## Parallelism

02 depends on the store methods here; can be scaffolded in parallel once
the schema and method signatures are agreed, actual implementation waits
on this landing.
