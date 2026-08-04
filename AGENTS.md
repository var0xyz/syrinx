# AGENTS.md

Orientation for AI agents (and humans) working in this repo. Read this first;
it captures the architecture, conventions, and "where things live" so you don't
have to rediscover them each session.

The canonical, human-facing docs live in [`docs/`](docs/) (published at
<https://var0xyz.github.io/syrinx/>). The forward-looking plan and per-feature
implementation steps live in [`specs/`](specs/README.md) — start there to see
what's built vs. proposed (each track table has a **Status** column).

## What Syrinx is

A distributed, P2P-*ish* content platform for closed communities. The server is
a **tracker/relay, not a content library**: it stores metadata, identity
bindings, keys, and a handful of signed records. **Reed content lives on user
devices** (IndexedDB / localStorage). Peers verify OpenPGP signatures; trust is
cryptographic, not "trust the server".

Vocabulary:

- **Reed** — a post (a signed piece of content). Bodies are held by peers; the
  server only holds metadata + the server countersignature.
- **Echo** — a repost/boost of a reed. **Reply** — a reed with a `replying` ref.
- **Pipe** — a live hashtag subscription (proposed).
- **Countersignature** — the server's detached PGP signature over a record,
  including a server-authoritative timestamp. Proves the record legitimately
  existed on this server.

## Tech stack

- **Backend:** Go (see `go.mod` for the version), `gorilla/mux` router +
  `gorilla/websocket`, `lib/pq` over **PostgreSQL**, `zerolog` logging,
  OpenPGP via `ProtonMail/go-crypto`, OpenTelemetry SDK (traces largely
  scaffolded/dead — see `observability.go` and `specs/observability/`),
  `zalando/go-keyring` for the server-key passphrase, `google.golang.org/protobuf`.
- **Frontend (`spa/`):** SvelteKit 2 + Svelte 5, TypeScript, Vite 6, static
  adapter (`adapter-static` → `spa/build`), PWA (`@vite-pwa/sveltekit` +
  workbox), `openpgp` v6 in the browser, Playwright e2e.
- **Docs (`docs/`):** a separate static docs site (its own `package.json`).

## Build / run / test

Backend (from repo root):

```bash
make build          # go build .
make run            # build + ./syrinx  (needs a .env; see below)
make test           # go test ./...
make up / make down # docker-compose
```

Operator CLI (server identity backup/restore, passphrase rotation) is a
**separate build tag** in the same `main` package:

```bash
make ops                                  # go build -tags ops -o bin/ops .
make export-identity                       # ./bin/ops export-identity
make import-identity FILE=bundle.sxi.gpg   # ./bin/ops import-identity <file>
```

`ops.go` is `//go:build ops`; `main.go` is `//go:build !ops`. The two never
compile together — the `ops` binary and the server binary are mutually
exclusive builds of the root package.

Config comes from the process environment (`tooxie/env`, `env.MustAssert`),
**not** a loaded `.env` file — copy `.env.example` to `.env` and `source` it (or
`make env`). Key vars: `DB_*`, `SERVER_NAME` (required, non-empty),
`ALLOWED_ORIGIN`, `PORT`, `SERVER_KEY_PASSPHRASE` (optional; else keychain /
prompt), `RECOVERY_MODE`, `SIGNUP_MODE` (`open|invite|closed`),
`MAX_INVITES_PER_USER` (`>=1`, or `-1`/unset = unlimited).

SPA (from `spa/`):

```bash
npm run dev            # vite dev
npm run build          # vite build → spa/build (served by the Go server)
npm run check          # svelte-check
npm run test:e2e       # Playwright
# targeted node harnesses that guard cross-language parity:
npm run test:signing        # BytesToSign parity
npm run test:verify-binary  # binary WS verify
```

The Go server serves the built SPA: `router.PathPrefix("/").Handler(spaHandler("spa/build"))`.
Run `npm run build` before expecting the Go server to serve current UI.

## Repository map

### Go server (root package `syrinx`)

Root files are the "main" package (`//go:build !ops` except `ops.go`):

- `main.go` — boot sequence + **all root route registration** + middleware
  wiring + graceful shutdown. This is the map of the HTTP surface.
- `db.go` — **`InitDB`**: the entire schema DDL (created on every boot). There
  are **no migrations** (the commented-out `MigrateDB` is intentionally off).
  Schema changes are **blank-slate**: recreate the DB, don't write `ALTER`s.
- `handlers.go` (large) — root HTTP handlers (users, reeds, keys, follows).
- `services.go` (large) — `DataService` (DB access) + business logic; also
  `Services`, `MarkdownService`. `services_test.go` alongside.
- `middlewares.go` — CORS, logging, **signature-auth** middleware, and the
  **`responseSigner`** (signs every `/api/*` response; see `RESPONSE_SIGNER.md`).
- `interfaces.go`, `constants.go`, `utils.go`, `logger.go`, `observability.go`,
  `spa_handler.go`, `ops.go`.

### Go subpackages (`syrinx/<pkg>`)

Feature logic is deliberately pushed into packages; **main only wires** boot,
DDL, routes, and middleware.

- `crypto/` — OpenPGP service: sign/verify/countersign, key add, types.
- `signing/` — **`BytesToSign`** (the canonical signed-envelope helper) and the
  normalized `user_signatures` / `server_signatures` store helpers.
  `testvectors.json` + `roundtrip_test.go` pin cross-language parity with the
  SPA. **Do not "harden" `BytesToSign` with escaping — it will break every
  existing signature.** (The rationale is documented at the top of `signing.go`
  and in `specs/README.md` → "Why nothing is escaped".)
- `identity/` — shared canonical identity/profile/reed payload builders used by
  both live traffic and recovery. Shared builders belong here, not in feature
  packages.
- `ids/` — random, server-scoped user/reed ID generation.
- `secret/` — server-key passphrase resolver (env → keychain → prompt → auto-gen).
- `recovery/` — **all** server-side DB-reconstruction (`RECOVERY_MODE`) logic:
  bundle export/import, nested key chains, claim/peer/reeds/follows handlers,
  import-gate middleware, `RegisterRoutes`. Registered **only** when
  `RECOVERY_MODE` is on.
- `invites/` — invite-only signup: mode/quota, `invites` store, lifecycle API,
  signup consume, `RegisterRoutes` + `Deps`.
- `deletion/` — signed reed/account removal store + helpers.
- `coverage/` — reed network coverage + live stats.
- `realtime/` — WebSocket service: connection manager, message types, publish-ready,
  reed subscribe, ongoing-recovery gate. Uses **binary protobuf frames** (see
  `proto/`). Has its own `README.md` (Known Issues incl. the publish/relay race).
- `proto/` — `websocket.proto` + generated `websocket.pb.go` (WS wire is
  protobuf today; HTTP is still JSON — see `specs/protobuf/`).

### Frontend (`spa/src/`)

- `routes/` — SvelteKit pages: `signup`, `import`, `recover`/`recovery`,
  `profile`, `reed/[userID]/[reedID]`, `reeds`, `feeds`, `invites`, `delete`,
  `goodbye`, `welcome`, `preamble`, `+layout.svelte` (ref prefetch), etc.
- `lib/services/` — API/client services (has its own `README.md`).
- `lib/repositories/` — IndexedDB persistence (reeds, profiles, etc.).
- `lib/verifiers/` — client-side signature verification (verify-before-store).
- `lib/crypto` helpers, `lib/stores/`, `lib/components/`, `lib/workers/`,
  `lib/utils/` (incl. `identicon.ts`, the avatar fallback).
- `scripts/` — node parity harnesses invoked by the `test:*` npm scripts.

### Specs & docs

- `specs/README.md` — the index of every proposal/track **with per-step Status**
  (Implemented / In progress / Proposed / Cancelled) and a top-of-file
  "Status at a glance" summary. Each subdir (`recovery/`, `invites/`,
  `deletion/`, `signatures/`, `conversations/`, `coverage/`, `publish/`,
  `avatars/`, `pipes/`, `account_recovery/`, `protobuf/`, `observability/`) has
  its own `README.md` with the authoritative status and locked decisions for
  that feature.
- `docs/` — canonical human docs (architecture, trust, cryptography, identity,
  deletion, invites, operators, contributors, philosophy, `planned.md`).

## Conventions & invariants

**Two recovery concepts — do not conflate:**

- **Server recovery** (`recovery/`, `RECOVERY_MODE`): operator rebuilt a wiped
  DB; clients report signed evidence *to* the server. Bookkeeping in
  `ongoing_recoveries` / `unclaimed_accounts` / `pending_follows`.
- **Account recovery** (`specs/account_recovery/`, package `syrinx/accountrecovery`
  when built): a single user reconstitutes a client from keys while the server
  still holds the account. **Never** overload `syrinx/recovery` or
  `ongoing_recoveries` for it.

**Signed-envelope / signature rules** (see `specs/README.md` → "Shared
conventions"):

- One canonical `BytesToSign` (Go) mirrored by `bytesToSign` (SPA); they MUST be
  byte-identical. Keys sorted ASCII-lexicographically; empty values omit the
  whole line; **no escaping**; timestamps RFC3339 UTC second-precision `Z`.
- Detached PGP signatures over the exact `BytesToSign` output, base64 (std
  alphabet) on the wire, never nested base64-of-base64.
- One helper called by both signer and verifier per feature (the drift bug that
  prerequisite 01 fixed).
- Clients are *supposed to* **verify before store** every signed resource
  (`lib/verifiers/`) — but see Security below: the response-signature path and a
  few verifier gaps are not fully wired yet.
- The `responseSigner` middleware signs authenticated `/api/*` responses — but
  **only when a userID is in context** (unauthenticated responses are unsigned)
  and it currently **fails open**. Do not assume "every response is signed."

**Server countersignature always carries a server-authoritative timestamp** —
newest-server-timestamp wins; revocation state is sticky. User-supplied
timestamps are never trusted for globally-contended decisions (username
squatting / revocation replay). See `specs/recovery/README.md` "Trust model".

**Blank slate everywhere.** No DB migrations, no dual-write, no backward compat.
Schema changes go in `InitDB`; recreate the DB. Callers ship in lockstep.

**Feature packaging pattern:** new server features go in their own
`syrinx/<feature>` package exposing `RegisterRoutes(api, Deps{...})`; `main.go`
wires it (see the `invites.RegisterRoutes` / `recovery.RegisterRoutes` blocks).
DDL goes in `InitDB`. Shared payload builders go in `identity/`.

## Security invariants & known gaps

A security review lives in [`RISKS.md`](RISKS.md) (severity-ranked, `file:line`,
concrete attacks + fixes). Read it before touching crypto, auth, signing,
recovery, realtime, or SPA key handling. Highlights a future agent must respect:

**Invariants you must not break:**

- **`BytesToSign` has NO escaping** and its output must stay byte-identical
  between Go (`signing/`) and SPA (`spa/src/lib/services/signing.ts`). Never add
  escaping "to be safe" — it silently breaks every existing signature. But also
  **do not build code that parses a signed envelope back into fields** from
  user-controlled bytes. (One offender already exists: `ExtractReedHeader` in
  `services.go` re-parses the reed envelope — see RISKS.md M1. Don't add more.)
- **Server countersignatures must bind identity** (reedID+authorID, or
  userID+fingerprint, + serverID + server-key fingerprint + server timestamp)
  and be verified against the server key selected **by fingerprint**. When
  adding a signed resource, bind every field a peer will later trust — notably
  the **author key fingerprint** (missing on reeds today, RISKS.md L1).
- **Reject revoked keys for new signed operations.** The auth middleware does
  this (`middlewares.go`), and `UpdateUser`/`DeleteReed`/`DeleteMe` re-check the
  payload signer isn't revoked. Recovery claim does **not** yet (RISKS.md M3) —
  don't copy recovery's key-selection as a model.
- **The server never sees or validates reed content** — only the author's
  detached signature over it. Content authenticity is a peer-side check. Don't
  write server code that assumes it can trust reed bodies.

**Known gaps (don't assume these protections exist):**

- SPA production `apiService.request()` does **not** verify the server response
  `Signature` header (the verifier is dead code) — RISKS.md C1.
- Unauthenticated HTTP responses are unsigned; response signing fails open —
  RISKS.md H2/H3.
- WebSocket handshake auth signs only a timestamp (replayable, unbound to
  user/server) and has no read-limit — RISKS.md H1/M5.
- SPA persists the key passphrase in `localStorage`, logs private-key material,
  and treats `localStorage.userId` alone as "logged in" — RISKS.md C2/C3/H4.
- Some server-provided fields are consumed unsigned (counts, `hasReeds`,
  `activeKeyFingerprint` on `/users/{id}/info`) — treat as untrusted hints —
  RISKS.md M9.

**When you change security-relevant code:** update `RISKS.md` if you fix or
introduce a finding, and add/adjust the parity tests (`signing/roundtrip_test.go`,
SPA `test:signing` / `test:verify-binary`).

## Where to look first for common tasks

- "What's the HTTP surface?" → `main.go` route block (+ `invites`/`recovery`
  `RegisterRoutes`).
- "What's in the DB?" → `db.go` `InitDB`.
- "How is X signed/verified?" → `signing/`, `crypto/`, `identity/`, and
  `lib/verifiers/` on the SPA side.
- "Is feature Y built?" → `specs/README.md` status column + `specs/Y/README.md`.
- "Realtime/WebSocket behavior" → `realtime/` (+ its `README.md`) and
  `proto/websocket.proto`.
- "How does response signing work?" → `RESPONSE_SIGNER.md` + `middlewares.go`.

## House rules for changes

- Prefer editing existing files; keep feature logic in its package, not `main`.
- Add a Go test next to the code (`*_test.go`) as the packages already do; run
  `make test`. For anything touching `BytesToSign` / wire parity, also run the
  SPA `test:signing` / `test:verify-binary` harnesses.
- Keep `specs/*/README.md` status columns accurate when you land or start a step.
- Don't add DB migrations or escaping to `BytesToSign`. Don't commit secrets;
  `SERVER_KEY_PASSPHRASE` is intentionally absent from `.env.example`.
- **Never log or persist private-key material or passphrases** (no `console.log`
  of keys, no `localStorage` passphrase). Never add a WS/postMessage signing
  path without an origin/identity check. See `RISKS.md`.
