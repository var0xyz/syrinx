# Server key rotation

Replacing the server's signing key without stranding its users or peers.
Replacing a key always **revokes** the current one in favour of a successor,
and both keys sign the revocation. One operator command,
`ops rotate-key ["reason"] [--compromised]`: without the flag, clients and peers
adopt the successor automatically; with it, the old key is **compromised** and
everyone confirms the successor out-of-band. It replaces the `.rvk` revocation
files, which predate the `ops` tool.

Terms match user keys: a key is *revoked* once it has a successor, and a
revocation is *compromised* or not.

**Blank slate — no migration, no backwards compatibility.** Schema changes go
into `InitDB`; recreate the DB.

| #                                | Title                                                  | Depends on |
|----------------------------------|--------------------------------------------------------|------------|
| [00](00_design.md)               | Design, key states, trust model                        | —          |
| [01](01_ops_commands.md)         | Revocation records, `ops rotate-key`, drop `.rvk`      | 00         |
| [02](02_revocation_endpoint.md)  | Revocations through `/keys`, server-side enforcement   | 01         |
| [03](03_spa.md)                  | SPA: adopt successors, refuse compromised keys         | 02         |
| [04](04_peers.md)                | Peers: notify, re-pin, fallback                        | 01         |

02 and 04 can proceed in parallel once 01 is in.

---

## Status

| #  | Title                                                  | Status   |
|----|--------------------------------------------------------|----------|
| 00 | Design, key states, trust model                        | Implemented |
| 01 | Revocation records, `ops rotate-key`, drop `.rvk`      | Implemented |
| 02 | Revocations through `/keys`, server-side enforcement   | Implemented |
| 03 | SPA: adopt successors, refuse compromised keys         | Implemented |
| 04 | Peers: notify, re-pin, fallback                        | Implemented |

**Track status: Implemented.**

## Locked decisions

- The server mints a key on its own only on first boot. Every later key comes
  from `ops rotate-key`; a revoked current key with no successor stops the
  server from booting.
- Replacing a key revokes the old one, and the **old key always signs the
  revocation**, naming its successor; the successor signs it too.
- The revocation carries a signed **`compromised`** flag. It is the only thing
  that tells a planned rotation from a compromise, and lives only on the
  revocation (`private_key_revocations`), never on the key.
- **Not compromised:** the revoked key's signatures stay valid, and clients and
  peers that trust it adopt its successor automatically.
- **Compromised:** nothing new backed by the revoked key is accepted, nobody
  adopts its successor automatically, and its signatures count only if
  timestamped before the revocation.
- Server keys use the **same endpoints as user keys**:
  `GET /keys/{id}/revocation` and `GET /keys/{id}`. Both take signed requests,
  so only registered users and peers can walk to a new key. Routes
  that skip user-signature auth (signup, login, `/server/info`, …) keep
  accepting only the current key, so a revoked key opens nothing to newcomers.
- Peers are told **once per peer**, and look up our revocations if they miss
  the notice.
- Signatures made by a compromised key are not re-issued. Clients hold the
  signed records and nothing can re-deliver them credibly; the backdating gap
  is accepted in `RISKS.md`.
