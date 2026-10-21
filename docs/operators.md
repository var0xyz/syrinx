# Operators

Run an instance, protect the server identity, and recover when the database is gone.

## Quick start (local)

You need Go, Node.js and PostgreSQL (`docker-compose.yml` has one).

```bash
git clone https://github.com/var0xyz/syrinx.git
cd syrinx
docker compose up -d db          # or point DB_* at your own Postgres
cp .env.example .env             # set SERVER_NAME
source .env                      # config is read from the environment, not the file
ROOT_KEY_EXPORT_PASSPHRASE='…' make run   # first boot: mints the root identity and exits
make run                         # API on :8080, Ctrl-C to stop
cd src/frontend && npm install && npm run dev   # app on :5173
```

Import the root identity file the first boot wrote at `/import` to sign in as the operator. `make up` / `make down` run the same stack in Docker; set `SERVER_KEY_PASSPHRASE` first, since a container has no keychain to read it from.

## Running at home

`deploy/syrinx.sh` installs and operates Syrinx on a home server over SSH: Go backend, SPA, Postgres, nginx, firewall and systemd units. It's tested on a Raspberry Pi 5 running Debian 13; any systemd-based Debian or Ubuntu host should work. No cloud hosting or cloud know-how needed.

```bash
./deploy/syrinx.sh setup      # interactive first install; safe to re-run
./deploy/syrinx.sh update     # pull, rebuild, restart
deploy/scripts/syrinx/cp-root-creds.sh   # fetch the minted root identity
```

Both `setup` and `update` build from the official repository, [`var0xyz/syrinx`](https://github.com/var0xyz/syrinx), on its default branch. To run your own fork, or a branch other than the default, pass `--repo` and `--branch`:

```bash
./deploy/syrinx.sh update --repo https://github.com/you/syrinx.git --branch my-feature
```

Neither flag is saved, so pass them on every `setup` and `update`. An `update` without `--repo` goes back to the official repository.

`setup` asks for a **public edge mode**. Both currently depend on Cloudflare:

| Mode | How it works | Trade-off |
|------|--------------|-----------|
| `cloudflare` (default) | A Cloudflare Tunnel: an outbound connection from the host, no inbound ports, works behind any NAT | Cloudflare terminates TLS and sees all traffic metadata (not reed bodies, which are encrypted end to end) |
| `mtls` | nginx is the public edge on port 443 and requires a client certificate from every device | Needs port forwarding; each member's device needs a certificate; Cloudflare only updates the domain's DNS as your home IP changes |

Either way, Cloudflare is a point where a community can be cut off. The server key and the members' offline-first apps are what let it come back elsewhere. Full details: [`deploy/scripts/syrinx/README.md`](https://github.com/var0xyz/syrinx/blob/main/deploy/scripts/syrinx/README.md).

## Essential configuration

Copy from [`.env.example`](https://github.com/var0xyz/syrinx/blob/main/.env.example):

| Variable | Purpose |
|----------|---------|
| `SERVER_NAME` | Short instance name (no spaces) |
| `DB_*` | Postgres connection |
| `PORT` | Listen address |
| `API_BASE_URL` | This server's own public URL, used for federation |
| `ALLOWED_ORIGIN` | Required. SPA origin, also this server's frontend URL: peers send their users here, and `ops import-identity` needs it too |
| `SERVER_KEY_PASSPHRASE` | Optional; if unset, interactive boot uses OS keychain (auto-generate or prompt) |
| `SIGNUP_MODE` | `open` \| `invite` \| `closed`; ignored while `RECOVERY_MODE=true` (signups always blocked then) |
| `MAX_INVITES_PER_USER` | Cap (`-1` / unset = infinite) |
| `RECOVERY_MODE` | `true` only during community rebuild after identity import; overrides `SIGNUP_MODE` to block all signups |
| `ROOT_KEY_EXPORT_PASSPHRASE` | First boot only: mints the root operator identity, writes its export and exits; unset it afterwards |

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

Replace the server signing key itself with `ops rotate-key ["reason"]`, adding `--compromised` (with a reason) when the old key may have been stolen. Run it with the server stopped; it asks you to confirm first, because nobody can sign up, import an account or recover one until you hand out the new public key. It mints a new key, revokes the old one in its favour (both keys sign the revocation), and the server signs with the new key on its next boot. Re-export the identity bundle afterwards. The rotation is recorded in the public ledger, with its reason, as done by the server operator rather than any member; a server still running on the old key can no longer write to the ledger. After a compromise, hand the new key to users and peer admins out-of-band (`ops print-key`). Without `--compromised`, signed-in users and peer servers follow the new key on their own. With it, users see the key prompt and peers stop accepting this server until their admin approves it again with the new key.

## Recovery procedure (checklist)

1. New machine, empty Postgres, `.env` with the same logical `SERVER_NAME` as appropriate. The domain may change (say, after a seizure): point `ALLOWED_ORIGIN` and `API_BASE_URL` at the new one. The server's identity is its key, not its domain.
2. Make server-key passphrase available (keychain or `SERVER_KEY_PASSPHRASE`).
3. `make import-identity FILE=…`
4. Start API with `RECOVERY_MODE=true`.
5. Tell the community: restore from backup in the app; the client will claim and report automatically when status says recovery is needed.
6. Watch unclaimed / ongoing recovery signals in logs; wait until enough peers have completed.
7. Set `RECOVERY_MODE=false` and restart when ending the window.
8. If the domain changed, re-establish federation with your peers.

Details of claim, peer report, reed/follow upload, and the import gate are documented under [Identity, invites & recovery](/identity).

## Signup policy

- Launch closed or invite-only for real communities.
- Bootstrap the first accounts under `open` or documented invite exceptions, then switch to `invite` / `closed`.
- Expose nothing sensitive on `/api/server/info` beyond what the SPA needs for CTAs (mode, recovery flag, invite cap).

## Observability

Telemetry is optional and off by default. A small instance runs fine without it: the server still logs JSON to stdout at `LOG_LEVEL` (`warn` by default).

### What the server emits

With `OTEL_COLLECTOR_HOST` set, the server exports OpenTelemetry data to a collector at that host:

- **Traces** over OTLP gRPC on `OTEL_COLLECTOR_PORT` (default `4317`): one span per HTTP request and one per database query. Every request is sampled.
- **Metrics** over OTLP HTTP on port `4318` of the same host (this port is fixed): HTTP and database-pool stats, plus `syrinx.*` counters for users, reeds, follows, invites, keys, WebSocket traffic, federation relay, admin actions and refused requests.

| Variable | Purpose |
|----------|---------|
| `OTEL_COLLECTOR_HOST` | Collector address; unset disables telemetry entirely |
| `OTEL_COLLECTOR_PORT` | Trace (gRPC) port, default `4317` |
| `OTEL_RESOURCE_ATTRIBUTES` | Standard OTEL variable, e.g. `host.name=syrinx-01`, to tell hosts apart when they report the same hostname |

Once a collector is configured, it's treated as required. At boot the server sends a test metric, and it **refuses to start** if the collector is unreachable rather than silently dropping data. An outage later on doesn't stop the server.

### The deploy-script setup

`deploy/syrinx.sh setup` asks for an OTEL collector host. Give it one and the app host gets a local collector agent (`otelcol-agent.service`), and the server points at it on `127.0.0.1`. The agent forwards to your telemetry host:

- the server's traces and metrics
- the server's journald logs, parsed into fields
- host CPU, memory, disk and network metrics

To turn it off later, re-run `setup` and answer `off`. A blank answer keeps the current setting. The telemetry host itself is a separate machine running OpenObserve with a bundled "Syrinx" dashboard, installed with `./deploy/telemetry.sh setup` and refreshed with `./deploy/telemetry.sh update`. On a Raspberry Pi, OpenObserve must be cross-compiled on another machine (macOS, Debian or Arch, with ~24 GB of RAM + swap; about an hour), and `telemetry.sh setup` offers to do it. Upstream's `arm64` binaries crash on the Pi's CPU, which lacks AES instructions, and building on the Pi itself isn't supported: on a Pi 4 it used up all 9.7 GB of RAM + swap after four hours, before the largest part was done. Full details, including reaching a LAN-only UI over SSH: [`deploy/scripts/telemetry/README.md`](https://github.com/var0xyz/syrinx/blob/main/deploy/scripts/telemetry/README.md).

### Privacy

Treat the telemetry host as holding member data. Metrics and logs carry raw user, reed and key IDs. They also record who follows, blocks, invites and vouches for whom, so together they map the community's social graph. Reed content is never included.

- Keep the collector on your LAN. Its OTLP ports have no authentication, so the telemetry host's firewall opens them only to the LAN ranges you configure.
- If you expose the OpenObserve UI through a Cloudflare Tunnel, put a Cloudflare Access policy in front of the login page.
- Back up, retain and delete telemetry with the same care as the database.

## Security hygiene

- Treat `bin/ops` and identity bundles as crown jewels.
- Do not commit `.env` or key material.
- Keep SPA and API origins aligned (`ALLOWED_ORIGIN`) so CORS and cookie-less signed clients behave.
- After recovery, rotate operational secrets if the incident involved host compromise—not only DB restore.
