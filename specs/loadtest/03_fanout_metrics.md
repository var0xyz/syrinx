# Loadtest 03 — Publish → delivery fanout-latency correlation

## Status

Proposed.

## Depends on

[02](02_driver.md)

## Context

The most distinctive thing about Syrinx's architecture is that reed content
is never stored server-side — new reeds fan out to followers/pipe
listeners/broadcast subscribers via a live WS relay
([realtime/service.go](../../realtime/service.go),
[realtime/README.md](../../realtime/README.md)). Per-action latency (how
long a single publish HTTP call took) says nothing about how long it takes
for that reed to actually *arrive* at other connected clients under load —
that number only exists if two different virtual users' timestamps are
correlated by reed ID. This step specifies that correlation, layered on top
of the driver from [02](02_driver.md).

Pipe deliveries are the easiest fanout path to correlate because they are
run-scoped by construction: every virtual user in a run can subscribe to the
same `#loadtest_<runID>` tag, so any publish tagged with it is expected to
reach every other subscribed virtual user, with no dependency on the
follow graph.

## Scope

- Every virtual user subscribes to the run's pipe tag
  (`serverConnection.subscribePipe`) for the duration of the run, in addition
  to whatever action it's currently performing.
- Publish actions tagged with the run's pipe tag record a
  `publishedAt` timestamp (from inside the same `page.evaluate` that calls
  `performPublish`, right after the publish promise resolves) and report the
  reed ID back to the Node driver.
- Every virtual user's page listens for pipe deliveries
  (`pipeReedQueue` in [reeds.ts](../../spa/src/lib/repositories/reeds.ts), or
  the underlying `ServerEvent.PipeReed` on
  [serverConnection.ts](../../spa/src/lib/services/serverConnection.ts)) and
  records `{ reedID, deliveredAt }` for each one it sees, surfaced to the
  Node driver via a lightweight polling or event bridge (see Design).
- The Node driver correlates `publishedAt` (one sample per publish) against
  every `deliveredAt` it received for that `reedID` (one sample per
  *other* subscribed virtual user) and reports the distribution of
  `deliveredAt - publishedAt` deltas as the fanout-latency metric.

## Non-goals

- Correlating follow-graph (`FOLLOW_REED`) or broadcast (`BROADCAST_REED`)
  delivery paths — same technique would apply, but pipe delivery is the v1
  target because it doesn't require building a follow graph first.
- Clock synchronization beyond what a single machine already guarantees —
  all pages run in the same Node/Playwright process's browser, so
  `Date.now()` inside every page is already reading the same host clock
  (no NTP-drift concern to solve here).
- Measuring server-side relay internals directly (e.g. instrumenting
  `realtime/connection_manager.go`) — this step is black-box, client-observed
  latency only.

## Design

### Getting events out of the page without polling every tick

Playwright's `page.exposeFunction` lets in-page code call back into the Node
process directly, which is a cleaner fit here than polling: expose a
`__loadtestReportDelivery(reedID, deliveredAt)` function on each virtual
user's page before navigation, and have a small in-page listener (installed
once, right after `performSignup` resolves) call it whenever a `PIPE_REED`
event fires:

```js
// installed via page.evaluate once, after signup:
const { serverConnection, ServerEvent } = await import('/src/lib/services/serverConnection.ts');
serverConnection.on(ServerEvent.PipeReed, (data) => {
  window.__loadtestReportDelivery(data.reedID ?? data.reed_id, Date.now());
});
```

The Node-side `runVirtualUser` (from [02](02_driver.md)) registers the
exposed function to forward straight into a shared `FanoutTracker` (new,
`spa/loadtest/fanoutTracker.mjs`) keyed by `reedID`.

### `FanoutTracker`

```js
class FanoutTracker {
  recordPublish(reedID, publishedAt) { /* ... */ }
  recordDelivery(reedID, deliveredAt) { /* ... */ }
  // called at report time: for every published reedID, compute
  // deliveredAt - publishedAt for each recorded delivery.
  summarize() { /* -> { p50, p95, p99, samples, missed } */ }
}
```

`missed` tracks reed IDs that were published but never observed as
delivered to any other subscriber within a grace window (e.g. 30s after
publish) — a non-zero `missed` count under load is itself an important
signal (dropped/failed fanout), not just a metrics-collection artifact.

### Wiring into the existing driver

- `virtualUser.mjs` calls `context.exposeBinding` (or `page.exposeFunction`)
  once per page, before the action loop starts.
- The publish action (already timed for the per-action latency report from
  [02](02_driver.md)) additionally calls `metrics.fanout.recordPublish(reedID,
  publishedAt)` when the tagged variant is chosen.
- `runner.mjs`'s final report prints the per-action latency table (from 02)
  and a separate "fanout latency" section from `FanoutTracker.summarize()`.

## Work

1. `spa/loadtest/fanoutTracker.mjs` — tracker + summarize implementation.
2. Wire `serverConnection.on(ServerEvent.PipeReed, ...)` + `exposeFunction`
   bridge into `virtualUser.mjs`'s setup (right after signup, before the
   action loop).
3. Tag a configurable fraction of publish actions with the run's pipe tag
   (reuse the existing "some fraction tagged" note from
   [02](02_driver.md)'s scenario table) and record `publishedAt` on those.
4. Extend `runner.mjs`'s final report with the fanout-latency section.

## Acceptance

- Running the driver with ≥2 concurrent virtual users produces a non-empty
  fanout-latency distribution (i.e. at least one publish is observed as
  delivered to at least one other subscriber).
- `missed` is reported explicitly and is not silently folded into the
  latency percentiles.
- Killing the target server's realtime connection mid-run (manual test)
  causes `missed` to rise instead of the driver hanging or crashing.
