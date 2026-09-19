# Contributors

This site is the **canonical source of truth** for design intent. When you change behavior, update the relevant page here in the same effort as the code.

## Repository layout

| Path | Responsibility |
|------|----------------|
| `src/backend/` | Go module — HTTP API, middleware, DB init, WebSocket service, all feature logic (in `package main` directly — see below) |
| `src/backend/observability/`, `src/backend/observability/metrics/` | Business-metrics recorder DI interface — the one feature area still a real subpackage |
| `src/backend/proto/` | WebSocket protobuf definitions (generated code needs its own package) |
| `src/frontend/` | SvelteKit PWA client |
| `cli/` | Separate Go module, standalone CLI tool (optional tooling) — not part of the server module |
| `docs/` | This VitePress documentation site |
| `deploy/` (incl. `deploy/jobs/`), `scripts/` | Deploy automation, cron job definitions, dev-environment scripts |

Feature logic lives directly in root `package main` under `src/backend/`
(`handlers.go`, `services.go`, and dedicated same-named files like
`recovery.go`/`realtime.go` for larger features) — not in per-feature
subpackages. `main.go` wires config, routes, and middleware for every
feature; there's no `RegisterRoutes`/`Deps` indirection to route around.
Reach for a real subpackage only when code is genuinely reusable outside
this server, matching `observability/`'s bar.

## Local development

- `make run` / Compose for the full stack, or `dev.sh` for a tmux-oriented setup if you use it.
- SPA: Node 20+, install under `src/frontend/`, `pnpm dev` / `npm run dev`.
- Docs: under `docs/`, `npm install && npm run dev`.

Project `.gitignore` is intentionally narrow (project artifacts only). Put personal editor ignores in `.git/info/exclude`.

## Design culture

- **Verify before trust** — clients check user and server signatures; don’t add server-only “trust me” paths for sensitive mutations.
- **Canonical bytes** — signing goes through the shared `bytesToSign` helper; never “almost the same” serialization on one side.
- **Blank-slate schema** — this project often prefers recreate-DB cutovers over long dual-write migrations while it is still early. Say so in the PR if you change schema.
- **Idempotent certificates** — removals and similar attestations should replay safely.
- **Offline-first where it matters** — author queues for publishes/removals/revocations; sync is not “hope the tab stayed open.”

## Documentation workflow

1. Read the pages under *How it works* for the subsystem you touch.
2. Implement in the owning package.
3. Update `docs/*.md` so the site still matches reality.
4. Run `npm run build` in `docs/` if you changed structure or config.

CI builds VitePress from `docs/` on `main` and deploys the artifact. In repo Settings → Pages, set **Source: GitHub Actions**. Do not use “Deploy from a branch” with `main` / `docs` — that serves raw markdown through Jekyll instead of the built site.

Do not reintroduce a parallel “design folder” that drifts from this site.

## Voice

Prefer direct commit-message honesty: state the trade-off, the attack you raised the cost of, and what you are *not* solving. Avoid engagement-product language and false guarantees about deletion or anonymity.
