# Contributors

This site is the **canonical source of truth** for design intent. When you change behavior, update the relevant page here in the same effort as the code.

## Repository layout

| Path | Responsibility |
|------|----------------|
| Root Go module | HTTP API, middleware, DB init, wiring |
| `realtime/` | WebSocket service, fanout, protobuf events |
| `spa/` | SvelteKit PWA client |
| `identity/` | Shared identity payload builders |
| `crypto/`, `signing/`, `keys/`, `secret/` | Crypto primitives and key handling |
| `recovery/` | Server-side recovery mode (handlers, gates, stores) |
| `invites/` | Invite lifecycle |
| `deletion/` | Removal certificates and related API |
| `cli/` | Separate Bubble Tea CLI module (optional tooling) |
| `docs/` | This VitePress documentation site |
| `proto/` | Realtime protobuf definitions |

Feature packages own their domain logic. `main` wires config, routes, and middleware—it should not accumulate recovery/invite/deletion business rules.

## Local development

- `make run` / Compose for the full stack, or `dev.sh` for a tmux-oriented setup if you use it.
- SPA: Node 20+, install under `spa/`, `pnpm dev` / `npm run dev`.
- Docs: under `docs/`, `npm install && npm run dev`.

Project `.gitignore` is intentionally narrow (project artifacts only). Put personal editor ignores in `.git/info/exclude`.

## Design culture

- **Verify before trust** — clients check user and server signatures; don’t add server-only “trust me” paths for sensitive mutations.
- **Canonical bytes** — signing goes through shared `BytesToSign` helpers; never “almost the same” serialization on one side.
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
