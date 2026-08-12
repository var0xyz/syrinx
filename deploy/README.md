# Deploy

Scripts for building, deploying, and operating Syrinx on real
infrastructure. Two independent hosts, two independent script sets:

- **[`syrinx.sh`](syrinx.sh)** (scripts in
  [`scripts/syrinx/`](scripts/syrinx/README.md)) — the Syrinx application
  itself (Go backend + SvelteKit SPA). Installs on the app host, hardens it
  for internet exposure via a Cloudflare Zero Trust Tunnel (no inbound
  ports), and manages the app's lifecycle (updates, restarts, signup mode,
  DB access/wipe, the one-time root-identity mint).
- **[`telemetry.sh`](telemetry.sh)** (scripts in
  [`scripts/telemetry/`](scripts/telemetry/README.md)) — an optional
  OpenObserve + OTEL Collector stack for observability. Installs on a
  **separate, dedicated host** (traditionally a second Raspberry Pi) that
  the app host ships traces/logs/metrics to over OTLP. Syrinx runs fine
  without this; skip it entirely if you don't want the extra Pi.

Every script prompts interactively on first run and saves your answers to a
local `*.env` file (mode 600, gitignored) so re-running later only asks
about what changed. Everything sensitive — DB passwords, the app's
key-encryption passphrase, the OpenObserve admin password, Cloudflare
tunnel tokens — is either generated with `openssl rand` or typed in once
and never echoed back by default.

## Which one do I run first?

1. If you want observability, set up `telemetry.sh`/`scripts/telemetry/`
   first (it needs no dependency on the app host) and note the host/IP it
   ends up on.
2. Run `scripts/syrinx/setup.sh` on the app host (or `./syrinx.sh setup`
   from your Mac/laptop). It prompts for the domain, repo URL, and
   Cloudflare tunnel token, and — if you set up telemetry — the telemetry
   host's address, to wire up local OTLP export.

Each subdirectory's README has the full script-by-script breakdown,
including exactly how `syrinx/setup.sh` hardens the host (firewall, systemd
sandboxing, unprivileged service user, zero inbound ports) and how
credentials/secrets are handled end to end.
