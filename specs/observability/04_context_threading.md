# Observability 04 — Thread `context.Context` so DB spans nest under the request span

## Status

Implemented.

## Depends on

[02](02_app_bootstrap.md), [03](03_db_instrumentation.md).

## Context

`otelsql` (and any OTEL-instrumented driver) only nests a DB span under the
caller's span if the `context.Context` passed into the `...Context` SQL
method (`QueryRowContext`, `QueryContext`, `ExecContext`) carries that span.
Across the codebase, DB access largely uses the **non-context** methods:

```go
// services.go — representative of most DataService methods
err := s.db.QueryRow(`SELECT ... WHERE u.id = $1`, userID).Scan(&ts)
```

This is not confined to `services.go`. A grep for `*sql.DB` call sites shows
the same pattern in several internal packages:

| Package | Example | Context-aware today? |
|---|---|---|
| `services.go` (`DataService`) | `s.db.QueryRow(...)` | No |
| `invites` | `Store.Insert(ctx, ...)` | **Partially** — some methods already take `ctx` |
| `recovery` | `SaveOwnIdentity(db, ...)`, `SaveFollowing(db, ...)` | No |
| `deletion` | `InsertAccountCert(db, ...)`, `GetAccountCert(db, ...)` | No |
| `realtime` | `DBService{db}` methods | No |

`invites` is the template to follow — it already demonstrates the target
shape for the others.

## Scope

For each package above:

1. Add `ctx context.Context` as the first parameter to every exported
   function/method that touches `*sql.DB` (skip ones that don't run
   queries).
2. Swap `db.Query`/`db.QueryRow`/`db.Exec` for
   `db.QueryContext`/`db.QueryRowContext`/`db.ExecContext`, passing the new
   `ctx` through.
3. At the call sites in `handlers.go`, pass `r.Context()` (the HTTP request's
   context, which already carries the `otelmux`-created span from
   [02](02_app_bootstrap.md)) instead of no context / a fresh
   `context.Background()`.
4. Where a goroutine detaches from the request lifecycle (e.g. the realtime
   broadcast fanout in `main.go`, which runs in `go
   realtimeService.Start(broadcastChan)`), do **not** propagate the
   originating request's context — use a fresh background context (with its
   own span if that codepath is worth tracing) since that work outlives the
   request.

## Non-goals

- Any behavior change beyond adding a parameter and swapping method
  suffixes. This is a mechanical plumbing change, not a logic rewrite.
- Tracing inside `realtime`'s WebSocket long-lived connections as "one span
  per connection" — out of scope; if useful later, treat as a separate spec
  (the semantics of a multi-hour WS connection's "span" don't match a single
  HTTP request's).

## Migration approach

Given the size of `services.go` (~1600 lines, dozens of `DataService`
methods) and the number of packages involved, land this incrementally rather
than as one large change:

1. Start with the handful of hottest/most latency-sensitive endpoints (e.g.
   `GetReed`, `SignReed`, feed/timeline-style reads) to prove out the
   pattern and confirm the resulting traces look right in OpenObserve.
2. Expand package by package (`services.go` → `recovery` → `deletion` →
   `realtime`), following the same shape `invites` already uses.
3. A method not yet migrated simply keeps producing a root span (per
   [03](03_db_instrumentation.md)) instead of a nested one — this is a safe,
   incremental degradation, not a breaking state, so migration order doesn't
   need to be strictly dependency-ordered.

## Open points

- Whether `ctx` becomes the first field threaded through `Services`/
  `Handlers` structs at construction time, or passed per-call only — prefer
  per-call (`ctx` as a function parameter), matching Go convention and the
  existing `invites.Store.Insert(ctx, ...)` precedent.
- Whether to add a lint/CI check (e.g. `contextcheck` or similar) once
  migration is complete, to prevent new call sites from reintroducing
  non-context DB calls.
