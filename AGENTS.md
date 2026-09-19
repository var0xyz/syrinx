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

- **Backend (`src/backend/`):** Go (see `src/backend/go.mod` for the version),
  `gorilla/mux` router + `gorilla/websocket`, `lib/pq` over **PostgreSQL**,
  `zerolog` logging, OpenPGP via `ProtonMail/go-crypto`, OpenTelemetry SDK
  (traces largely scaffolded/dead — see `src/backend/observability/` and
  `specs/observability/`), `zalando/go-keyring` for the server-key
  passphrase, `google.golang.org/protobuf`.
- **Frontend (`src/frontend/`):** SvelteKit 2 + Svelte 5, TypeScript, Vite 6,
  static adapter (`adapter-static` → `src/frontend/build`), PWA
  (`@vite-pwa/sveltekit` + workbox), `openpgp` v6 in the browser, Playwright
  e2e.
- **Docs (`docs/`):** a separate static docs site (its own `package.json`).

Repo layout: `src/backend/` and `src/frontend/` hold all Go and SvelteKit
source respectively; everything else (`specs/`, `docs/`, `deploy/` — incl.
the ripples-cleanup cron job at `deploy/jobs/` — `scripts/`, root-level
`Makefile`/`Dockerfile*`/`docker-compose.yml`) stays at the repo root.
`cli/` (a separate `syrinx-cli` Go module — a standalone CLI tool, unrelated
to the server) also stays at repo root.

## Build / run / test

Backend (from repo root — the `Makefile` `cd`s into `src/backend/` itself):

```bash
make build          # go build -C src/backend -o ../../bin/syrinx .
make run            # build + run (binary executes with cwd=src/backend)
make test           # go test -C src/backend ./...
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

SPA (from `src/frontend/`):

```bash
npm run dev            # vite dev
npm run build          # vite build → src/frontend/build
npm run check          # svelte-check
npm run test:e2e       # Playwright
# targeted node harnesses that guard cross-language parity:
npm run test:signing        # BytesToSign parity
npm run test:verify-binary  # binary WS verify
```

The Go server serves the built SPA:
`router.PathPrefix("/").Handler(spaHandler("../frontend/build"))` — a
local-dev-only path (the binary runs with cwd=`src/backend`); production
serves the SPA via nginx directly (see `deploy/scripts/syrinx/setup.sh`),
never through this handler. Run `npm run build` before expecting the Go
server to serve current UI locally.

## Repository map

### Go server (`src/backend/`, root package `syrinx`)

**All server code lives directly in `package main`** — there are no Go
subpackages for feature logic anymore (`crypto`, `signing`, `identity`,
`roles`, `secret`, `recovery`, `invites`, `deletion`, `coverage`, and
`realtime` were each folded into root and their directories deleted; see
`specs/depackaging/README.md` for the history and rationale of that move).
The only Go code outside `package main` is `observability/`,
`observability/metrics/`, and `proto/` — kept independent because they have
a real DI interface (`metrics.Recorder`) or can't be `package main`
(generated protobuf code) respectively.

Files are the "main" package (`//go:build !ops && !ripplescleanup` unless
noted); most feature areas that used to be a subpackage now have a
same-named root file instead:

- `main.go` — boot sequence + **all root route registration** + middleware
  wiring + graceful shutdown. This is the map of the HTTP surface.
- `db.go` — **`InitDB`**: the entire schema DDL (created on every boot). There
  are **no migrations** (the commented-out `MigrateDB` is intentionally off).
  Schema changes are **blank-slate**: recreate the DB, don't write `ALTER`s.
- `handlers.go` (large) — HTTP handlers for every feature (users, reeds, keys,
  follows, invites, recovery, ripples, federation, …).
- `services.go` (large) — `DataService` (DB access) + business logic for
  every feature; also `Services`, `MarkdownService`.
- `middlewares.go` — CORS, logging, **signature-auth** middleware, and the
  **`responseSigner`** (signs every `/api/*` response; see `RESPONSE_SIGNER.md`).
- `crypto.go` — OpenPGP: sign/verify/countersign, key add (unexported
  `cryptoService`, was package `crypto`).
- `identity.go`, `identity_id.go` — canonical identity/profile/reed payload
  builders (unexported `build*Payload` funcs) and the canonical-ID type
  (unexported `identityID`, `canonicalID(serverID, userID, ...reedID)`),
  parsed back by splitting on the *last* `@`. "Canonical" means
  **everywhere**, not just the DB FK: `users.id`/`identities.id`, wire/JSON
  fields, URL path params (`@` unencoded), and every signed payload all carry
  the full `userID@serverID` form — see
  `specs/federation/SCOPE_canonical_id_everywhere.md`. Exception:
  `rootUserID = "1"` (in `constants.go`) stays a bare literal; reconstruct
  the full canonical form to compare it, never bare-string-match (prevents a
  remote user with local id "1" from being treated as root).
  **`bytesToSign` (`utils.go`)** is the canonical signed-envelope helper,
  mirrored by SPA `signing.ts`. **Do not "harden" `bytesToSign` with
  escaping — it will break every existing signature.**
  (Rationale documented at its definition and in `specs/README.md` → "Why
  nothing is escaped".)
- `roles.go` — role tiers (root/admin/user), `isRootIdentity`,
  `validateProfileRole` (was package `roles`).
- `secret.go` — server-key passphrase resolver (env → keychain → prompt →
  auto-gen; was package `secret`).
- `recovery.go` — server-side DB-reconstruction (`RECOVERY_MODE`) wire types
  and verification logic (bundle export/import, nested key chains); store
  logic lives in `services.go`, handlers in `handlers.go`. Registered
  **only** when `RECOVERY_MODE` is on (was package `recovery`).
- `realtime.go` — WebSocket service: connection manager, auth, dispatch/relay
  logic, message types; ~75 DB query methods live in `services.go` as
  `DataService` methods (was package `realtime`). Wire is **JSON text
  frames** today; a binary protobuf path exists but only covers a handful of
  message types and is unused in production.
- `constants.go`, `utils.go`, `logger.go`, `spa_handler.go`, `ops.go`,
  `ripples_cleanup.go`, `mailbox.go`, `mentions.go`, `federation_relay.go`,
  `root.go`, `wire.go`.
- `observability/`, `observability/metrics/` — the one still-independent
  subpackage with a real DI interface (`metrics.Recorder`, `Noop`/`OTEL`
  implementations).
- `proto/` — `websocket.proto` + generated `websocket.pb.go`, a partial,
  stale stub — can't be `package main` (generated code needs its own
  package), so this is the only other Go code outside root. Both HTTP and WS
  are JSON/form-encoded in production; a protobuf migration for HTTP, WS, and
  federation is spec'd but not implemented — see `specs/protobuf/`.

### Frontend (`src/frontend/src/`)

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

**Two recovery concepts — do not conflate** (user-facing overview in [`docs/identity.md`](docs/identity.md) → [Restore paths at a glance](docs/identity.md#restore-paths-at-a-glance)):

- **Server recovery** (`recovery.go`, `RECOVERY_MODE`): operator rebuilt a
  wiped DB; clients report signed evidence *to* the server. Bookkeeping in
  `ongoing_recoveries` / `unclaimed_accounts` / `pending_follows`.
- **Account recovery** (`specs/account_recovery/`; `AccountRecoveryChallenge`/
  `BootstrapAccountRecovery` in `handlers.go` — always root-level functions,
  never a separate package): a single user reconstitutes a client from keys
  while the server still holds the account. **Never** overload the server
  recovery flow or `ongoing_recoveries` for it.

**Signed-envelope / signature rules** (see `specs/README.md` → "Shared
conventions"):

- One canonical `bytesToSign` (Go, `utils.go`) mirrored by `bytesToSign`
  (SPA); they MUST be byte-identical. Keys sorted ASCII-lexicographically;
  empty values omit the whole line; **no escaping**; timestamps RFC3339 UTC
  second-precision `Z`.
- Detached PGP signatures over the exact `bytesToSign` output, base64 (std
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

**Feature organization pattern:** new server features live directly in
`package main` — no per-feature subpackage. Routes register directly in
`main.go` (`api.HandleFunc(...)`, no `RegisterRoutes`/`Deps` indirection);
handlers go in `handlers.go`, DB/business logic in `services.go`, and (for a
large enough feature) a dedicated same-named root file (see `recovery.go`,
`realtime.go`). DDL goes in `InitDB` (`db.go`). Shared payload builders go
in `identity.go`. Only reach for a real subpackage when the code is
genuinely reusable outside this server (see `observability/` for the bar).

## Security invariants & known gaps

A security review lives in [`RISKS.md`](RISKS.md) (severity-ranked, `file:line`,
concrete attacks + fixes). Read it before touching crypto, auth, signing,
recovery, realtime, or SPA key handling. Highlights a future agent must respect:

**Invariants you must not break:**

- **`bytesToSign` has NO escaping** and its output must stay byte-identical
  between Go (`utils.go`) and SPA (`src/frontend/src/lib/services/signing.ts`).
  Never add escaping "to be safe" — it silently breaks every existing
  signature. Also **do not build code that parses a signed envelope back
  into fields** from user-controlled bytes — a prior offender that did this
  (`ExtractReedHeader`) has since been removed; don't reintroduce the
  pattern.
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
introduce a finding, and add/adjust the parity tests (`handlers_signing_test.go`,
SPA `test:signing` / `test:verify-binary`).

## Where to look first for common tasks

- "What's the HTTP surface?" → `main.go` route block.
- "What's in the DB?" → `db.go` `InitDB`.
- "How is X signed/verified?" → `utils.go` (`bytesToSign`), `crypto.go`,
  `identity.go`, and `lib/verifiers/` on the SPA side.
- "Is feature Y built?" → `specs/README.md` status column + `specs/Y/README.md`.
- "Realtime/WebSocket behavior" → `realtime.go` and `proto/websocket.proto`.
- "How does response signing work?" → `RESPONSE_SIGNER.md` + `middlewares.go`.

## Debugging

**Two-instance federation dev setup.** `scripts/federation-dev-up.sh` (see also
`federation-dev-down.sh`) spins up two independent Syrinx instances (A on
`:8081`/SPA `:5174`, B on `:8082`/SPA `:5175`) in a `tmux` session
(`syrinx-federation`), sharing one `syrinx_db` postgres container but each with
its own database (`syrinx_a`, `syrinx_b`). Use this — not two ad hoc manually
launched processes — to reproduce or fix any federation bug; it builds
`bin/syrinx` once and launches both instances from it with the right env
already exported in each pane. To pick a debug build back up after editing
Go source, restart the instances *from their own tmux panes* (`tmux attach -t
syrinx-federation`, Ctrl-C + re-run `./bin/syrinx` in each `instance-a`/
`instance-b` API pane) — don't launch replacement processes elsewhere
(background `nohup`, a different terminal); the env each pane already has
(`DB_NAME`, `PORT`, `SERVER_NAME`, `SERVER_KEY_PASSPHRASE`, ...) needs to match
exactly, and relaunching outside the user's own terminal/tmux session is not
yours to do without asking.

**A `writeResponse(w, http.StatusBadRequest, ...)` branch does not always have
a matching `log.Error()` call next to it** — several exist bare in
`federation_relay.go`'s peer-relay handlers. If a federation request 400s (or
programmed with a similarly abrupt error) with *no* error-level log line
anywhere nearby in the JSON log stream, do not assume the build is stale or
logging isn't wired — grep the handler source directly for every
`StatusBadRequest`/`StatusForbidden`/`StatusUnauthorized` call in that exact
function and read each condition by hand. This has burned real debugging time
more than once.

**Ownership/ID checks in `federation_relay.go` are not symmetric across
legs — verify direction before trusting a "same guard as leg N" comment.**
Some legs run *peer → home* (the caller's own user is the subject: check
`embeddedServerID == peerServerID`); others run *home → peer* (the *callee's*
own user is the subject: check `embeddedServerID == h.services.db.GetServerID()`).
A field named `requester_user_id` or similar can belong to either side
depending on which direction that specific leg flows — read the leg's own
doc comment for who calls whom, don't pattern-match the check from a
similarly-named sibling.

## Deploy

`deploy/syrinx.sh <command>` runs the matching script under `deploy/scripts/syrinx/`
on the app host over SSH (`setup`→`setup.sh`, `update`→`update.sh`,
`wipe-db`→`wipe-db.sh`, etc. — see the header comment in `deploy/syrinx.sh` for
the full command map). It reuses the host saved in
`deploy/scripts/syrinx/deploy.env`; only `setup` may (re)establish that host.

**`setup.sh` is interactive** (confirms/collects `APP_NAME`, `APP_DOMAIN`,
`APP_REPO`, `EDGE_MODE`, Cloudflare tokens, `TELEMETRY_HOST`) but every prompt
falls back to the value already saved in `~/syrinx/setup.env` on the host when
given a blank line. So once a host has been set up once, a non-interactive
agent/CI run can re-run it by piping blank lines through:

```bash
yes '' | ./deploy/syrinx.sh setup
```

This keeps every saved value as-is. It does **not** work for a genuinely first
-time setup (no `setup.env` yet) or if you need to change a value — do those
interactively in a real terminal. `wipe-db --force` and `update`/`restart` are
already non-interactive by flag/design and need no such trick.

After `setup`, fetch the (re)minted root identity export with
`deploy/scripts/syrinx/cp-root-creds.sh` — also non-interactive once the host
is saved (it only prompts y/N about deleting the remote copy, default No).

## House rules for changes

- Prefer editing existing files. Feature logic goes in root `package main`
  (see "Feature organization pattern" above), not a new subpackage — a new
  subpackage is only warranted for code genuinely reusable outside this
  server.
- Add a Go test next to the code (`*_test.go`, same directory — Go requires
  this for package-internal tests); run `make test`. For anything touching
  `bytesToSign` / wire parity, also run the SPA `test:signing` /
  `test:verify-binary` harnesses.
- Keep `specs/*/README.md` status columns accurate when you land or start a step.
- Don't add DB migrations or escaping to `bytesToSign`. Don't commit secrets;
  `SERVER_KEY_PASSPHRASE` is intentionally absent from `.env.example`.
- **Never log or persist private-key material or passphrases** (no `console.log`
  of keys, no `localStorage` passphrase). Never add a WS/postMessage signing
  path without an origin/identity check. See `RISKS.md`.
