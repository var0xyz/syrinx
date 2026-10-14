# Admin actions

This directory specifies what **admins and root** can do to accounts on their
own instance: kick, suspend and reinstate, promote and demote, rebind a lost
key, reserve usernames, review flags, send broadcasts and private messages,
and connect to other servers. Every action is opened by one admin, approved
by another, and recorded in a **public ledger** with its reason. The one
exception is a private message to a single user, which is never logged.

Admins do **not** control content. No admin action removes or hides an
individual reed. The only way content leaves the network is a kick, which
takes the whole account out.

**Blank slate — no migration, no backwards compatibility.** Recreate the DB
when schema changes.

| #                  | Title                                        | Depends on                  |
| ------------------ | -------------------------------------------- | --------------------------- |
| [00](00_design.md) | Design + locked model                        | [roles](../roles/README.md) |
| [01](01_requests_and_approvals.md) | Requests, approvals and approver eligibility | 00                          |
| [02](02_ledger.md) | Public ledger                                | 01                          |
| [03](03_suspend_reinstate.md) | Suspend / reinstate                          | 01, 02                      |
| [04](04_kick.md) | Kick (invite-tree selection, removal fanout) | 01, 02                      |
| [05](05_promote_demote.md) | Promote, demote and permissions              | 01, 02                      |
| [06](06_lost_device_rebind.md) | Lost-device key rebind                       | 01, 02                      |
| [07](07_reserved_usernames.md) | Reserved usernames                           | 00, 02                      |
| [08](08_flags.md) | Flags                                        | 00                          |
| [09](09_broadcasts.md) | Broadcasts and private messages              | 01, 02                      |
| [10](10_admin_spa.md) | Admin SPA                                    | 01–09                       |

---

## Status

**Implemented.**

| Step  | Status                   |
| ----- | ------------------------ |
| 00    | Implemented              |
| 01–10 | Implemented              |

## Locked decisions

| Topic              | Decision                                                                                                                                                                                    |
| ------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Content control    | **None.** No per-reed removal or hiding by admins; no "hide from discovery"                                                                                                                 |
| Two-person rule    | Every action except reserved usernames, admin invites, mesh and flag review needs a **second admin's approval**                                                                             |
| Permissions        | `admin.create`, `admin.demote`, `user.kick`, `user.suspend`, `user.rebind`, `user.message`, `broadcast.message`, `mesh.invite`, `mesh.connect`, `mesh.disconnect`. Requester and approver both need the action's; nobody grants one they lack; root holds all; no stored "all" |
| Approver           | Anyone can try to approve; the server refuses the approval if the approver is the requester, a target, or, on a kick or a permission change, someone the requester **invited directly**. Root may approve its own requests |
| Acting on admins   | Any admin may request; **root must approve**. Nobody acts on root                                                                                                                           |
| Expiry             | Requests **never expire**                                                                                                                                                                   |
| Moot requests      | A target that leaves (self-deletes or is kicked by another request) drops out; a request with no targets left is **moot**                                                                   |
| Reasons            | **Required** on every action except broadcasts and private messages, and **public**                                                                                                                                              |
| Ledger             | Readable by every **signed-in local member** who isn't suspended. One **server-signed**, chained entry per admin action (server timestamp, the acting admin's ID, a message without usernames, target user IDs, content). A request gives two entries: opened (with the reason) and closed (naming the request ID), pushed to every member over the WebSocket and stored locally, so a member can detect a rewritten ledger. Paginated endpoint for backfill on signup |
| Logged             | **Every** admin action: requests (executed, rejected, withdrawn, moot), key rebinds, broadcasts, admin invites created/claimed, mesh invites created/canceled, mesh connections approved/rejected, disconnections, reserved usernames |
| Not in the ledger  | Private messages and flags                                                                                                                                                                  |
| Kick               | **Final.** Account and **all its content** removed; username freed; the person may sign up again with a new invite                                                                          |
| Kick selection     | Admin picks targets from the target's **invite tree** (inviter above, invitees below, shown as a thread)                                                                                    |
| Kick removal trust | Removal certificate carries **both admins' signatures** + server countersignature; clients verify all three, and trust the server checked roles and permissions                             |
| Suspend            | **Reversible, local only.** The server refuses the user's signed operations, ledger reads and WebSocket with the suspension certificate; peers see no change                                |
| Reinstate          | Manual only, needs approval                                                                                                                                                                 |
| Promote / demote   | user ↔ admin; needs approval. A promotion carries the permission set; a separate request changes it. Demoting or changing an admin's permissions is acting on an admin → root approves  |
| Lost device        | User enters their **user ID** (no username lookup) on a **lost-device page**, mints a key, downloads `<userID>.pub.txt.gz`, hands it to an admin out of band                                |
| Rebound key        | **Restricted**: can sign only a rotation to a freshly minted key; anything else (reeds included) is refused                                                                                 |
| Rebind trust       | Old key revoked as **lost** (old reeds stay valid), marked **admin-rebound**; vouches for the old key don't carry over                                                                      |
| Reserved usernames | **Root only**, case-insensitive, affect **future** signups only, no approval, logged. `root` belongs to the root identity forever, even after root renames                                          |
| Flags              | Users flag a **reed ID** only; admins load content from the network. Server never sees content                                                                                              |
| Flag visibility    | Admins see the reporter. Flags on an admin's reed are visible only to **other** admins. Root's reeds can't be flagged                                                                       |
| Flags vs actions   | No link: an action is taken on an account, never "because of a flag"                                                                                                                        |
| Broadcasts         | Admin message (≤140 characters) to every **local** member's mailbox; needs approval; never leaves the instance; logged                                                                      |
| Private messages   | Admin message to **one** local member's mailbox; needs approval like a broadcast; **not** logged                                                                                            |
| Federation         | No admin action reaches into another server. Refusing a remote user's content is **out of scope**                                                                                           |
