# Syrinx

**A censorship-resistant communications platform.**

> 📣 **[Introducing Syrinx: read the announcement →](https://var0.xyz/posts/introducing-syrinx.html)**

A P2P-ish, federated social platform for closed communities, where the server
never holds your posts.

We say P2P-_ish_ because NAT makes direct connections between peers very hard to
establish, so the server relays the traffic between them. That traffic travels
encrypted: a post is encrypted to its reader before it leaves the holder's
device, and the server relays ciphertext it can't read.

A Syrinx server works like a BitTorrent tracker: it knows _who has_ a post, not
_what it says_. Posts ("reeds") live on members' devices. Every record is
OpenPGP-signed and checked by the client, so trust comes from the cryptography,
not the operator. Servers connect to each other to form a mesh of small,
independent communities.

> **Status:** proof of concept, pre-launch. Nothing is stable yet: schemas, wire
> formats and APIs change without migrations. See
> [`specs/README.md`](specs/README.md) for what's built and what's planned.

## Why censorship-resistant?

- **The database is distributed among the users.** Content lives on members'
  devices; the server holds only pointers to it.
- **It's cheap to run at home.** Syrinx is built to perform well on modest
  hardware, and ships with scripts to deploy it securely on a home network
  (tested on a Raspberry Pi 5).
- **It's federated.** Communities talk to each other without all living on one
  central server, creating a mesh of small, independent communities.
- **Losing the domain doesn't mean losing the community.** A server's identity
  is its key pair, not its domain name.
- **Admins answer to the public.** Every admin action needs a second admin's
  approval and lands in a public ledger: who asked for it, who approved it, and
  the reason they gave. Members reach every admin at once by mentioning
  `@admins` in a reed.

Together, this means anyone can run their own community, and members will be
able to take their content elsewhere if they disagree with how theirs is run.
Anyone can see what admins do with the power they've been granted. The one
exception is a private message from the admins to a member, which is still
visible to every admin.

**When a server is seized.** Say the community loses the domain they operate
under. The community spins up a new server under a new domain, imports the
server's key from a backup, and starts operating right away.

Shutting a community down becomes a game of whack-a-mole: take down one small
piece and it reappears somewhere else. The network isn't impossible to attack;
the security comes from making the cost and scale of shutting it down
unrealistic.

**What a seized server does reveal.** No content, but the pointers are metadata.
The server holds user ids, usernames, emails and bios, and knows who follows
whom and who holds which reeds. Everything but the user id is member-provided,
emails and bios are optional, and none of it is validated. Ripples (comments on
reeds) are stored in plain text but only live 30 days after the last ripple on a
reed is posted.

## Admins

Admins run a community but don't control its content: no admin action removes or
hides a reed. Administrative actions are opened by one admin and approved by
another, with its reason. They all land in a server-signed, chained public
ledger that every member can read and keeps a copy of. See
[Admins](https://var0xyz.github.io/syrinx/admins).

## Running it locally

You need Go (version in `src/backend/go.mod`), Node.js, and PostgreSQL
(`docker-compose.yml` has one).

```bash
git clone https://github.com/var0xyz/syrinx.git
cd syrinx
docker compose up -d db          # or point DB_* at your own Postgres
cp .env.example .env             # set SERVER_NAME
source .env                      # config is read from the environment
```

The first boot mints the root operator identity and exits:

```bash
ROOT_KEY_EXPORT_PASSPHRASE='…' make run   # prints the path of the .sxi.gpg it wrote
```

Then start the server and the app:

```bash
make run                                  # API on :8080, Ctrl-C to stop
cd src/frontend && npm install && npm run dev   # app on :5173
```

Open <http://localhost:5173/import> and import the root identity file to sign in
as the operator. `make up` runs the same stack in Docker; it needs
`SERVER_KEY_PASSPHRASE` set, since a container has no keychain or prompt. To
deploy on a real host, see
[`deploy/scripts/syrinx/README.md`](deploy/scripts/syrinx/README.md) and the
[operator guide](https://var0xyz.github.io/syrinx/operators).

## Documentation

The **[documentation site](https://var0xyz.github.io/syrinx/)** covers the
architecture, trust model, cryptography, recovery, operators and contributors.

## Contributing and security

Start with [`AGENTS.md`](AGENTS.md) and the
[contributor docs](https://var0xyz.github.io/syrinx/contributors). Known
security findings live in [`RISKS.md`](RISKS.md); read it before touching
crypto, auth, signing or recovery.

## Disclaimer

This software is in early stages of development and has never received a
security audit. All the claims that we make about its security are completely
theoretical at this point and have never been proven. If you have a real need
for a secure communications platform do not use Syrinx.

## License

[AGPL-3.0](LICENSE)
