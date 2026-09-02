# Syrinx app-host deploy scripts

Scripts to install, run, and operate the Syrinx application (Go backend +
SvelteKit SPA) on a dedicated host — designed and tested for a Raspberry Pi 5
running Debian 13 (Trixie), but any systemd-based Debian/Ubuntu host works.

Everything environment-specific is either prompted for interactively on first
run, generated randomly (`openssl rand`), or read from `setup.env` /
`/etc/$APP_NAME/app.env`, both of which are created with restrictive
permissions and are never committed to git.

## Scripts

- **[`../../syrinx.sh`](../../syrinx.sh)** — run from your Mac/laptop, not
  the host. Copies this directory to the app host over `scp` and runs the
  requested script there via `ssh` (as root, with `sudo`), so you don't have
  to manually copy files over and log in yourself first. See "Deploying
  without a manual copy" below.
- **`cp-root-creds.sh`** — run from your Mac/laptop. Downloads the root
  identity's export file + passphrase from the host and offers to delete
  them there afterward. See "Root identity bootstrap" below.
- **`cp-client-cert.sh <name>`** — run from your Mac/laptop. Downloads a
  device's mTLS client certificate (issued via `mtls_issue_client_cert`) and
  offers to delete it from the host afterward. Only relevant when
  `EDGE_MODE=mtls`. See "Public edge: Cloudflare Tunnel vs. mTLS" below.
- **`setup.sh`** — the main installer. Idempotent: safe to re-run any time to
  repair config drift or pick up new settings. See "What setup.sh does" below.
- **`update.sh [--branch <name>]`** — pulls the latest code, rebuilds, and
  restarts. `--branch` clones that branch instead of `APP_REPO`'s default.
  Does not touch the firewall, the database, or the public edge (Cloudflare
  tunnel or mTLS material — both are repaired in place if missing/drifted,
  never reconfigured). Use this for routine deploys once `setup.sh` has run
  once.
- **`restart.sh`** — restarts the `$APP_NAME.service` systemd unit without
  rebuilding anything. Fast path for config-only changes.
- **`set-signup-mode.sh <open|invite|closed>`** — flips `SIGNUP_MODE` in
  `app.env` and restarts the service.
- **`psql.sh`** — opens an interactive `psql` shell (or runs `-c '...'`)
  against the app database, using credentials read from `app.env`. Requires
  root (the env file is `640 root:$APP_USER`).
- **`wipe-db.sh [--force]`** — backs up (`pg_dump | gzip`, mode 600) then
  drops and recreates an empty database with the same owner/grants. Requires
  typing the database name to confirm and refuses to proceed on a mismatch,
  unless `--force` is passed to skip the prompt (e.g. for non-TTY callers).
- **`root-bootstrap.sh`** — not run directly; sourced by `setup.sh`/`update.sh`
  to mint the reserved root identity (`users.id=1`) on first boot. See below.
- **`otel-agent.sh`** — not run directly; sourced by `setup.sh`/`update.sh` to
  install/remove the local telemetry shipper (`otelcol-agent`) that forwards
  this host's app traces/metrics/logs to a separate telemetry Pi. Only
  installed when a telemetry collector host is configured; fully optional.
- **`mtls.sh`** — not run directly; sourced by `setup.sh`/`update.sh` to
  generate the local CA, the nginx server certificate, and per-device client
  certificates when `EDGE_MODE=mtls`. See "Public edge: Cloudflare Tunnel vs.
  mTLS" below.
- **`ddns.sh`** — not run directly; sourced by `setup.sh`/`update.sh` to
  install a systemd timer that keeps `$APP_DOMAIN`'s DNS record pointed at
  this host's current public IP. Only installed under `EDGE_MODE=mtls`
  (Cloudflare Tunnel mode doesn't need it — DNS there points at Cloudflare,
  not this host).

## First run

```
sudo ./setup.sh
```

You'll be prompted for:

- **Application name** — used to derive the Linux user, systemd unit name,
  install paths, and database name.
- **Public domain** — the hostname the app will be served from.
- **Git repo URL** — where to `git clone --depth 1` the monorepo from on
  every build. Use an SSH URL or a plain public HTTPS URL; avoid embedding a
  token in the URL (`https://TOKEN@github.com/...`) since it will briefly be
  visible in `ps aux` output to any local user during the clone.
- **Public edge mode** — `cloudflare` (default) or `mtls`. See "Public edge:
  Cloudflare Tunnel vs. mTLS" below before choosing.
- **Cloudflare Zero Trust Tunnel token** — required when edge mode is
  `cloudflare`. See "Network exposure" below for why this is the only way in
  for that mode.
- **Cloudflare DNS API token + Zone ID** — required when edge mode is
  `mtls`, so this host can keep its domain pointed at its own public IP. See
  "Public edge: Cloudflare Tunnel vs. mTLS" below.
- **OTEL collector host** (optional) — IP/hostname of a telemetry Pi running
  the `deploy/scripts/telemetry` stack. Leave empty to disable observability export.

Answers are saved to `setup.env` (mode 600) so re-running `setup.sh` later
only asks about things that changed (press Enter to keep the saved value).

Database credentials and the server's key-encryption passphrase are
generated automatically (`openssl rand -base64 18`, alphanumeric-filtered)
and written to `/etc/$APP_NAME/app.env` (mode 640, owned by
`root:$APP_USER`) — never typed in, never logged, never echoed back.

## Deploying without a manual copy

Every script above assumes it's already sitting on the app host.
`../../syrinx.sh` is a thin wrapper that runs on your Mac/laptop instead, so
you don't have to `git clone`/`scp` this directory over and SSH in yourself
first:

```
./syrinx.sh setup                        # first-time install
./syrinx.sh update                       # routine deploy
./syrinx.sh update --branch canonicalmerge  # deploy a specific branch
./syrinx.sh restart
./syrinx.sh signup-mode invite
./syrinx.sh psql -c 'select count(*) from users;'
./syrinx.sh wipe-db
```

(run from `deploy/`, i.e. `./deploy/syrinx.sh setup` from the repo root)

Only `setup` prompts for the host address (`user@ip` or `ip`) — it's the
one command allowed to establish or change it, saved to `deploy.env` (mode
600, gitignored — same convention as
`deploy/scripts/telemetry/deploy-openobserve-pi.sh`). Every other command
(`update`, `restart`, `signup-mode`, `psql`, `wipe-db`) runs silently
against whatever's already saved — no banner, no prompt, no connectivity
check. If nothing's saved yet, or the saved host stops working, the fix is
the same: run `./syrinx.sh setup` again. It never touches `setup.env`/
`app.env` on the host — those are only ever written by `setup.sh` itself,
on the host, so redeploying can't clobber settings or secrets already
saved there.

## Public edge: Cloudflare Tunnel vs. mTLS

`setup.sh` asks which of two mutually exclusive edge modes to use — pick
whichever fits your network and threat model. Re-running `setup.sh` with a
different `EDGE_MODE` switches a host from one to the other (tears down the
previous edge's config, sets up the new one).

### `cloudflare` (default)

Described in full in "How setup.sh hardens the host for the internet"
below. A Cloudflare Zero Trust Tunnel makes an **outbound** connection from
this host to Cloudflare's edge, which terminates public TLS and proxies
requests down the tunnel. No inbound port is ever opened, so this works
behind any NAT/CGNAT/strict home router without any configuration on your
end, and the domain's DNS just points at Cloudflare — nothing to keep in
sync with this host's IP.

### `mtls`

nginx itself becomes the public edge, listening directly on `443` and
terminating TLS with a certificate this script generates. It also requires
every client to present a **client certificate** signed by a CA generated
locally on this host (`ssl_verify_client on`) — any connection without one
is rejected at the TLS handshake, before nginx evaluates a single `location`
block or proxies anything to the Go app. This is a stronger, transport-level
guarantee than an application-layer check, at the cost of more moving parts
you're responsible for:

- **Port-forwarding.** Your router must forward `443/tcp` (and `80/tcp`,
  used only for the HTTP→HTTPS redirect) to this host. Not reliably possible
  on strict/symmetric NAT or carrier-grade NAT (common on some ISPs) — check
  before choosing this mode.
- **Dynamic public IP.** Most residential ISPs rotate the public IP
  periodically. `ddns.sh` installs a systemd timer (`$APP_NAME-ddns.timer`,
  every 5 minutes) that detects IP changes and updates `$APP_DOMAIN`'s
  DNS record via the Cloudflare DNS API — this is why `mtls` mode asks for a
  Cloudflare DNS API token (`Zone:DNS:Edit` scope) and the zone's Zone ID
  even though it isn't using a Tunnel. Create the token at
  <https://dash.cloudflare.com/profile/api-tokens> → "Create Token" →
  "Edit zone DNS" template, scoped to the zone for your domain. Find the
  Zone ID on the domain's Cloudflare dashboard overview page.
- **Self-signed CA.** The server certificate and all client certificates are
  signed by one CA generated locally on this host
  (`/etc/$APP_NAME/mtls/ca.crt` + `ca.key`, the key never leaves the box) —
  there's no dependency on Let's Encrypt or port 80 being reachable from the
  public internet for issuance, but it also means the server cert isn't
  publicly trusted: any client (browser, `curl`, mobile app) needs the CA's
  public certificate installed to trust the connection at all, on top of
  needing its own client certificate to get past `ssl_verify_client`.
- **Issuing client certificates.** For each device/user that should be able
  to reach the site, run on the host:
  ```
  sudo bash -c 'source mtls.sh; mtls_issue_client_cert <name>'
  ```
  then fetch it from your laptop (never printed to the terminal or a log,
  same convention as the root-identity export):
  ```
  ./cp-client-cert.sh <name>
  ```
  This downloads `client.key`, `client.crt`, and `ca.crt` to
  `client-certs/<name>/` (gitignored). Import into a browser's certificate
  store, or test from the command line:
  ```
  curl --cert client.crt --key client.key https://<domain>/
  ```

## How setup.sh hardens the host for the internet

The zero-inbound-port model below describes **`EDGE_MODE=cloudflare`**
specifically — `mtls` mode intentionally opens `443`/`80` and makes nginx
the public edge instead; see "Public edge: Cloudflare Tunnel vs. mTLS"
above for its own (different) hardening properties. Syrinx is designed to
be exposed publicly without ever opening an inbound port when using the
default `cloudflare` mode. The hardening happens in layers:

1. **Zero inbound ports.** `ufw default deny incoming` blocks everything
   except SSH (`22/tcp`, for administration only). The app itself is never
   reachable from the internet by IP — nginx binds to `127.0.0.1:8081` only,
   not `0.0.0.0`. Public traffic reaches the box exclusively through a
   **Cloudflare Zero Trust Tunnel**, which is an *outbound* connection the
   Pi initiates to Cloudflare's edge; Cloudflare terminates TLS and proxies
   requests down the tunnel. There is nothing for a port scanner to find.

2. **Dedicated, unprivileged service user.** `useradd -r -s /bin/false
   "$APP_USER"` creates a system account with no shell and no login. The Go
   binary runs as this user, not root, and the binary itself is installed
   `chmod 500` (owner execute-only, not even readable) owned by that same
   user.

3. **Hardened systemd sandbox.** The `$APP_NAME.service` unit sets an
   extensive list of systemd sandboxing directives:
   `CapabilityBoundingSet=` (drops all Linux capabilities),
   `NoNewPrivileges=true`, `ProtectSystem=strict` + `ProtectHome=true` (the
   entire filesystem is read-only to the process except the paths it's
   explicitly granted), `PrivateTmp=true`, `PrivateDevices=true`,
   `ProtectKernelTunables=true`, `ProtectKernelModules=true`,
   `ProtectControlGroups=true`, `RestrictSUIDSGID=true`,
   `RestrictNamespaces=true`, `RestrictRealtime=true`, `LockPersonality=true`,
   `RemoveIPC=true`, `RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6`
   (blocks raw sockets and other protocol families), and a
   `SystemCallFilter=@system-service` allowlist that also explicitly denies
   dangerous syscall groups (`@clock @module @mount @obsolete @privileged
   @raw-io @reboot @swap`). Even a full remote code execution bug in the app
   would land in a process that cannot load a kernel module, mount anything,
   change the system clock, or escalate privileges.

4. **Database isolation.** PostgreSQL authenticates over SCRAM-SHA-256 (the
   script patches `pg_hba.conf` if it finds the older `md5`/trust config),
   the app connects as its own dedicated role with a random password, and
   `REVOKE CREATE ON SCHEMA public FROM PUBLIC` stops any other database
   role from creating objects in the app's schema.

5. **Secrets never touch the shell history or process list.** Passwords and
   the key-encryption passphrase are generated server-side with `openssl
   rand`, written directly into files, and never appear as command-line
   arguments to anything.

6. **Everything sensitive is filesystem-restricted.** `setup.env` (600),
   `app.env` (640, root + service group only), the deploy log (700 dir /
   600 file), the root-identity passphrase file (600, see below), and the
   `cloudflared.service` unit (600, because it embeds the tunnel token in
   `ExecStart=` and systemd units are world-readable, 644, by default) are
   all locked down explicitly rather than relying on the umask in effect at
   the time.

7. **Atomic, fail-safe deploys.** The Go binary and SPA are built into a
   scratch directory first; only after *both* builds succeed does the
   script touch the live install (`install`, `cp -r`, `systemctl restart`).
   A build failure can never leave a half-upgraded install running. Every
   run is logged to `/var/log/$APP_NAME/setup-<timestamp>.log`
   (last 20 kept) so a partial failure is diagnosable after the fact instead
   of silently invisible.

## Root identity bootstrap

Syrinx reserves `users.id=1` as a special "root" identity, minted once on
first successful start. Because the server process runs under
`ProtectSystem=strict` (no write access outside its granted paths),
`root-bootstrap.sh` temporarily grants a narrow, revocable write path via a
systemd drop-in (`ReadWritePaths=/var/lib/$APP_NAME`), starts the service
once so it can write the `.sxi.gpg` key export, waits for it to exit cleanly,
then **removes the drop-in and restarts** so the service goes back to its
fully locked-down configuration for normal operation.

The one-time import passphrase for that export file is **never printed to
the terminal** (so it can't leak via scrollback, shell history, or the
setup/update log). It's written next to the export file instead:

```
/var/lib/$APP_NAME/root-export/syrinx-1-<timestamp>.sxi.gpg             # the key export
/var/lib/$APP_NAME/root-export/syrinx-1-<timestamp>.sxi.gpg.passphrase  # the passphrase
```

Both files are mode 600, owned by `$APP_USER`. Fetch them from your
Mac/laptop instead of SSHing in and `sudo cat`-ing by hand:

```
./cp-root-creds.sh
DEPLOY_HOST=pi@10.0.0.50 ./cp-root-creds.sh
OUT_DIR=~/Desktop ./cp-root-creds.sh   # default: ./root-creds (gitignored)
```

It reads `APP_NAME` from `~/syrinx/setup.env` on the host (so run
`./syrinx.sh setup` there at least once first), finds the latest export via
`sudo find`/`sudo cat` over SSH — nothing is chmod'd or made world-readable
on the host in the process — writes both files locally at mode 600, then
prompts whether to delete the remote copies (default: no). Say yes once
you've verified both files landed safely; the passphrase is the only copy
and isn't needed for the server to keep running, but losing it with no
other copy saved means losing the ability to import that identity
elsewhere.

If a prior mint failed partway (DB row written but export file missing),
`setup.sh`/`update.sh` will refuse to proceed and print the exact
recovery command (`FORCE_ROOT_REMINT=1`, only allowed when no other users
exist yet).

## Notes

- All scripts that touch the live system require root (`sudo`).
- `FORCE_CF=1 sudo ./setup.sh` forces the Cloudflare tunnel config to be
  rewritten even if it currently looks healthy (e.g. after rotating the
  token). Only applies to `EDGE_MODE=cloudflare`.
- Consider restricting SSH (port 22) to known source IPs or a VPN/allowlist
  if your Pi has a static administrative access path — `setup.sh` leaves it
  open to any source by default since it's the only way in for initial
  administration.
- Switching `EDGE_MODE` on a live host (e.g. `cloudflare` → `mtls`) tears
  down the previous edge's config (cloudflared unit, or mTLS
  CA/certs+DDNS timer) and stands up the new one on the next `setup.sh` run
  — there's no in-between state, but expect a brief window of downtime
  while nginx reloads and the new edge comes up.
