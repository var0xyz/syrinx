# Notifications 01 — `@everyone` admin broadcast handle

## Status

Proposed.

## Depends on

[00](00_glossary_and_design.md)

## Context

See [00](00_glossary_and_design.md#admin-mention-everyone) for the full
rationale. Summary: `@everyone` is a publish-time, admin-gated expansion
that inserts a `reed_mentions` row for every local user, reusing the
existing mention index rather than inventing new storage or delivery.

## Scope

- Detect the literal `@everyone` token in reed content at publish time.
- Gate expansion on `roles.IsAdmin(author.Role)`.
- Expand via one `INSERT ... SELECT` inside the existing `CreateReed`
  transaction — not a per-recipient loop.
- Non-admin authors' `@everyone` text is inert: stored, not expanded, no
  error.

## Non-goals

- No `@role`/`@group` generalization.
- No broadcast-retry mechanism (see [00](00_glossary_and_design.md#broadcast-reliability--documented-gap-not-implemented) —
  documented there, not built here or anywhere in this spec).
- No cross-server expansion (see [00](00_glossary_and_design.md#federation-boundary)).

## Design

### Detection

`handlers.go`'s `SignReed` already extracts local mentions before calling
`CreateReed` (`handlers.go:1614-1636`, `ExtractMentions` +
`MentionTargetValid` loop building `localMentions`). Add a plain substring
check on `contentBody` for the literal token `@everyone` at the same
point. This is intentionally *not* part of `mentions.go`'s
`ExtractMentions` regex (`~<userID>@<serverID>`) — `@everyone` has no
userID to resolve, so it is checked and handled entirely separately, not
folded into the existing mention-parsing pass.

### Authorization

If the token is present, check `roles.IsAdmin(user.Role)` using the
already-fetched author profile (`handlers.go:1640-1649` already loads
`user` before building `markdown` — reuse that, no extra query). If the
author is not admin/root, treat the token as ordinary text: no expansion,
no error, no special handling. The reed publishes normally.

### Expansion

If the author is admin/root, set a new flag on `createReedParams` (e.g.
`BroadcastToEveryone bool`) alongside the existing `Mentions []ReedRef`
field (`services.go:1240-1253`). Inside `insertReedCoreTx`
(`services.go:1444`), after the existing per-mention insert loop
(`services.go:1508-1518`), add:

```go
if p.BroadcastToEveryone {
    if _, err := tx.ExecContext(ctx, `
        INSERT INTO reed_mentions (
            mentioning_user_id, mentioning_reed_id,
            mentioned_user_id, mentioned_server_id
        )
        SELECT $1, $2, id, $3 FROM users
        ON CONFLICT (mentioning_reed_id, mentioned_server_id, mentioned_user_id) DO NOTHING
    `, p.UserID, p.ReedID, localServerID); err != nil {
        return Reed{}, fmt.Errorf("insert everyone broadcast: %w", err)
    }
}
```

One statement, same transaction, same `ON CONFLICT DO NOTHING` idempotency
already used for regular mentions two lines above it. No loop over users
in application code — this is what keeps the operation cheap and atomic
regardless of userbase size (see [00](00_glossary_and_design.md#broadcast-reliability--documented-gap-not-implemented)
for what atomicity does and doesn't guarantee here).

The author themselves should not get a `reed_mentions` row for their own
broadcast (self-mentions are already excluded by `ExtractMentions` for
regular tokens per `mentions.go`'s doc comment) — the `SELECT id FROM
users` should exclude `p.UserID`, matching that existing behavior.

### Non-admin authors

No code path change needed beyond "don't set `BroadcastToEveryone`." The
literal string `@everyone` in a non-admin's reed content is never
inspected beyond the initial detection check (which only decides whether
to *attempt* gating, not whether to reject); it publishes as plain text.

## Testing

(Documented expectations for whoever implements this — not run here.)

- Admin publishes a reed containing `@everyone` → every other local user
  (not the author) gets a `reed_mentions` row for that reed.
- Non-admin publishes a reed containing `@everyone` → no `reed_mentions`
  rows beyond any regular `~userID@serverID` tokens also present; reed
  content still literally contains the text.
- Idempotency: retried/duplicate publish attempts (same `reedID`) do not
  double-insert (`ON CONFLICT DO NOTHING`, same as regular mentions).
- A transaction failure partway through (e.g. a later statement in the
  same `CreateReed` transaction errors) leaves zero `reed_mentions` rows
  from the broadcast — full rollback, not partial fanout.

## Dependencies

- [00](00_glossary_and_design.md) for the locked model.
- Reuses `reed_mentions` schema and `CreateReed`/`insertReedCoreTx`
  transaction from [conversations 04](../conversations/04_mentions.md).
- Reuses [`roles.IsAdmin`](../../roles/roles.go).
