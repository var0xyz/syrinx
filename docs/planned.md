# Planned features

These are not built yet. They are part of the direction—features that complete the picture of small communities that can talk among themselves without becoming another engagement factory or surveillance surface.

## Account migration

Members should not be tied to one community. If you disagree with how yours is run, you should be able to take your account and your reeds to another one.

What works today is narrower: a community can move _itself_ to a new domain under the same server key (see [Identity, invites & recovery](/identity#server-recovery-hostile-takeover-db-loss)). Moving a single account to a _different_ community is harder. Your reeds and profile carry **user signatures**, and the server that witnessed them **countersigned** them. A new community has not validated that history, so re-establishing trust across instances—who attests what, which countersignatures count, how holdings replay—is real protocol work.

## Private messaging

One-to-one messages between members that are **end-to-end encrypted** and delivered toward the recipient—not stored or readable on the server as a message archive. Today the only private messages are the ones admins send a member, which every admin can see.

The work is shipping a path that matches the rest of the stack: local keys, verify-before-trust, no “the server has a copy for your convenience.” Relays may still help with NAT the same way reed content does—**in transit**, not as a library of chat history the operator can browse.

Chat was deferred so the PoC could ship as a canary: prove the distribution and authenticity model first, and only grow into messaging if people actually want Syrinx.

## Group chats

Same privacy posture as private messaging, for more than two people: encrypted group conversation inside a community (and, eventually, across federation where that makes sense).

Group chat is where most real coordination happens—and where centralized apps harvest the most. The design pressure is higher than DMs: membership changes, sender keys, history for new members, and the temptation to “just keep it on the server.” Syrinx should resist that temptation. Groups inherit the closed-community model: you know who is in the room, the server is not the room’s memory, and leaving or dissolving a group should not require trusting an operator’s delete button alone.

## Town halls

Some conversations don't fit in a feed. A community deciding where to go next, or working through a topic that touches a nerve, needs everyone in one room at the same time, with every voice getting its turn.

Town halls are that room: live audio discussions the community schedules ahead of time, so members can plan to be there. Anyone can listen. If you want to speak, you join the queue, and when it's your turn you have the floor. A moderator keeps it that way, so you can make your point without being talked over.

A town hall is only recorded if its organizers choose to, and you'll know before you join. When it is, the recording is made by the community's admins on their own devices, never by the server, in keeping with Syrinx's rule that the server doesn't hold anything sensitive. Members then help keep the recording, each device holding a small piece of it, so it stays with the community without any single place having to store it all.

## Command line interface

A terminal client for Syrinx, for members who would rather not live in a browser tab. It would be a full peer, not a thin wrapper around the API: your keys stay on your machine, every reed is verified before it is stored, and the content you keep is held locally, the same as in the web app.

## Protocol specification

A written spec of the Syrinx protocol, complete enough that anyone can build their own client without reading the server's source. If you don't trust the app your operator serves, you should be able to use one you trust instead (see [Openness over walled gardens](/philosophy#openness-over-walled-gardens)). That only works if the protocol is documented.

Today the pieces exist but live in code: the protobuf messages on the wire, the canonical JSON every signed payload is serialized as, and the payloads themselves. The spec would pin them down in one place:

- **Wire format** — the HTTP and WebSocket messages, and how a client authenticates.
- **Signed payloads** — what each record signs, how it is serialized, and what a client must verify before storing it.
- **Peer behavior** — how reeds are requested, held and relayed, and how removals reach holders.
- **Backup formats** — the encrypted key and full backups, so you can take your data from one client to another.

The spec would ship with the shared test vectors already used to keep the server and the web app byte-identical, so a new client can check its own signing against them.
