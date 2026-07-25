# Deletion

Hard-deleting a row on the server is not enough in a system where **holders** keep content. If the server forgets a reed, it cannot reliably tell peers to drop it—and a compromised server that can invent “please delete everything” events could wipe the distributed store.

Syrinx therefore uses **signed removal certificates**.

## Trust split

| Layer | Who | Attests |
|-------|-----|---------|
| User signature | Author’s active key | “I remove this reed / this account” |
| Server countersignature | Active server key | “This server witnessed that removal at this timestamp” |

Clients **must** verify both before deleting local reed content or purging peer account data. Either failure → ignore the event; keep data.

That raises the bar for an attacker who only owns the server: they need author keys (or a long game of MITM per author/holder pair), not a single forged admin RPC.

## Reed removal

### Offline-first author flow

1. Write a local `pendingRemoval` (ids + user signature).
2. `DELETE` to the server when online (body carries the user signature; retry across restarts).
3. Persist the returned certificate (e.g. `removedReeds`).
4. Drop the reed from local `reeds`.
5. Clear the pending entry.

### Server behavior

- First successful accept stores user signature + countersignature.
- Later identical retries return the **same** cert—never mint a second server signature for the same removal (**idempotent**).
- `GET` of a removed reed returns **410 Gone** with the certificate body so holders can verify before purge.
- Fanout reuses the new-reed realtime path (live dispatch + `SYNC_REQUEST` catch-up). Holders clear allocations after apply so removals do not re-deliver forever. The server may keep bookkeeping rows so allocations are not cascade-deleted before peers apply.

## Account removal

Account deletion is a signed certificate with an optional short note (≤140 characters).

- Author flow is **online-only** (sign → DELETE → verify → wipe local session)—not a silent offline retry queue. Leaving an account should not “happen later by accident.”
- Peers receive `ACCOUNT_REMOVED` (live and on sync catch-up) and **410** bodies typed as account.
- One account cert authorizes peers to drop **all** local reeds by that user id. Per-reed certs are not required for account teardown.
- **Public keys remain** on server and devices—tombstones stay verifiable; goodbye notes can show on a gone profile.

`/delete/confirm`-style steps that exist for UX are auth-gated appropriately; form bodies on `DELETE` must actually be parsed (Go’s default form parsing ignores DELETE bodies).

## Why soft certificates fit privacy here

In many systems, soft-delete tombs fight privacy because the server retained content. Syrinx’s server primarily holds **references and metadata**, not a content archive. Certificates prove *authorization to purge local copies*, not a confession of plaintext stored centrally.

## Interaction with tips and recovery

Tip / history-fork checks should treat a reed covered by a removal cert as gone, and should not treat account-removed authors as normal tip sources. Recovery ingest of deletion certificates may lag the first recovery cut; the design still assumes holders already apply fanout/410 in normal operation.
