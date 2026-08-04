# Load testing — real browsers, script-driven

Syrinx has no synthetic-request load test: every authenticated call needs a
real PGP signature over `signing.BytesToSign` bytes, signup is a multi-step
signed handshake, and the WS wire is an authenticated, framed protocol. A
second implementation of that signing/framing logic in a generic load tool
(k6, Locust, Artillery, a bespoke Go client) would be a parity risk the repo
already warns about for `BytesToSign` — "one helper... so the two cannot
drift."

Instead: drive the **real SPA** in **real browsers**. Many isolated
Playwright `BrowserContext`s (one per virtual user, like one device each)
load the actual, unbundled SPA from Vite's dev server — which already proxies
`/api` and `/ws` to any target host via `API_HOST`
([spa/vite.config.ts](../../spa/vite.config.ts)) — and call the app's real
service/repository functions directly in-page (`page.evaluate` + dynamic
`import('$lib/...')`), the same trick [spa/tests/signup-and-publish.spec.ts](../../spa/tests/signup-and-publish.spec.ts)
already uses. No signing/framing code is reimplemented; the load test is
just many real clients, run headless, driven by script instead of clicks.

| # | Title | Depends on | Status |
|---|-------|------------|--------|
| [00](00_design.md) | Design + `API_HOST` proxy trick + locked model | — | Proposed |
| [01](01_shared_flow_helpers.md) | Extract `performSignup` / `performPublish` into reusable SPA services | 00 | Proposed |
| [02](02_driver.md) | Playwright driver: virtual users, scenario mix, config, npm script | 00, 01 | Proposed |
| [03](03_fanout_metrics.md) | Publish → delivery fanout-latency correlation | 02 | Proposed |

## Locked decisions

| Topic | Decision |
|-------|----------|
| Client | Real browsers (Playwright), not a synthetic protocol client |
| Interaction style | **Script-driven**: call real SPA functions in-page via `page.evaluate` + dynamic import; no click/type simulation |
| Target | Vite dev server (`API_HOST=<target> npm run dev`) proxying to a staging/production-like server — dev mode is required so `page.evaluate` can `import()` unbundled source |
| Scale | Single host, many `BrowserContext`s sharing a few browser processes (tens–low hundreds of concurrent virtual users), not goroutine-scale |
| Data lifecycle | No cleanup sweep — accounts/reeds created by a run are left in place; run-scoped usernames/tags make them recognizable |
| Signup gating | Target is assumed open-signup; invite provisioning is out of scope |
| Peer relay | Simulated "for free" — each virtual user is a real loaded SPA instance holding reeds in IndexedDB and answering `REQUEST_REED`/`RELAY_RESPONSE` like any real peer |

## Status

**Proposed.** Nothing in this directory has been implemented yet.
