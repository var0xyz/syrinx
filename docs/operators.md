# Operators

Run an instance, protect the server identity, and recover when the database is gone.

## Quick start (Docker)

```bash
git clone https://github.com/var0xyz/syrinx.git
cd syrinx
cp .env.example .env   # set SERVER_NAME, DB_*, ALLOWED_ORIGIN, signup policy
make run               # or: docker compose up --build
```

Stop with `make stop` / `docker compose down`.

For local development without Compose, use Go + Postgres: `make install`, create the DB, configure `.env`, `go run main.go`. The SPA is built separately under `spa/` and served as static assets in production layouts.

## Essential configuration

Copy from [`.env.example`](https://github.com/var0xyz/syrinx/blob/main/.env.example):

| Variable | Purpose |
|----------|---------|
| `SERVER_NAME` | Short instance name (no spaces) |
| `DB_*` | Postgres connection |
| `PORT` / `ALLOWED_ORIGIN` | Listen address and SPA origin |
| `SERVER_KEY_PASSPHRASE` | Optional; if unset, interactive boot uses OS keychain (auto-generate or prompt) |
| `SIGNUP_MODE` | `open` \| `invite` \| `closed`; ignored while `RECOVERY_MODE=true` (signups always blocked then) |
| `MAX_INVITES_PER_USER` | Cap (`-1` / unset = infinite) |
| `RECOVERY_MODE` | `true` only during community rebuild after identity import; overrides `SIGNUP_MODE` to block all signups |

Prefer the **OS keychain** for the server-key passphrase on single-host deploys. Use the env var when multiple replicas must unwrap the same key without a shared keychain.

## Server identity

The instance’s OpenPGP signing identity is the continuity of the community. Lose it without a bundle, and recovery from peer evidence becomes far harder.

### Export

```bash
make export-identity
# builds bin/ops and runs export-identity
```

You get an encrypted bundle (`.sxi.gpg`). Protect the **bundle password** (password manager). It is **not** stored in the keychain. The **server-key passphrase** still unwraps key armor inside; keep that too.

### Import (new host / empty DB)

```bash
make import-identity FILE=/path/to/bundle.sxi.gpg
```

Then start with `RECOVERY_MODE=true`. Boot without a prior successful import should fail closed.

Rotate the server-key passphrase with the ops CLI when needed; keychain entries update when not using the env override.

## Recovery procedure (checklist)

1. New machine, empty Postgres, `.env` with the same logical `SERVER_NAME` as appropriate.
2. Make server-key passphrase available (keychain or `SERVER_KEY_PASSPHRASE`).
3. `make import-identity FILE=…`
4. Start API with `RECOVERY_MODE=true`.
5. Tell the community: restore from backup in the app; the client will claim and report automatically when status says recovery is needed.
6. Watch unclaimed / ongoing recovery signals in logs; wait until enough peers have completed.
7. Set `RECOVERY_MODE=false` and restart when ending the window.

Details of claim, peer report, reed/follow upload, and the import gate are documented under [Identity, invites & recovery](/identity).

## Signup policy

- Launch closed or invite-only for real communities.
- Bootstrap the first accounts under `open` or documented invite exceptions, then switch to `invite` / `closed`.
- Expose nothing sensitive on `/api/server/info` beyond what the SPA needs for CTAs (mode, recovery flag, invite cap).

## Observability

The stack can export OpenTelemetry to SigNoz when configured. Observability is optional for understanding the protocol; it is not required to run a small instance.

## Security hygiene

- Treat `bin/ops` and identity bundles as crown jewels.
- Do not commit `.env` or key material.
- Keep SPA and API origins aligned (`ALLOWED_ORIGIN`) so CORS and cookie-less signed clients behave.
- After recovery, rotate operational secrets if the incident involved host compromise—not only DB restore.
