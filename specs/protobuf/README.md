# Protobuf wire — migrate all client↔server communications

Syrinx’s HTTP API and WebSocket channel speak **Protocol Buffers** end to
end. Shared resource messages (`User`, `Reed`, signature blocks, certs) are
defined once under `proto/` and generated for Go and the SPA.

| #  | Title | Depends on |
|----|-------|------------|
| [00](00_design.md) | Design + locked model | — |
| [01](01_shared_messages.md) | Shared resource protos + codegen | 00 |
| [02](02_websocket_schema.md) | WebSocket envelope + event protos | 01 |
| [03](03_http_codec.md) | HTTP encode/decode + content type | 01 |
| [04](04_http_endpoints.md) | Switch every HTTP handler/client | 03 |
| [05](05_websocket_binary.md) | Binary WS only; SPA + realtime | 02 |
| [06](06_spa_types.md) | SPA consumes generated types | 04, 05 |

**Blank slate.** Ship server and SPA together. One wire dialect; recreate
local assumptions if a stale client remains.

Signing input (`BytesToSign` / detached PGP) is unchanged — protobuf is
the **transport** encoding of already-structured fields, not the canonical
bytes under a signature.
