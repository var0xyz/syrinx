# HA 11 — Deploy scripts: topology, per-role setup, rolling update

## Status

Proposed.

## Depends on

[09](09_load_balancer.md), [10](10_database_host.md)

## Context

`deploy/syrinx.sh <command>` copies `deploy/scripts/syrinx/` to one saved
host and runs the matching script there with sudo. `setup.sh` does
everything on that host: packages, Postgres, the Go build, the SPA build,
nginx, systemd units, cron jobs, `cloudflared` or mTLS, and the OTEL agent.
Only `setup` may set or change the saved host (`deploy.env`).

## Topology file

`deploy.env` (mode 600, gitignored) grows from one host to a topology:

```sh
TOPOLOGY=ha                      # single | ha
LB_HOST=alvaro@192.168.0.20
DB_HOST=alvaro@192.168.0.21
APP_HOSTS="alvaro@192.168.0.22 alvaro@192.168.0.23 alvaro@192.168.0.24"
```

`TOPOLOGY=single` keeps today's `DEPLOY_HOST` line and nothing else. Only
`setup` writes this file, asking for the topology first and then the hosts
it needs. Every other command reads it silently, as today.

## Per-role scripts

`setup.sh` is split into role scripts that share `common.sh`. Single mode
runs all three on one host, which reproduces today's install exactly.

| Script | Runs on | Does |
|---|---|---|
| `setup-db.sh` | DB host (single: the host) | Postgres, role, database, `pg_hba` for the given app IPs, tuning drop-in, dump timer, firewall |
| `setup-app.sh` | each app host (single: the host) | App user, env file, systemd unit, cron jobs, OTEL agent, firewall. Receives the binary and env; does not build |
| `setup-lb.sh` | LB host (single: the host) | nginx (upstream from `APP_HOSTS`, or `127.0.0.1:8080` in single), the gateway (HA only), SPA release dirs, `cloudflared` or mTLS, OTEL agent, firewall |
| `build.sh` | first app host (single: the host) | Clone/pull the repo, build the Go binary and cleanup jobs, build the SPA. Leaves artifacts in a release dir |

`syrinx.sh setup` in HA orchestrates, from the operator's machine:

1. `setup-db.sh` on `DB_HOST` with the list of app IPs.
2. `build.sh` on the first app host.
3. Copy artifacts **through the operator's machine** (`scp` from app 1,
   then to the others): the Go binary and jobs to every app host, the SPA
   build and the gateway binary to the LB. App hosts never need SSH access
   to each other.
4. Generate the shared secrets once on the first run (DB password,
   `SERVER_KEY_PASSPHRASE`, `GATEWAY_TOKEN`) and write the same values into every app
   host's env file (and the token into the gateway's) over SSH. They pass through the operator machine in
   memory only, never to its disk, and are read back from app 1 on later
   runs (a blank answer keeps saved values, as today).
5. `setup-app.sh` on every app host with `REPLICA_MODE=ha`,
   `REPLICA_ID=<host short name>`, `DB_HOST=<DB LAN IP>`,
   `GATEWAY_ADDR=<LB LAN IP>:8071`.
6. First install only: start app 1 alone with `ROOT_KEY_EXPORT_PASSPHRASE`
   to mint the root identity, exactly as today; then
   `syrinx.sh root-creds <dir>` pulls it from app 1.
7. Start the app hosts one at a time, waiting for each `/api/health`.
8. `setup-lb.sh` on `LB_HOST`: nginx, then the gateway, which resets
   realtime state on its first start; then the Cloudflare route check or
   mTLS as today.
9. Print the report: one line per host and role, plus a line per app host
   showing its gateway link is up.

Building on one app host and copying keeps one build per update, instead
of one per Pi; the build already fits on a Pi, so no cross-compilation is
needed.

## Commands in HA

| Command | Single | HA |
|---|---|---|
| `setup` | As today | Steps above |
| `update` | As today | `build.sh` on app 1, ship SPA to the LB (new release dir, old `_app/` kept in `shared-app/`), then **rolling**: for each app host, install binary, restart (the replica drains to the others first, so nobody reconnects), wait for `/api/health` and its gateway link, then the next. Abort on the first host that doesn't come back; the rest keep the old version. The gateway is updated last, and only when its binary changed, since restarting it reconnects everyone |
| `restart` | As today | Rolling, same wait |
| `signup-mode` | As today | Edit every app host's env, rolling restart |
| `psql` | As today | On `DB_HOST` |
| `wipe-db` | As today | Stop the gateway and all app hosts, wipe on `DB_HOST`, start the app hosts one by one, then the gateway ([10](10_database_host.md)) |
| `server-key` | As today | On app 1 |
| `rotate-key` (ops) | Stop, rotate, start | Stop **all** app hosts, rotate on app 1, start all. The script checks each app host's service is stopped first, so no replica keeps signing with the old key |

Old and new binaries run side by side during a rolling update. Blank slate
still applies to the schema (no migrations), so an update that changes the
schema or the wire (including `gateway.proto`) is a **full restart**
instead: stop all, start all. The
update script detects it by comparing the embedded schema hash
(`InitDB`'s DDL, hashed at build time) between the running and the new
binary, and says which kind of update it is doing.

## Telemetry

Every host gets the OTEL agent with its own `host.name`; app hosts add
`service.instance.id=$REPLICA_ID`, and the LB's agent also receives the
gateway's traces and metrics. `telemetry.sh` is unchanged.

## Docs

`docs/operators.md` gets a "Running several replicas" section: the
topology, what HA does and doesn't protect (app tier only), the commands
table above, and the connection budget. `deploy/scripts/syrinx/README.md`
documents the role scripts.

## Tests

Script-level, against VMs or Pis:

- Fresh HA setup with 1 LB, 1 DB, 2 app hosts reaches a working site.
- `update` with an app host powered off aborts after that host and leaves
  the site serving.
- Re-running `setup` with blank answers changes nothing.
- Single-topology `setup` and `update` produce the same files as before
  the split (diff the generated nginx config, units and env).
