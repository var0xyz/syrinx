# Syrinx

Syx, for short, is a distributed, P2P-*ish* content platform.

The Internet we fell in love with is dying. First it was the abusive monopoly of one browser dictating the direction the Web had to take. Then came the centralization of search through one company. Then the monopoly of communication and content creation by social networks. Finally, total surveillance through age verification—nothing else than a hard link between the content you create and your identity. A complete loss of privacy.

Syrinx is built for closed communities that care about privacy. The server tracks who published what and helps peers find each other. **It does not store any content.** Holders on the network relay reeds; clients verify OpenPGP signatures before they trust anything.

Keep in mind that is a proof of concept, an idea for how we can reclaim it: small, independent, underground communities, away from the reach of the panopticon the Web has become—many communities interconnected in a mesh through federation.

## Values

- **It's your attention:** No push notifications, no alerts, no interruptions.
- **It's your platform:** Syrinx will never push content to your feed you didn't subscribe to.
- **It's your time:** No infinite scroll, no engagement optimization, no dark patterns.
- **It's your decision:** We give you control over your data, even at the expense of convenience.
- **It's your right:** No tracking, no analytics, no data collection.
- **It's open:** Built for the Open Web. You can inspect what the app does, the data it stores and transmits.
- **It's our promise:** Syrinx is free and open source, and will remain so.

## Why P2P-*ish*?

The server stores no content. It works more like a BitTorrent **tracker**: it knows *who has* a reed, not *what that reed says*. Peers hold the bodies; the server keeps anonymized references—IDs and routing metadata—so holders and viewers can find each other.

True peer-to-peer sockets are unreliable for most people. NAT makes hole-punching fragile, so Syrinx uses the server as a **relay** when content must move between devices. Relayed bytes are in transit only. Nothing is archived there as a library of posts.

What does travel the network is still safe to treat as untrusted infrastructure: every reed and sensitive record is **cryptographically signed**. Clients verify OpenPGP signatures before they trust or keep anything. The path may be plain to the wire; authenticity does not depend on trusting the relay.

That is the *ish*: distribution and verification like a peer network, with a tracker/relay in the middle because the internet we actually have is full of NATs.

## Start here

| Topic | What you'll learn |
|-------|-------------------|
| [Philosophy](/philosophy) | Honesty about permanence, closed communities, what we refuse to fake |
| [Planned features](/planned) | Likes, federation, ephemeral comments, messaging, discoverability |
| [Architecture](/architecture) | API, realtime, SPA, and the tracker/relay role of the server |
| [Trust model](/trust) | What we defend against, and what we don't pretend to |
| [Cryptography](/cryptography) | Keys, canonical signing, user vs server signatures |
| [Content distribution](/content) | Reeds, feeds, relay, local storage rules |
| [Identity, invites & recovery](/identity) | Accounts, signup modes, rebuilding after disaster |
| [Deletion](/deletion) | Signed removal certificates instead of silent wipes |
| [Operators](/operators) | Run a server, keys, recovery procedures |
| [Contributors](/contributors) | Package layout and where design intent lives |

This site is the **canonical documentation** for Syrinx. When behavior changes, these pages change with it.
