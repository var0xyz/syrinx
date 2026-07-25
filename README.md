# Syrinx

A distributed, P2P-*ish* content platform for closed communities that care about attention, privacy, and cryptographic authenticity.

The server tracks metadata and relays in transit. **Content lives on devices.** Peers verify OpenPGP signatures; the instance is a tracker, not a content library.

## Values

Syrinx is built on the following values:

* It's your attention: No push notifications, no alerts, no interruptions.
* It's your platform: Syrinx will never push content to your feed you didn't subscribe to.
* It's your time: No infinite scroll, no engagement optimization, no dark patterns.
* It's your decision: We give you control over your data, even at the expense of convenience.
* It's your privacy: Private messages are never routed through the server, they are delivered directly to the recipient, encrypted.
* It's your right: No tracking, no analytics, no data collection.
* It's open: Built for the Web, not a native walled garden—so you can inspect what the app does. You should not have to trust the developers; verify that what you receive is cryptographically signed and untampered.
* It's our promise: Syrinx is free and open source, and will remain so.

## Documentation

**[Documentation site](https://var0xyz.github.io/syrinx/)** — architecture, trust model, cryptography, recovery, operators, and contributors. That site is the canonical source of truth.

## Quick start

```bash
git clone https://github.com/var0xyz/syrinx.git
cd syrinx
cp .env.example .env   # set SERVER_NAME and related vars
make run
```

Stop with `make stop`. More detail: [Operator guide](https://var0xyz.github.io/syrinx/operators).

## License

AGPL-3.0
