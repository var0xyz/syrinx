# Observability 03 — DB query spans via `otelsql`

## Status

Implemented (`obs.OpenDB` via `otelsql`, `RegisterDBStats`).

## Depends on

[02](02_app_bootstrap.md) (needs a live `TracerProvider` registered globally).

## Context

`ObservabilityManager.InstrumentDatabase` is a stub:

```go
func (om *ObservabilityManager) InstrumentDatabase(db *sql.DB) (*sql.DB, error) {
	return db, nil
}
```

No query ever produces a span today, and it's unused from `main.go` anyway
(`main.go` opens `db` with plain `sql.Open("postgres", dbURL)` and never
calls `InstrumentDatabase`).

## Scope

Replace `sql.Open` with `github.com/XSAM/otelsql`'s instrumented open, at the
single call site in `main.go`:

```go
import (
    "github.com/XSAM/otelsql"
    semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
)

db, err := otelsql.Open("postgres", dbURL, otelsql.WithAttributes(
    semconv.DBSystemPostgreSQL,
))
if err != nil { ... }
defer db.Close()

reg, err := otelsql.RegisterDBStatsMetrics(db, otelsql.WithAttributes(
    semconv.DBSystemPostgreSQL,
))
if err != nil { ... }
defer reg.Unregister()
```

This makes `InstrumentDatabase` unnecessary — either delete it or fold the
above into it and call it once from `main.go` right after `sql.Open`/`Ping`,
whichever reads more naturally with the existing boot sequence (`db.Ping()`
happens immediately after `sql.Open` today; `otelsql.Open` returns a regular
`*sql.DB` so `Ping()` is unaffected).

`RegisterDBStatsMetrics` gives connection-pool metrics (open/idle/in-use
connections, wait count/duration) for free — a reasonable bonus signal for
spotting pool exhaustion, on top of the per-query spans.

### Privacy: no query arguments in spans

`otelsql` captures the SQL statement text on each span by default. It does
**not** capture bound argument values unless the caller explicitly opts in
via a query-args span option (not used here) — leave that off. Verify this
with a quick manual check once implemented: run a query with a sensitive
argument (e.g. a username) and confirm the exported span's attributes show
only `SELECT ... FROM users WHERE username = $1`, not the literal value.

## Non-goals

- Correlating these spans under the HTTP request span that triggered them —
  every `DataService`/`invites`/`recovery`/`realtime`/`deletion` call site
  today uses the non-context SQL methods (`db.QueryRow`, not
  `db.QueryRowContext`), so `otelsql` will produce **root** spans, not
  children of the request span, until [04](04_context_threading.md) lands.
  These root spans are still useful in isolation (you can see "this query
  took 40ms" in OpenObserve right away) — they just won't yet show up nested
  under "this request took 80ms."
- Instrumenting non-Postgres access, if any is added later — out of scope,
  no other DB in use today.

## Verification

Once 02 + 03 land (before 04), you should already be able to query
OpenObserve's trace explorer and see two independent kinds of spans
appearing: one per HTTP request (from `otelmux`) and one per DB call (from
`otelsql`), each with accurate durations — just not yet linked into a single
trace per request. That linkage is exactly what 04 adds.
