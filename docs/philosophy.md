# Philosophy

Syrinx does not try to look like a conventional social network with better privacy settings. It makes the real constraints of networked publishing visible, and designs around them.

## Honesty about permanence

Centralized services sell the illusion of control: “delete this post,” endless privacy policies, corporate speak. Once a message has been displayed on someone else’s screen, that message has already been downloaded. They can copy it, screenshot it, save it. **They** are in control, not the server.

Syrinx puts that reality in your face. Content lives on users’ devices—think torrent-like distribution, not a content CDN. There is no guarantee that something published can be erased from every holder. Assume that anything you publish may live somewhere forever.

What Syrinx offers instead of fake delete buttons:

- **Cryptographic authenticity** — peers can verify that a reed really came from a key you control.
- **Optional anonymity** — you need not use your real identity.
- **Signed removal certificates** — when you *do* ask holders to drop a reed or an account, they can verify the request was yours (and witnessed by the server), not a forged wipe from a compromised operator. See [Deletion](/deletion).

## Closed communities by design

Syrinx is meant for **sensitive, high-trust groups**, not open growth-hacked platforms. Operators choose signup policy:

| Mode     | Meaning                    |
|----------|----------------------------|
| `open`   | Anyone may register        |
| `invite` | A valid invite is required |
| `closed` | No new signups             |

There is no email or phone verification. Those are privacy-violating shortcuts. Invite-only registration is how an instance stays a community instead of a spam farm—and how it resists casual surveillance culture that scrapes open networks for sentiment and dossiers.

## Attention is not a product

No push notifications. No algorithmic feed of things you didn’t ask for. No infinite scroll engineered for engagement. Follow people on purpose; read when you choose. The product is the conversation you opted into, not a machine optimized to keep you scrolling.

## Convenience is not the highest good

Losing a device without a backup can mean losing access to your account. There is no “forgot password” that magically restores encrypted material you no longer hold. That is uncomfortable—and intentional. Control over keys and data often costs convenience. Syrinx chooses the former.

## Openness over walled gardens

Syrinx runs on **the Open Web** on purpose. Native app stores are walled gardens: opaque binaries, update gates, and a culture of “just trust the vendor.” A web client is something you can open, read, and audit. You can see what leaves the device and what claims it makes about signatures and keys.

You should **not** trust the developers—in fact, you shouldn’t have to. Every reed and sensitive record you accept is meant to be **cryptographically signed**. Verify the signatures. If the bits were tampered with in transit or by a hostile update, verification fails. Openness here means inspectability of the client *and* authenticity you can check independently of whoever shipped the UI.

FOSS is part of that promise (see Values), but the deeper point is epistemological: **verify, don’t trust.**

## Only store what you trust

The app does not turn your device into an open warehouse for the whole network.

See a reed on **broadcast** from someone you don’t follow? You can read it. It does not earn a permanent home on your phone. Ambient traffic stays ephemeral on purpose: so a stranger’s flood cannot poison your local store, and so **you** decide whose words you help keep alive.

**Follow** someone, and the contract changes. Their reeds become part of what you keep—and you join the mesh that holds and relays their content for others. Distribution is consent: you amplify the people you chose, not everyone who shouted into the room.

## A canary, not a cathedral

Chat—and a full social/comms suite—was deferred on purpose. The release is a **proof of concept**: a canary in the mine. Put the core idea in the world—tracker/relay distribution, signed authenticity, closed communities—and see whether anyone wants something like Syrinx.

There is no point in building an entire social network and messaging platform if nobody will use it. Demand first; polish and breadth when the canary comes back alive. Until then, depth on the foundation beats feature parity with every messenger.

## The security bar

No system survives a [wrench attack](https://xkcd.com/538/). Syrinx aims to make certain attacks **orders of magnitude more expensive**—especially forging network-wide deletion or impersonating users after server compromise—so they stop being worth it. Perfect airtightness is not the claim. Deterrence through cryptography and verification is.
