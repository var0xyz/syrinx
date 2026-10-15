# Syrinx

**A censorship-resistant communications platform.**

Syrinx (Syx, for short) is a P2P-_ish_, federated social platform for closed communities, where the server never holds your posts.

The Internet we fell in love with is dying. First it was the abusive monopoly of one browser dictating the direction the Web had to take. Then came the centralization of search through one company. Then the monopoly of communication and content creation by social networks. Finally, total surveillance through age verification—nothing else than a hard link between the content you create and your identity. A complete loss of privacy.

Syrinx is a proof of concept, an idea for how we can reclaim it: small, independent, underground communities, away from the reach of the panopticon the Web has become—many communities interconnected in a mesh through federation.

## Why P2P-_ish_?

NAT makes direct connections between peers very hard to establish, so the server **relays** the traffic between them. That traffic travels encrypted: a reed is encrypted to its reader before it leaves the holder's device, and the server relays ciphertext it can't read. See [Content privacy](/content_privacy).

The server works like a BitTorrent **tracker**: it knows _who has_ a reed, not _what that reed says_. Reeds live on members' devices. Every reed and sensitive record is **OpenPGP-signed** and checked by the client before it trusts or keeps anything, so trust comes from the cryptography, not the operator.

That is the _ish_: distribution and verification like a peer network, with a tracker/relay in the middle because the internet we actually have is full of NATs.

## Why censorship-resistant?

- **The database is distributed among the users.** Content lives on members' devices; the server holds only pointers to it.
- **It's cheap to run at home.** Syrinx is built to perform well on modest hardware, and ships with scripts to deploy it securely on a home network (tested on a Raspberry Pi 5). See [Operators](/operators#running-at-home).
- **It's federated.** Communities talk to each other without all living on one central server.
- **Losing the domain doesn't mean losing the community.** A server's identity is its key pair, not its domain name.
- **Admins answer to their members.** Admin actions need a second admin's approval and land in a ledger every member can read: who asked, who approved, and the reason they gave. Members reach every admin at once by mentioning `@admins` in a reed. See [Admins](/admins).
- **Moving your account to another community** is [planned](/planned#account-migration), not yet implemented.

Together, this means anyone can run their own community, and members will be able to take their content elsewhere if they disagree with how theirs is run. Members can see what admins do with the power they've been granted. The one exception is a private message from the admins to a member, which stays out of the ledger but is visible to every admin.

### When a server is seized

Say a malicious actors manages to take over a community's domain. The community spins up a new server under a new domain, imports the server's key from a backup, and starts operating right away. The app is offline-first and keeps running from the device's cache with no server at all, so members open the old app, export their data, and import it under the new domain. Because everything is signed, the server recognizes it and rebuilds its database. Federation links with other servers have to be re-established. See [Identity, invites & recovery](/identity#server-recovery-hostile-takeover-db-loss).

Shutting a community down becomes a game of whack-a-mole: take down one small piece and it reappears somewhere else. The network isn't impossible to attack; the security comes from making the cost and scale of shutting it down unrealistic.

### What a seized server does reveal

No reed content, but the pointers are metadata. The server holds user ids, usernames, emails and bios, and knows who follows whom and who holds which reeds. Everything but the user id is member-provided, emails and bios are optional, and none of it is validated. It also holds who liked which reed, the admin ledger, and [ripples](/content#ripples) (comments on reeds), which are stored in plain text but only live 30 days after the last ripple on a reed.

### Cloudflare

The home-deploy scripts put the server behind Cloudflare. With a Cloudflare Tunnel (the default, no port forwarding), all traffic passes through Cloudflare, which terminates TLS and sees the same metadata the server does, though not reed bodies. With mTLS, nginx serves the public edge from home and Cloudflare only keeps the domain's DNS pointed at your home IP. Either way, Cloudflare is a point where a community can be cut off; the server key and the offline-first app are what let it come back elsewhere.

## Start here

| Topic                                     | What you'll learn                                                                             |
| ----------------------------------------- | --------------------------------------------------------------------------------------------- |
| [Philosophy](/philosophy)                 | Honesty about permanence, offline-first ownership, closed communities, what we refuse to fake |
| [Planned features](/planned)              | Account migration, private messaging, group chats, discoverability                            |
| [Architecture](/architecture)             | API, realtime, SPA, federation, and the tracker/relay role of the server                      |
| [Trust model](/trust)                     | What we defend against, and what we don't pretend to                                          |
| [Cryptography](/cryptography)             | Keys, canonical signing, user vs server signatures                                            |
| [Content distribution](/content)          | Reeds, feeds, relay path, consent and abuse guardrails                                        |
| [Content privacy](/content_privacy)       | Why the server never sees reed content, and how end-to-end encrypted relay works              |
| [Relay model](/relay-model)               | Schema-level reference for the relay/allocation machinery and how it crosses server borders   |
| [Invites](/invites)                       | Invite links, hash-only storage, redeem flow, hijack resistance                               |
| [Identity, invites & recovery](/identity) | Accounts, signup modes, rebuilding after disaster                                             |
| [Deletion](/deletion)                     | Signed removal certificates instead of silent wipes                                           |
| [Admins](/admins)                         | Roles, two-admin approval, the ledger, flags and `@admins`                                    |
| [Operators](/operators)                   | Run a server, at home or elsewhere, keys, recovery procedures                                 |
| [Contributors](/contributors)             | Repository layout and where design intent lives                                               |
