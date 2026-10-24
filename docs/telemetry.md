# Telemetry

Syrinx has **no client-side telemetry by default**. The app on your device
doesn't report what you click, read or scroll past, and it doesn't
fingerprint your browser. Nothing in the app phones home to an analytics
service.

Whatever the system collects is server-side, and only in terms the server
already knows: your **user ID**, which is already in its database.

## Disclaimer

This page describes what Syrinx is designed to do **by default**, as it
ships in this repository. Anyone can clone Syrinx, change it and deploy it.
Whoever operates a server can change it to collect more data than described
here, and as a user you would have no way of knowing.

Nothing on this page is a claim or a guarantee about what any particular
server does. Before you join a server, make sure you trust the people who
operate it.

## What the system doesn't collect

- **IP addresses.** nginx, which sits in front of the app, strips every
  IP header before a request reaches the app, so the app only ever sees
  connections from `127.0.0.1`. It has no way to learn your address.
- **User agents, browser or device fingerprints.** Not recorded, and
  stripped from telemetry before it leaves the app host.
- **Device IDs and usernames.** Log lines and metrics name users by ID only.
- **Reed content.** The server never sees it in the first place; see
  [Content privacy](/content_privacy).

In short: nothing you didn't already hand the server by having an account.

## What the system collects, and why

Server telemetry counts what happens on the server (requests, database
queries, reeds, follows, invites, keys, WebSocket and federation traffic,
admin actions, refused requests) tagged with the user, reed or key IDs
involved. It exists to answer two kinds of questions:

- **Performance.** Which requests are slow, which queries need tuning,
  where the server needs more capacity.
- **Abuse.** Whether a member is creating resources far faster than anyone
  else, or injecting malicious content into the network, such as reeds
  whose claimed tags or mentions don't match what they really contain.

Because every record is tied to a user ID rather than to a person, a
browser or a network address, it's pseudonymous: it tells an operator
*which account* did something, never who's behind it or where they are.

## Federation

When you follow someone on another community's server, read their reeds
or reply to them, your app never talks to that server. It only ever
connects to your home server, which relays everything to the peer on your
behalf; see [Relay model](/relay-model).

So a third-party server never sees you directly:

- Every connection it receives comes from **your home server's IP
  address**, never yours.
- It can't fingerprint your browser, because your browser never reaches it.
- It learns only what federation needs it to: your user ID and the signed
  records you send its way.

If you trust your home server, a third-party server can't collect more
about you than that. The exception is a link you choose to open yourself:
your browser goes there directly, like any link on the web.

## Limits

- Telemetry is **optional and off by default**. An operator turns it on
  by pointing the server at a collector they run themselves; Syrinx sends
  nothing to its developers or to any third party.
- It's **deleted after 30 days** by default.
- An operator runs their own nginx and network. Everything above holds for
  the stock deploy scripts. If a server is reached through Cloudflare,
  Cloudflare sees visitors' IPs; that's outside Syrinx's control.

How each of these is enforced, and what an operator should do to keep it
that way, is under [Operators → Privacy](/operators#privacy).
