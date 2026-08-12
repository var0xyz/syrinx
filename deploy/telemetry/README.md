# Telemetry host deploy scripts

Scripts to install and operate an [OpenObserve](https://openobserve.ai/) +
OpenTelemetry Collector stack on a **dedicated telemetry host**, separate
from the Syrinx app host(s). App hosts (via `deploy/syrinx/otel-agent.sh`)
ship traces, logs, and metrics here over OTLP; this host stores and
visualizes them.

Everything environment-specific is prompted for interactively,
generated randomly, or read from `telemetry.env` / `deploy.env`, both of
which are created with restrictive permissions and are never committed to
git (`deploy.env.example` is the only tracked template, and it contains no
real values).

## Scripts

- **`deploy.sh`** — run from your Mac/laptop, not the telemetry host. Copies
  `setup.sh`, `update.sh`, `build-openobserve-pi.sh`, `common.sh`, and
  `dashboards/` to the host over `scp` and runs the requested one via `ssh`
  (as root, with `sudo`); `deploy.sh tunnel` runs `tunnel.sh` locally
  instead. See "Deploying without a manual copy" below.
- **`setup.sh`** — the main installer for the telemetry host itself. Prompts
  for hostname, OpenObserve admin credentials, optional public domain, and
  which LAN ranges may reach it, then installs OpenObserve + the OTEL
  Collector as systemd services. Idempotent — safe to re-run.
- **`update.sh`** — re-detects the CPU architecture and refreshes the
  OpenObserve + OTEL Collector binaries in place, re-applies the firewall
  rules, and restarts both services. Warns (without stopping) if the live
  "Syrinx" dashboard has drifted from the bundled JSON before overwriting
  it — see "Dashboard drift" below. Does not touch the Cloudflare tunnel.
- **`common.sh`** — not run directly; shared functions sourced by every
  other script in this directory (architecture detection, binary install,
  systemd unit generation, firewall rules, dashboard provisioning,
  Cloudflare Tunnel setup).
- **`tunnel.sh`** — run from your Mac/laptop. Opens an SSH local-port-forward
  to a LAN-only telemetry host's OpenObserve UI, for when no Cloudflare
  Tunnel is configured (`TELEMETRY_DOMAIN` left empty during `setup.sh`).
  See "Reaching a LAN-only UI" below.
- **`build-openobserve-pi.sh`** — builds OpenObserve from source *directly
  on the Pi* (wrapped in `tmux` so it survives an SSH disconnect — this can
  take 1–3+ hours on a Pi 4). Normally invoked automatically by
  `setup.sh`/`update.sh`; only needed directly for troubleshooting.
- **`cross-compile-openobserve-pi.sh`** — the **preferred** build path: runs
  on a Mac, cross-compiles OpenObserve for `aarch64-unknown-linux-gnu` using
  Zig (`cargo zigbuild`), and optionally deploys the result straight to a Pi
  via `DEPLOY_HOST=`. Minutes instead of hours.
- **`deploy-openobserve-pi.sh`** — copies a Mac-cross-compiled binary to a
  Pi over `scp`/`ssh`, installs it, and wires up (or repairs) the
  `openobserve` and `otelcol-contrib` systemd units. Prompts for the Pi's
  address and remembers it in `deploy.env` (gitignored).
- **`dashboards/generate_syrinx_dashboard.py`** — regenerates
  `dashboards/syrinx-app.dashboard.json` (the bundled "Syrinx" OpenObserve
  dashboard: HTTP golden signals, DB pool/query stats, user/reed business
  metrics, WebSocket message volume). Re-run this after adding a new
  `syrinx.*` metric or trace attribute; `setup.sh`/`update.sh` import or
  update the dashboard from this file automatically.

## Why a Pi needs a special build

Raspberry Pi CPUs lack the AES hardware instruction that upstream
OpenObserve's prebuilt `arm64` binaries require, so they `SIGILL` on start
([openobserve#3910](https://github.com/openobserve/openobserve/issues/3910)).
`common.sh` patches around this (`target-feature=+neon` instead of
`+aes,+neon`, no `gxhash` SIMD) before building, and every install path runs
a quick smoke test (spin the binary up against a throwaway localhost
instance) before trusting it. Cross-compiling on a Mac (`cross-compile-*`)
is faster; building on-Pi (`build-openobserve-pi.sh`) is the fallback when
no Mac is available.

## First run

```
sudo ./setup.sh
```

You'll be prompted for:

- **Telemetry hostname** — what app hosts will use to reach this box.
- **OpenObserve admin email/password** — leave the password blank to have
  one generated (`openssl rand -base64 18`).
- **Public domain** (optional) — exposes the OpenObserve UI at
  `https://<domain>` via an outbound-only Cloudflare Zero Trust Tunnel, the
  same no-inbound-port pattern the Syrinx app host uses. Leave empty to keep
  this host LAN-only.
- **Cloudflare Zero Trust Tunnel token** — only asked if a public domain was
  given.
- **LAN CIDRs** allowed to reach OTLP (`4317`/`4318`) and the OpenObserve UI
  (`5080`) — defaults to all RFC1918 private ranges
  (`192.168.0.0/16 10.0.0.0/8 172.16.0.0/12`); narrow this to your actual
  subnet if you want to be stricter.

Answers persist to `telemetry.env` (mode 600). The generated OpenObserve
login is printed once at the end of `setup.sh`/`update.sh` — by default the
**password is hidden** in that output (`Login: admin@... / (hidden — rerun
with SHOW_PASSWORD=1 to print it)`) since terminal scrollback, tmux
history, and CI logs are not a safe place for it by default; pass
`SHOW_PASSWORD=1` when you actually need to see it (e.g.
`sudo SHOW_PASSWORD=1 ./setup.sh`), or read it straight from
`telemetry.env` (600) or `/etc/openobserve/openobserve.env` (600) on the
box.

## Deploying without a manual copy

`setup.sh`/`update.sh`/`build-openobserve-pi.sh` assume they're already
sitting on the telemetry host. `deploy.sh` runs on your Mac/laptop instead:

```
./deploy.sh setup                  # first-time install
./deploy.sh update                 # routine update
./deploy.sh build                  # on-Pi source build (troubleshooting)
SHOW_PASSWORD=1 ./deploy.sh setup  # also print the OpenObserve admin password
./deploy.sh tunnel                 # SSH port-forward to the UI (see below)
```

It prompts once for the host address, saves it to `deploy.env` (mode 600,
gitignored — shared with `deploy-openobserve-pi.sh` and `tunnel.sh`), copies
the files each script needs to `~/telemetry` on the host over `scp`,
then `ssh`es in and runs the requested script with `sudo`. It never touches
`telemetry.env` on the host — that's only ever written by `setup.sh` itself,
on the host, so redeploying can't clobber settings already saved there.

Env vars set locally aren't visible over `ssh` on their own, so `deploy.sh`
forwards `SHOW_PASSWORD` explicitly into the remote `sudo` invocation —
setting it before `./deploy.sh setup`/`update` has the same effect as
setting it before `sudo ./setup.sh` directly on the host.

`deploy.sh tunnel` is a shortcut for `tunnel.sh` — it runs entirely locally
(nothing to `scp` or execute remotely), so it skips the copy/ssh-to-script
flow above and just execs `tunnel.sh` directly, sharing the same
`deploy.env`.

`cross-compile-openobserve-pi.sh` and `deploy-openobserve-pi.sh` already run
from the Mac and do their own `ssh`/`scp` — use those directly rather than
through `deploy.sh`.

## Network exposure model

Same philosophy as the Syrinx app host:

- UFW default-denies all inbound traffic. OTLP ports and the UI port are
  opened **only** to the configured LAN CIDRs — never `0.0.0.0/0`. If
  `TELEMETRY_LAN_CIDRS` is left empty, those ports stay effectively
  localhost-only.
- The only way to reach the UI from outside the LAN is the optional
  Cloudflare Tunnel, which is an outbound connection this host initiates —
  no inbound port is ever opened for it. Enabling a public domain still
  leaves the OpenObserve login page internet-reachable (gated by the admin
  password); `setup.sh` prints a reminder to add a Cloudflare Access policy
  in the Zero Trust dashboard if you do this.
- `otelcol-contrib`'s OTLP receiver has no authentication of its own — it's
  a bridge, not a public API — so it must stay behind the LAN-CIDR firewall
  rule. Don't widen `TELEMETRY_LAN_CIDRS` to `0.0.0.0/0`.

### Reaching a LAN-only UI

If you left the public domain empty (no Cloudflare Tunnel), the OpenObserve
UI is only reachable from the configured LAN CIDRs. To view it from off the
LAN without opening a tunnel, forward it over SSH instead:

```
./tunnel.sh                          # prompts for the host, same as deploy-openobserve-pi.sh
./deploy.sh tunnel                   # equivalent — deploy.sh execs tunnel.sh directly
DEPLOY_HOST=pi@10.0.0.50 ./tunnel.sh
LOCAL_PORT=15080 ./tunnel.sh         # forward a different local port
```

Opens `http://127.0.0.1:5080` (or `$LOCAL_PORT`) tunneled to the host's
`:5080` and blocks in the foreground until you `Ctrl-C`. Shares the saved
host in `deploy.env` with `deploy-openobserve-pi.sh` and `deploy.sh`.

## Credential handling

- The OpenObserve admin email/password are written to
  `/etc/openobserve/openobserve.env` (mode 600, consumed by systemd's
  `EnvironmentFile=`).
- The OTEL Collector needs those same credentials (as an HTTP Basic-auth
  header) to push data into OpenObserve. That header is embedded in
  `/etc/otelcol/config.yaml`, which is written **mode 600** — base64 is not
  encryption, so this file is treated as a secret, not a plain config file.
- `cloudflared.service` embeds the tunnel token directly in its
  `ExecStart=` line (that's how token-managed Cloudflare tunnels work).
  Since files under `/etc/systemd/system/` are world-readable (644) by
  default, this unit is explicitly `chmod 600` after being written rather
  than relying on whatever umask happened to be active.
- `deploy-openobserve-pi.sh` can print the admin login it finds on the
  remote Pi at the end of a deploy; like `setup.sh`/`update.sh`, the
  password is hidden by default — pass `--show-password` to show it.

## Typical workflows

**Mac → Pi (recommended, fast):**
```
./cross-compile-openobserve-pi.sh
./deploy-openobserve-pi.sh          # prompts for the Pi's address
```
or in one shot:
```
DEPLOY_HOST=pi@10.0.0.50 ./cross-compile-openobserve-pi.sh
```

**On the Pi directly (no Mac available):**
```
sudo ./setup.sh          # first time
sudo ./update.sh         # subsequent updates
```

**Regenerating the bundled Syrinx dashboard after adding a metric:**
```
python3 dashboards/generate_syrinx_dashboard.py
sudo ./update.sh          # re-imports the updated JSON
```

## Dashboard drift

`update.sh` always overwrites the live "Syrinx" dashboard with
`dashboards/syrinx-app.dashboard.json` (that's how it picks up new
tabs/panels after you regenerate it). Before doing that, it fetches the live
dashboard from OpenObserve and compares it against the bundled JSON — if
someone edited a panel by hand in the UI since the last update, that edit is
about to be silently clobbered. If they differ (ignoring server-only fields
like `dashboard_id`, `hash`, `owner`, `created`), `update.sh` prints a
warning naming the drift and continues; it never blocks the run. If you want
to keep a manual UI edit, save it (e.g. duplicate the panel, or note the
change) before running `update.sh` again — or fold it into
`dashboards/generate_syrinx_dashboard.py` so it survives future syncs
instead of only living in the UI.
