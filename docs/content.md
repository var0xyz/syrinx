# Content distribution

The unit of publication is a **reed**—a signed post. Distribution separates **metadata** (server) from **bodies** (peers), and separates **ambient delivery** from **what you choose to keep**.

## Reeds

Authors compose a reed, sign a canonical payload, and register it with the server. Followers and broadcast subscribers learn that something new exists. Fetching the body goes through the relay path when the viewer does not already hold it.

Server-side reed records bind identifiers important for authenticity (reed id, author id, server signing fingerprint) so countersignatures cannot be replayed across contexts casually.

## Feeds

| Feed | Meaning |
|------|---------|
| **Followcast** | Reeds from people you follow—explicit subscription |
| **Broadcast** | Community-wide traffic you opted to watch |

Syrinx will not invent a “For You” firehose of accounts you never chose. Pagination and finite windows beat infinite engagement scroll.

## Session storage vs permanent storage

Not everything that appears on screen should enter IndexedDB.

- **SessionStorage** (and similar ephemeral stores) can hold ambient broadcast material so a malicious flood cannot permanently poison the local database.
- **IndexedDB** holds what you **explicitly** care about: your own data, followed authors’ content you keep, restored backups, etc.

Opening a reed you only saw in broadcast can trigger a **request** for the body; successful, verified content may then be kept. Unsolicited delivery alone is not a write ticket to permanent storage.

## Relay: request, response, miss

Because the server is not the content library:

1. Viewer sends a reed request (e.g. when opening a reed they don’t hold).
2. Server asks a **holder** to relay.
3. Holder responds with content **in transit** through the server, or reports a **relay miss** if they no longer have it (allocations are cleared so the network stops asking that holder).
4. Viewer verifies signatures; failure can also clear bad allocations.

Requests are allowed to outlive a single page view: starting a fetch, navigating away, and returning later is intentional. It also helps distribution—even if you never re-open the reed, you may have helped move bytes.

Reconnects use **sync** to catch up pending work so races (ack stored locally, WebSocket drop mid-relay) do not leave the UI stuck forever.

## History forks and tip checks

Multi-device concurrent publishing can **fork** an author’s reed history: two devices produce divergent tips while the server expects one coherent chain. Until multi-device sync exists, the protocol leans on safeguards:

- Clients report the **last local reed** when creating a new one; mismatch → reject.
- Tip checks treat removed reeds (and account-removed authors) correctly once deletion certificates exist.

**Device binding** (single active device) is the longer-term control plane for “which device may publish,” without putting device ids into the signed public profile.

## Offline-first

Syrinx treats the device as the source of truth for what you keep. If the server disappears, the app and your stored data should still be usable locally—you can export a backup and carry it forward. See [Philosophy — Offline-first](/philosophy#offline-first-you-own-your-data) for why that matters and what moving to another instance does *not* solve yet.

### Authoring while disconnected

Authors can queue work locally (pending publishes, pending removals, pending revocations) and sync when online. The local truth is signed; the server countersigns when it accepts. Retries are designed to be **idempotent** where certificates are involved—see [Deletion](/deletion).
