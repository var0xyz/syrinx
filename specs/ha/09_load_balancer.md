# HA 09 — Load balancer host: nginx and the gateway

## Status

Proposed.

## Depends on

[05](05_gateway.md)

## Context

In single mode nginx on the app host serves the SPA, proxies `/api/` and
`/ws/` to `127.0.0.1:8080`, strips client-IP headers, and is the edge
(behind `cloudflared`, or on :443 with mTLS). In HA all of that moves to
the LB Pi, plus the gateway.

## nginx

```nginx
upstream syrinx_app {
    least_conn;
    server 10.0.0.21:8080 max_fails=2 fail_timeout=10s;
    server 10.0.0.22:8080 max_fails=2 fail_timeout=10s;
    keepalive 32;
}
```

- `/api/` → `syrinx_app`, with today's headers, including the
  IP-stripping block (`NO_CLIENT_IP` in `setup.sh`), so the privacy
  guarantees in `docs/operators.md` hold unchanged. Plus
  `proxy_set_header Connection ""` for upstream keepalive.
- `proxy_next_upstream error timeout http_502 http_503` on `/api/`, which
  nginx applies to idempotent methods only. A `POST` that failed
  mid-flight isn't retried by the LB; signed clients retry on their own.
- `/ws/` → `127.0.0.1:8070` (the gateway), with `Upgrade` and the 24 h
  read timeout as today, and the same IP stripping. The gateway never sees
  a client address either.
- `/internal/` is **not** proxied: it's only reachable on the LAN, from
  the gateway.
- The SPA (`/`, `/_app/`, the `shared-app/` fallback, the
  `service-worker.js` no-cache rule) is served from the LB's disk, exactly
  as `setup.sh` configures it today. App hosts don't run nginx.

Health: passive (`max_fails`) is enough for `/api`. Replicas that are
stopping return 503 from `/api/health` and are skipped. The gateway tracks
replica health itself over its links.

## Gateway service

`syrinx-gateway.service`, sandboxed like the app's unit
(`DynamicUser`, `ProtectSystem=strict`, no write paths), restarted on
failure. Its env file holds `GATEWAY_*` ([05](05_gateway.md)).

## Edge

- **Cloudflare mode**: `cloudflared` runs on the LB, pointed at nginx.
- **mTLS mode**: nginx on the LB terminates TLS on :443 and checks client
  certificates; `mtls.sh`, `ddns.sh` and `cp-client-cert.sh` target the LB.

## Firewall

| Host | Inbound allowed |
|---|---|
| LB | 443 (mtls mode only); 8071 (gateway internal) from app hosts; 22 from LAN |
| App | 8080 from the LB only; 22 from LAN |
| DB | 5432 from app hosts only; 22 from LAN |

## Tests

- Kill one app host: `/api` keeps succeeding; sockets stay open.
- `curl` through the LB with `X-Forwarded-For` and `Cf-Connecting-Ip`
  set: neither the app's request span nor the gateway sees an address.
