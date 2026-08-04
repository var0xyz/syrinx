# Loadtest 02 — Playwright driver: virtual users, scenario mix, config

## Status

Proposed.

## Depends on

[00](00_design.md), [01](01_shared_flow_helpers.md)

## Context

`@playwright/test` is already a devDependency
([spa/package.json](../../spa/package.json)), used today for the correctness
e2e suite (`spa/tests/*.spec.ts`, `npm run test:e2e`). The load test is a
different kind of program — it runs for a configured duration, ramps
concurrency up, and reports aggregated metrics rather than pass/fail
assertions — so it is a standalone driver script using Playwright's browser
APIs directly (`chromium.launch()`, `browser.newContext()`), not a
`test()` block, and lives next to (not inside) the correctness suite.

## Scope

- New `spa/loadtest/` directory (sibling to `spa/scripts/`, the existing home
  for standalone node harnesses).
- Config surface: target dev origin, concurrency, ramp rate, run duration,
  scenario weights, think-time range.
- The weighted scenario picker and per-virtual-user loop.
- A results summary (console + optional JSON file) with per-action
  latency percentiles and error rates.
- An `npm run loadtest` script wired into `spa/package.json`.

## Non-goals

- A UI/dashboard — console + JSON output is enough for v1.
- Distributed coordination across hosts (see [00](00_design.md) non-goals).
- The fanout-latency-specific correlation logic — that is
  [03](03_fanout_metrics.md).

## Design

### Layout

```
spa/loadtest/
  runner.mjs      # entry point: parses config, launches browser, ramps
                  # contexts, runs the per-user loop, prints the report
  virtualUser.mjs # one virtual user's lifecycle (signup -> action loop)
  scenarios.mjs   # weighted action picker + think-time sampling
  metrics.mjs     # in-process aggregation (histograms per action) + report
```

### Config (env vars, following the existing `API_HOST` convention)

| Var | Default | Meaning |
|-----|---------|---------|
| `LOADTEST_ORIGIN` | `http://localhost:5173` | Local Vite dev origin to navigate to (must be the dev server that has `API_HOST` pointed at the real target) |
| `LOADTEST_USERS` | `20` | Target concurrent virtual users |
| `LOADTEST_RAMP_PER_SEC` | `2` | New contexts started per second until `LOADTEST_USERS` is reached |
| `LOADTEST_DURATION_SEC` | `120` | How long each virtual user keeps looping after signup |
| `LOADTEST_RUN_ID` | random 8 chars | Used in usernames (`loadtest_<runID>_<n>`) and the pipe tag (`#loadtest_<runID>`) |
| `LOADTEST_THINK_MS_MIN` / `_MAX` | `1000` / `5000` | Randomized delay between actions per virtual user |
| `LOADTEST_HEADLESS` | `true` | Passed through to `chromium.launch({ headless })` |
| `LOADTEST_REPORT_JSON` | unset | If set, path to write the final metrics report as JSON |

`LOADTEST_USERS` and the safety ramp default are deliberately conservative
(see [00](00_design.md) "Ramp-up and safety defaults") — scaling up is an
explicit choice, not the default.

### Scenario mix (`scenarios.mjs`)

A weighted action table, tunable via the module but with a sane v1 default:

| Action | Weight | Notes |
|--------|--------|-------|
| Publish a reed | 30% | ~1-in-3 tagged with `#loadtest_<runID>` so the pipe has something to fan out |
| Follow another virtual user | 20% | Picked from the run's own registry (below); no-op if the registry is still empty |
| Subscribe/unsubscribe the run's pipe tag | 15% | Toggles the virtual user's own pipe subscription |
| Read a reed authored by another virtual user | 25% | Exercises `REQUEST_REED`/`RELAY_RESPONSE` relay when not already held locally |
| Read own profile | 10% | Cheap baseline read |

Each pick is followed by a random think-time in
`[LOADTEST_THINK_MS_MIN, LOADTEST_THINK_MS_MAX]` before the next pick.

### Cross-user registry

`runner.mjs` keeps a single in-process registry (plain array/map, no
persistence needed) of `{ userID, username }` for every virtual user that
has completed signup, appended to as each `virtualUser.mjs` instance finishes
its signup step. "Follow" and "read a reed by another virtual user" pick a
random *other* entry from this registry. Early in a run, before enough users
have signed up, these actions degrade to a no-op (logged, not an error) —
acceptable given the run is meant to run longer than the ramp-up period.

### Virtual user loop (`virtualUser.mjs`)

```js
export async function runVirtualUser({ browser, index, config, registry, metrics }) {
  const context = await browser.newContext();
  const page = await context.newPage();
  await page.goto(config.origin);

  const username = `loadtest_${config.runID}_${index}`;
  const user = await time(metrics, 'signup', () =>
    page.evaluate(
      ([username]) => import('/src/lib/services/signupFlow.ts')
        .then((m) => m.performSignup(username, '')),
      [username],
    ),
  );
  registry.add({ userID: user.id, username });

  const deadline = Date.now() + config.durationMs;
  while (Date.now() < deadline) {
    const action = pickAction(config.weights);
    await runAction(action, { page, config, registry, metrics });
    await sleep(randomThink(config));
  }

  await context.close();
}
```

`runAction` dispatches to small `page.evaluate` calls per action type (each
one dynamically importing the relevant real module — `publishFlow.ts`,
`$lib/repositories/following.ts`, `$lib/services/serverConnection.ts`, the
reeds/user repositories for reads), wrapped in the same `time()` helper used
for signup so every action reports `{ action, durationMs, ok, error }`.

### Metrics (`metrics.mjs`)

In-process aggregation: for each action name, keep a running list (or a
streaming histogram, e.g. `hdr-histogram` if precision at scale matters —
plain arrays are fine at "tens to low hundreds of users" scale) of durations
plus an error counter. At the end of the run, compute p50/p95/p99 and error
rate per action and print a table; optionally write the same data as JSON to
`LOADTEST_REPORT_JSON`.

### `runner.mjs` orchestration

1. Read config from env (with the defaults above).
2. Launch one shared `chromium` instance (`headless: LOADTEST_HEADLESS`).
3. Ramp: every `1 / LOADTEST_RAMP_PER_SEC` seconds, start one more
   `runVirtualUser(...)` (fire-and-forget, tracked in a promise array) until
   `LOADTEST_USERS` have been started.
4. Await all virtual user promises (each resolves after its own
   `LOADTEST_DURATION_SEC` loop finishes and its context closes).
5. Print/write the final report; close the browser; exit.

### `npm run loadtest`

```json
"loadtest": "node loadtest/runner.mjs"
```

Typical invocation:

```bash
# terminal 1: SPA dev server pointed at the target
API_HOST=staging.example.com:443 npm run dev

# terminal 2: the load test itself
LOADTEST_USERS=50 LOADTEST_DURATION_SEC=300 npm run loadtest
```

## Work

1. `spa/loadtest/metrics.mjs` — histogram/aggregation + report printer.
2. `spa/loadtest/scenarios.mjs` — weighted picker + think-time sampler.
3. `spa/loadtest/virtualUser.mjs` — signup + action loop, using
   `performSignup`/`performPublish` from [01](01_shared_flow_helpers.md).
4. `spa/loadtest/runner.mjs` — config parsing, ramp, orchestration, report
   output.
5. `npm run loadtest` script in `spa/package.json`.
6. A short `spa/loadtest/README.md` documenting the env vars and the
   two-terminal invocation above (so this doesn't only live in `specs/`).

## Acceptance

- `LOADTEST_USERS=5 LOADTEST_DURATION_SEC=30 npm run loadtest` against a
  local `make run` backend completes without errors and prints a
  per-action latency/error report.
- Each virtual user's signup and at least one publish are visible server-side
  (new user + new reed rows) with no reimplemented signing — i.e. removing
  network access to the real target server causes the driver's actions to
  fail exactly the way the real UI would (proves no shortcut path exists).
- Aborting mid-run (Ctrl-C) does not hang — browser/contexts are closed on
  exit.
