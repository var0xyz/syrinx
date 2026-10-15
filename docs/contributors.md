# Contributors

These pages explain how Syrinx works. Design proposals and per-feature status live in [`specs/`](https://github.com/var0xyz/syrinx/tree/main/specs); start with [`AGENTS.md`](https://github.com/var0xyz/syrinx/blob/main/AGENTS.md) for orientation. If you change behavior a page describes, please update it too.

## Repository layout

| Path | Responsibility |
|------|----------------|
| `src/backend/` | Go module — HTTP API, middleware, DB init, WebSocket service, all feature logic (in `package main` directly — see below) |
| `src/backend/observability/`, `src/backend/observability/metrics/` | Business-metrics recorder DI interface — the one feature area still a real subpackage |
| `src/backend/proto/` | Protobuf definitions for every HTTP and WebSocket message (generated code needs its own package) |
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

- `make run` / Compose for the full stack (see [Operators](/operators#quick-start-local)), or `scripts/dev.sh` for a tmux-oriented setup if you use it.
- `scripts/federation-dev-up.sh` runs two federated instances side by side for cross-server work.
- SPA: install under `src/frontend/`, `npm run dev`.
- Docs: under `docs/`, `npm install && npm run dev`.

Project `.gitignore` is intentionally narrow (project artifacts only). Put personal editor ignores in `.git/info/exclude`.

## Design culture

- **Verify before trust** — clients check user and server signatures; don’t add server-only “trust me” paths for sensitive mutations.
- **Canonical bytes** — signed payloads go through the shared `canonicalJSON` helper (RFC 8785 JCS) via one `build*Payload` per record; never “almost the same” serialization on one side.
- **Blank-slate schema** — this project often prefers recreate-DB cutovers over long dual-write migrations while it is still early. Say so in the PR if you change schema.
- **Idempotent certificates** — removals and similar attestations should replay safely.
- **Offline-first where it matters** — author queues for publishes/removals/revocations; sync is not “hope the tab stayed open.”

## Documentation workflow

1. Read the pages under *How it works* for the subsystem you touch.
2. Implement the change.
3. Update `docs/*.md` so the site still matches reality.
4. Run `npm run build` in `docs/` if you changed structure or config.

CI builds VitePress from `docs/` on `main` and deploys the artifact. In repo Settings → Pages, set **Source: GitHub Actions**. Do not use “Deploy from a branch” with `main` / `docs` — that serves raw markdown through Jekyll instead of the built site.

## Voice

Prefer direct commit-message honesty: state the trade-off, the attack you raised the cost of, and what you are *not* solving. Avoid engagement-product language and false guarantees about deletion or anonymity.
