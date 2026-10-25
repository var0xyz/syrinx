# Bans

Two admins of a server ban a **user of a peer server** from interacting with
their community. The banned user can still read the community's content, follow
its members and like its reeds. Their replies, echoes, mentions and ripples no
longer reach it. Nothing they did before the ban is touched, and they can always
delete their own reeds.

A ban doesn't remove anyone from the network: only the user's home server can
kick them. The banning server asks nothing of the home server, but tells it
once. The home server stores the ban, shows it to its admins, tells its user,
and stops delivering what the banning server would refuse anyway.

Throughout: **A** is the banning server, **B** is the banned user's home server,
**Bob** is the banned user.

**Blank slate — no migration, no backwards compatibility.** Recreate the DB when
schema changes.

| #                          | Title                                                    | Depends on                                       |
| -------------------------- | -------------------------------------------------------- | ------------------------------------------------ |
| [01](01_requests.md)       | Ban and unban requests, `user_bans`, `peer_bans`, ledger | [admin actions](../admin_actions/README.md)      |
| [02](02_enforcement_a.md)  | Enforcement on A                                         | 01                                               |
| [03](03_notices.md)        | Ban notices A → B, mailbox, B's admin list               | 01                                               |
| [04](04_enforcement_b.md)  | Enforcement on B                                         | 03                                               |
| [05](05_unban_snapshot.md) | Unban snapshot                                           | 03, 04                                           |
| [06](06_flags.md)          | Flags reach the reporter's server too                    | [admin actions 08](../admin_actions/08_flags.md) |
| [07](07_spa_banned.md)     | SPA, banned side: banner, modal, mailbox                 | 03, 04                                           |
| [08](08_spa_admin.md)      | SPA, admin side: ban action, lists                       | 01, 03                                           |

06 is independent of the rest and can land first.

---

## Status

**Proposed.**

| Step  | Status   |
| ----- | -------- |
| 01–08 | Proposed |

## Locked decisions

| Topic             | Decision                                                                                                                                                       |
| ----------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Name              | **Ban**, to keep it apart from a member's [block](../blocking/README.md)                                                                                       |
| Who can be banned | A user of a **connected peer**. Never a local user (that's a kick or a suspension). A foreign admin or root is just a foreign user to A                        |
| Permission        | **`user.kick`**, for ban and unban. A ban is a kick limited to this server                                                                                     |
| Approval          | The two-admin rule of every request. The approver can't be the requester or someone the requester invited directly. No invite-tree selection, no root approval |
| Reason            | Required, public, in the ledger like every request's                                                                                                           |
| Reversible        | Yes, by an approved **unban** request, recorded on the ban                                                                                                     |
| History           | A ban is never deleted. A user banned again gets a new ban, and admins see the earlier ones. B keeps every ban of its users too                                |
| Reads             | **Never refused.** Bob reads A's profiles, reeds, threads and ripples as before                                                                                |
| Follows, likes    | **Allowed.** They aren't interactions                                                                                                                          |
| Replies, echoes   | Bob can still post them on B, and they reach his followers. They never reach A                                                                                 |
| Mentions          | Bob's reeds publish; mentions of A's members and of A's `@admins` are never delivered                                                                          |
| Ripples           | Readable, like every read. Bob can't post them on A's reeds, but can delete his own                                                                            |
| Flags             | A refuses Bob's flags on its reeds                                                                                                                             |
| Appeal            | Through Bob's own admins, who take it up with A. Bob can't reach A's admins                                                                                    |
| Before the ban    | Everything stays. Bob can delete his own reeds and ripples, and the deletion always reaches A. Nothing new is created on A                                     |
| Unban             | B sends A **one full snapshot** of Bob's live replies, echoes and mentions involving A. A reconciles; no live notifications for them                           |
| Broadcast feed    | A never puts Bob's reeds in its broadcast feed. A's members who follow Bob still get them                                                                      |
| Search            | Bob's searches skip A; A's members' searches skip Bob, but a mention typed with his ID goes through                                                            |
| One-way           | A's members can always reach Bob: mention, reply, like, echo, ripple on his reeds. Nothing Bob does reaches A. Bob's recourse is a block or his own admins     |
| Enforcement       | **Both servers.** B refuses at the source; A refuses as a backstop, answering 403 with the certificate. A never depends on B                                   |
| B not cooperating | B's data goes out of sync with A, A's doesn't                                                                                                                  |
| Notice            | One notice per ban, A → B, carrying its lift once there is one (fold at the border)                                                                            |
| Telling Bob       | B sends a mailbox message with the certificate. Bob's client verifies it                                                                                       |
| B's admins        | See every ban a peer made of their users, active and lifted                                                                                                    |
| Out of scope      | Refusing reads; showing admins a peer's kicks of its own users                                                                                                 |
