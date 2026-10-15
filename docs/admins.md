# Admins

Admins run a community, but they don't control its content, and members can see everything they do with the power they've been given.

## Roles

| Role | Who | Can |
|------|-----|-----|
| **Root** | The operator identity minted on the server's first boot | Every admin permission; reserves usernames; approves actions on admins |
| **Admin** | A member promoted by another admin, or invited as one | The permissions granted at promotion, and no more |
| **Member** | Everyone else | Read the ledger, flag reeds, reach the admins with `@admins` |

Permissions are granted one by one: `admin.create`, `admin.demote`, `user.kick`, `user.suspend`, `user.rebind`, `message.unicast`, `message.broadcast`, `mesh.invite`, `mesh.connect`, `mesh.disconnect`. Nobody can grant a permission they don't hold.

## What admins can't do

- **Touch content.** No admin action removes or hides an individual reed. The only way content leaves the network is a kick, which takes the whole account out.
- **Reach into another server.** Every admin action stays on the admin's own instance.
- **Act on root.** Nobody can.

## Two admins per action

Kicks, suspensions, reinstatements, promotions, demotions, key rebinds, broadcasts and private messages are opened by one admin and approved by another. Both need the action's permission. The server refuses an approval from the requester, from a target, or—on a kick or a permission change—from someone the requester invited directly.

Acting on an admin (demoting them, changing their permissions) always needs root's approval. Root may approve its own requests: a community with a single admin still has to be able to do anything at all.

Every action except a broadcast or a private message needs a reason. Requests never expire; one whose targets have all left is dropped as moot.

## The ledger

Every admin action lands in a ledger that every signed-in member who isn't suspended can read. That covers requests (executed, rejected, withdrawn or moot), key rebinds, broadcasts, admin invites, mesh invites, mesh connections and disconnections, reserved usernames, and server key rotations. Each request writes two entries: one when it's opened, with the reason, and one when it closes. An entry records who asked, who approved, the targets and the reason.

Entries are server-signed and chained, and pushed to every member, who keeps a copy on their device. A server that rewrites its ledger contradicts the copies its members already hold.

Two things stay out of the ledger:

- **Private messages** from the admins to a member. Every admin can see them.
- **Flags.** A flag is a member's report, not an admin action. Whatever admins do about it is an action, and is logged.

## Actions

| Action | What happens |
|--------|--------------|
| **Kick** | Final. The account and all its content are removed from the network; the username is freed, and the person may sign up again with a new invite. Targets are picked from the member's invite tree. The removal certificate carries both admins' signatures and the server's countersignature, and clients check all three. |
| **Suspend / reinstate** | Reversible and local. The server refuses the member's signed operations, ledger reads and connection until an approved reinstatement. Peer servers see no change. |
| **Promote / demote** | Member ↔ admin. A promotion carries its permission set; changing it later is a separate request. |
| **Rebind a lost key** | A member who lost every device mints a new key on the lost-device page and hands the public key to an admin out of band. The old key is revoked as lost, so old reeds stay valid, and the new key can only sign a rotation to a fresh one. |
| **Broadcast** | A message of up to 140 characters to every local member's mailbox. |
| **Private message** | The same, to one member. Not in the ledger, but visible to every admin. |
| **Reserve usernames** | Root only, no approval. Affects future signups only. |
| **Admin invites** | An admin invite expires after 24 hours and is logged once it's claimed. See [Invites](/invites#admin-invites-expire). |
| **Federation** | Peering with another server needs an admin's approval; see [Architecture — Federation](/architecture#federation). |

## Flags and `@admins`

A member flags a **reed id**, never its content. The server never sees what was flagged; admins load the reed from the network like anyone else. Admins see who filed a flag. A flag on an admin's reed is visible only to the other admins, and root's reeds can't be flagged.

To talk to the admins, a member mentions `@admins` in a reed. It reaches every admin of that server at once, and works for a peer server's admins too. It's also how a member answers a message from the admins.

## Related

- [Trust model](/trust) — what the ledger and signatures protect against
- [Invites](/invites) — admin invites and the invite tree
- [Identity, invites & recovery](/identity) — lost-device rebind and recovery
- [Operators](/operators) — the root identity and server key
