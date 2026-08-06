# Loadtest 00 — Design + `API_HOST` proxy trick + locked model

## Status

Proposed.

## Depends on

—

## Context

Every authenticated HTTP request needs `X-Syrinx-*` signature headers
produced from a real PGP key over `signing.BytesToSign` bytes
([middlewares.go](../../middlewares.go) `signatureAuthMiddleware`); signup is
a multi-step signed handshake (server-signed `userID` reservation, user
identity signature, key upload — see `Signup` in
[handlers.go](../../handlers.go)); and the realtime wire is an authenticated,
binary-framed protocol ([proto/websocket.proto](../../proto/websocket.proto),
[realtime/service.go](../../realtime/service.go)). A generic load tool (k6,
Locust, Artillery) or a bespoke client in another language would have to
reimplement all of that signing/framing from scratch — exactly the class of
drift risk `AGENTS.md` calls out for `BytesToSign`: "one helper... so the two
cannot drift." There is already exactly one correct implementation of all of
this: the SPA.

Reed bodies also never live on the server — they're relayed peer-to-peer over
the WS connection (`REQUEST_REED` → `RELAY_RESPONSE`, see
[realtime/README.md](../../realtime/README.md)). A load test that only
replays HTTP would miss this entirely; a load test that drives real,
persistently-connected browser instances gets it automatically, because each
instance behaves exactly like a real peer.

## Scope

- How virtual users get a real, unbundled copy of the SPA pointed at an
  arbitrary target server, without shipping load-test code into the
  production bundle.
- The "script-driven" interaction model: call real SPA functions in-page
  instead of simulating UI clicks.
- The virtual-user lifecycle and safety defaults.
- The metrics model (latency/error-rate per action, fanout latency — detailed
  further in [03](03_fanout_metrics.md)).

## Non-goals

- Reimplementing signing, the signup handshake, or the WS wire outside the
  SPA's own code.
- Distributed/multi-host load generation (single host, many browser contexts
  is the target scale for v1).
- Any cleanup/deletion sweep of load-test-created accounts or reeds.
- Provisioning invites — the target server is assumed to have open signup.
- Load-testing the docs site or the `ops`/`cli` binaries.

## Design

### Why the target must be a Vite dev server, not the built SPA

[spa/vite.config.ts](../../spa/vite.config.ts) already proxies `/api` and
`/ws` (including the WebSocket upgrade) to an `API_HOST` environment
variable:

```125:137:spa/vite.config.ts
  server: {
    proxy: {
      '/api': {
        target: `http://${process.env.API_HOST ?? 'localhost:8080'}`,
        changeOrigin: true
      },
      '/ws': {
        target: `http://${process.env.API_HOST ?? 'localhost:8080'}`,
        changeOrigin: true,
        ws: true
      }
    }
  }
```

Running `API_HOST=<staging-host:port> npm run dev` therefore serves the real
SPA **locally**, unbundled (Vite dev serves ES modules from source, not
minified chunks), while every API call and WS connection is transparently
forwarded to the real target server. This matters because the script-driven
interaction model (below) works by having Playwright's `page.evaluate` run
`await import('/src/lib/services/...')` inside the page — [spa/tests/signup-and-publish.spec.ts](../../spa/tests/signup-and-publish.spec.ts)
already does this today:

```88:100:spa/tests/signup-and-publish.spec.ts
    const verifyResult = await page.evaluate(async () => {
      const reed: any = await new Promise((resolve, reject) => {
        ...
      });
      const { verifyReed } = await import('/src/lib/verifiers/index.ts');
      const ok = await verifyReed(reed);
      return { ok, fingerprint: reed.serverSignature?.fingerprint };
    });
```

Those bare `/src/lib/...` import paths only resolve against Vite's dev
server; `adapter-static`'s production build (`spa/build`, served by the Go
server per `AGENTS.md`) bundles everything into hashed chunks with no stable
import path. So the load test always targets `<local-dev-origin>` in the
browser, with `API_HOST` pointed at whatever server (staging or production)
is actually being load-tested. **No SPA code changes are needed to make this
work** — it is already wired for local dev against any backend.

### Virtual user = one `BrowserContext`

Each virtual user gets its own Playwright `BrowserContext` (own
IndexedDB/localStorage origin — the same isolation a real separate device
would have) and one `Page` navigated to the local dev origin. Contexts share
a small number of browser processes (Playwright's normal model), which is
what makes "tens to low hundreds" of concurrent virtual users tractable on
one host — full separate browser processes would not scale that far.

Lifecycle:

1. Navigate to `/`.
2. Run signup (via `performSignup`, see [01](01_shared_flow_helpers.md)) with
   a run-scoped username, e.g. `loadtest_<runID>_<n>`.
3. Loop for the configured run duration: pick a weighted-random action
   (publish, follow, subscribe/unsubscribe a pipe, read a reed/profile),
   execute it via the real function, sleep a randomized think-time, repeat.
   See [02](02_driver.md) for the scenario/weight model.
4. Keep the WS connection open for the whole run — relay/fanout only works
   while the page is alive and subscribed.
5. Close the context at the end of the run. Do not delete the account or its
   reeds (locked decision: no cleanup).

### Ramp-up and safety defaults

Because the target is explicitly a staging/production-like server (not a
disposable local instance), the driver's defaults must be conservative:

- Default concurrency: low tens of virtual users, ramped up gradually (e.g.
  10 new contexts/second) rather than launched all at once.
- Scaling beyond the default requires an explicit flag/env var — there is no
  "load test the whole fleet" default.
- No rate-limiting is currently implemented server-side (not documented in
  `RISKS.md` or `AGENTS.md`); the driver is the only thing standing between
  "load test" and "unintentional DoS," so ramp-up and concurrency caps live
  in the driver, not the server.
- Because there is no cleanup, prefer shorter, more frequent runs with fresh
  run IDs over one very long soak, to keep affected accounts/reeds easy to
  identify after the fact if something needs to be manually cleaned up later.

### Metrics model (overview — see 03 for fanout specifically)

Every in-page action is timed with `performance.now()` around the real call
and returns `{ action, durationMs, ok, error }` to the Node driver, which
aggregates per-action p50/p95/p99 latency and error rate over the run.
Publish→delivery fanout latency needs cross-context timestamp correlation
and is specified separately in [03](03_fanout_metrics.md).

## Open points (resolve in 01–03)

- Exact scenario weight defaults (proposed in [02](02_driver.md); tune once
  a first run against real staging exists).
- Whether a future step adds server-side rate limiting — tracked as a
  follow-up, not blocking this spec.
