# Philosophy

Syrinx does not try to look like a conventional social network with better privacy settings. It makes the real constraints of networked publishing visible, and designs around them.

## Honesty about permanence

Centralized services sell the illusion of control. Through endless privacy policies and corporate speak they make you believe like you have control over your content and if you delete it, it's gone. Assuming that they actually delete it (and not simply hide it from the users) once a message has been displayed on someone else’s screen, that message has already been downloaded. They can copy it, screenshot it, save it. **They** are in control, not the server.

Syrinx puts that reality in your face. Content lives on users’ devices, all your followers hold a copy. The system will make the best effort to notify all holders and the app will immediately delete that content. There is no **guarantee** that something published can be erased from every holder, though. Assume that anything you publish may live somewhere forever.

What Syrinx offers instead of fake delete buttons:

- **Cryptographic authenticity** — peers can verify that a reed really came from a key you control.
- **Optional anonymity** — you need not use your real identity.
- **Signed removal certificates** — when you _do_ ask holders to drop a reed or an account, they can verify the request was yours (and witnessed by the server), not a forged wipe from a compromised operator. See [Deletion](/deletion).

## Closed communities by design

Syrinx is meant for **sensitive, high-trust groups**, not open growth-hacked platforms. Operators choose signup policy:

| Mode     | Meaning                    |
| -------- | -------------------------- |
| `open`   | Anyone may register        |
| `invite` | A valid invite is required |
| `closed` | No new signups             |

There is no email or phone verification. Those are privacy-violating shortcuts. Invite-only registration is how an instance stays a community instead of a spam farm—and how it resists casual surveillance culture that scrapes open networks for sentiment and dossiers.

Even an `open` community is not open to whoever happens to stumble upon it. Before you can sign up, the app asks for the **server's public key**, and it never fetches that key from the server itself: you have to get it out-of-band, from someone already in the community. Until you paste it in, the server turns your requests away, so a bot crawling the web sees a key prompt and nothing else.

The same key is what the app checks every server response against, so the gate doubles as a trust anchor: you know you are talking to the community you were told about, not to whoever answers at that address. It is a public key, not a password; any member can pass it on. It keeps out the casual visitor, not the determined one. That is what invites are for.

## Convenience is not the highest good

Control over your keys and data costs some convenience. Syrinx accepts that trade knowingly, and it is worth being clear about what you give up.

**Centralization.** The server does not hold reeds, so there is no global search across everything ever posted in a community, let alone across communities. You can search what is on your device; nobody can search everything, and that includes whoever would like to build a dossier.

**Instant access.** When you open a reed you don't hold, the app asks its holders for it. If none of them is online right now you have to wait. That is a good thing. It means the content spreads through the network in an organic way, jumping from user to user until it finds you. This is inspired by the bulletin board systems (BBS) we used to communicate in the early days of the Internet.

**A password.** There is none. Your account is your private key, which is virtually impossible to brute-force (unlike passwords) but also impossible to reset. It requires more effort on your side to keep them safe and secure.

## Offline-first: you own your data

Syrinx is **offline-first** on purpose. The server coordinates the network; it is not where your life lives. Your keys, your reeds, the profiles you follow, and the content you chose to keep sit on **your device**—IndexedDB behind a PWA that caches its own shell so the app still loads when the network does not.

If the server goes away—operator shutdown, seizure, billing failure, whatever—you should still be able to open Syrinx on the device that holds your data and read what you have. No login form that only works when someone else’s database is up. No “your account lives on our servers” hostage situation. **You own your data.**

That ownership is practical, not rhetorical. You can **export an encrypted backup** of your local store and move it to another device or keep it somewhere safe. If the server is seized, that backup is what brings the community back: the operator restores the server key under a new domain, and members export from the old app—still running from the device's cache—and import under the new one, using the app locally while the network is rebuilt. See [Why censorship-resistant?](/#why-censorship-resistant) and [Identity, invites & recovery](/identity) for the unified restore path.

What you **cannot** do yet is take that backup and upload it to a different community. Your reeds and profile carry **user signatures**; the server that witnessed them also **countersigned** them. A different community has not validated that history. [Account migration](/planned#account-migration) is planned; for now, moving means the _same_ community rebuilding through **recovery** and peer reporting, not silently repointing the client at a stranger’s tracker.

Offline-first is the guarantee that even if the server is experiencing downtime you can still operate your app normally and changes will be sync'ed as soon as the connection is re-established.

## Openness over walled gardens

Syrinx runs on **the Open Web** on purpose. Native app stores are walled gardens: opaque binaries, update gates, and a culture of “just trust the vendor.” A web client is something you can open, read, and audit. You can see what leaves the device and what claims it makes about signatures and keys.

You should **not** trust the developers—in fact, you shouldn’t have to. Every reed and sensitive record you accept is meant to be **cryptographically signed**. Verify the signatures. If the bits were tampered with in transit or by a hostile update, verification fails. Openness here means inspectability of the client _and_ authenticity you can check independently of whoever shipped the UI.

There is a limit to that. The web app is served by the server, so whoever runs the server controls the code your browser runs. A modified app could quietly skip the checks or leak your keys, and the signatures would not save you, because the code checking them is the code that changed. If you don't trust your operator with that, you don't have to use their app. Anyone can build a client that talks to a Syrinx server. Export your data, import it into a client you trust—one you audited, built yourself, or got from someone you trust more than the operator—and keep talking to the same community.

Syrinx is free and open source and will remain so, but the deeper point is epistemological: **verify, don’t trust.**

## Only store what you trust

The app does not turn your device into an open warehouse for the whole network.

See a reed on **broadcast** from someone you don’t follow? You can read it. It does not earn a permanent home on your phone. Ambient traffic stays ephemeral on purpose: so a stranger’s flood cannot poison your local store, and so **you** decide whose words you help keep alive.

**Follow** someone, and the contract changes. Their reeds become part of what you keep—and you join the mesh that holds and relays their content for others. Distribution is consent: you amplify the people you chose, not everyone who shouted into the room.

## A canary, not a cathedral

Chat—and a full social/comms suite—was deferred on purpose. The release is a **proof of concept**: a canary in the mine. Put the core idea in the world—tracker/relay distribution, signed authenticity, closed communities—and see whether anyone wants something like Syrinx.

There is no point in building an entire social network and messaging platform if nobody will use it. Demand first; polish and breadth when the canary comes back alive. Until then, depth on the foundation beats feature parity with every messenger.

## The security bar

No system survives a [wrench attack](https://xkcd.com/538/). Syrinx aims to make certain attacks **orders of magnitude more expensive**—especially forging network-wide deletion or impersonating users after server compromise—so they stop being worth it. Perfect airtightness is not the claim. Deterrence through cryptography and verification is.
