# Town halls

A scheduled live audio discussion about a topic. Admins schedule it and everyone
is notified. At the set time members join. The host introduces the topic and
answers. Open-mic speakers can talk whenever they want. The rest of the audience
raises a hand, and the moderator decides who speaks next.

The server never stores audio. A town hall is live; the only thing kept
afterwards is a recording, if the town hall was set to make one and said so up
front. Admins' devices record it, and it's published like any other signed
content held by peers.

**Blank slate — no migration, no backwards compatibility.** Recreate the DB when
schema changes.

| #   | Title                                                            | Depends on                                      |
| --- | ---------------------------------------------------------------- | ----------------------------------------------- |
| 00  | Transport spike: Opus frames over the WebSocket                  | —                                               |
| 01  | Town hall records: schedule, edit, cancel, `town_halls`          | [roles](../roles/README.md)                     |
| 02  | Announcement, RSVP, reminders                                    | 01, [notifications](../notifications/README.md) |
| 03  | Lifecycle and roles: lobby, live, ended; join, leave, fallback   | 01                                              |
| 04  | Audio relay over the WebSocket, server-enforced floor            | 00, 03                                          |
| 05  | Queue and floor: raise hand, reorder, grant, revoke, turn limits | 04                                              |
| 06  | Side chat                                                        | 03                                              |
| 07  | Removing someone from a town hall                                | 03                                              |
| 08  | Recording and transcript on admins' devices                      | 04                                              |
| 09  | Capacity                                                         | 03, 08                                          |
| 10  | Federation: peers' users attend, one stream per peer             | 04, [federation](../federation/README.md)       |
| 11  | SPA, audience side                                               | 02–07                                           |
| 12  | SPA, host and moderator side                                     | 02–07                                           |
| 13  | Media host: SFU on a separate host, `deploy/media.sh`            | 04                                              |
| 14  | End-to-end encrypted audio                                       | 04                                              |
| 15  | Recording backup: chunks held across peers                       | 08, [coverage](../coverage/README.md)           |

00 decides whether v1 ships over the WebSocket. 13, 14 and 15 come after v1 and
are independent of each other.

---

## Status

**Proposed.**

| Step  | Status   |
| ----- | -------- |
| 00–15 | Proposed |

## Locked decisions

| Topic                | Decision                                                                                                                                                                                                                               |
| -------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Name                 | **Town hall**                                                                                                                                                                                                                          |
| Who schedules        | **Admins only.** Scheduling, editing and cancelling are admin actions                                                                                                                                                                  |
| Organizer            | The admin who scheduled the town hall                                                                                                                                                                                                  |
| Scheduled            | Every town hall has a topic and a start time. Times are shown in the viewer's timezone                                                                                                                                                 |
| Lifecycle            | **Scheduled → lobby → live → ended.** **Only an admin can start it**, so an admin, and a recorder, is always there. It can start early. Admins can reschedule or cancel. The host or moderator ends it. A maximum duration ends it too |
| Announcement         | A **town hall record** signed by the scheduling admin and countersigned by the server. **Everyone is notified** when it's scheduled                                                                                                    |
| RSVP                 | Optional. **Only those who RSVP get reminders** ("starting soon"). An RSVP reserves nothing                                                                                                                                            |
| Published notice     | When the recording is published, those who attended or RSVP'd are notified                                                                                                                                                             |
| Roles                | Per town hall, not tied to server roles: **host**, **moderator**, **panelists**, **audience**. The host and moderator can be different people                                                                                          |
| Host                 | Introduces the topic and answers. Mic always open                                                                                                                                                                                      |
| Moderator            | Runs the queue and the floor. Mic always open                                                                                                                                                                                          |
| Panelists            | Mic always open: they can jump in whenever they want. Admins are panelists by default; a guest can be added without being made an admin                                                                                                |
| Audience             | Listens. Raises a hand to speak                                                                                                                                                                                                        |
| Muting               | Every open-mic role can mute itself. The moderator can mute anyone, the host included                                                                                                                                                  |
| Consent              | The moderator **grants** the floor; the speaker turns their own mic on. Nobody is ever unmuted by someone else                                                                                                                         |
| Floor enforced       | By the server: it forwards audio only from open-mic roles and the granted speaker. A modified client gains nothing by unmuting                                                                                                         |
| Raising a hand       | **Just a hand, no note.** Everyone in the queue sees their position. Lowering the hand or leaving removes it. A brief reconnect keeps the place                                                                                        |
| Queue                | The moderator reorders it, grants the floor, and revokes it                                                                                                                                                                            |
| Turns                | Optional per-turn time limit. The speaker can hand the floor back ("done")                                                                                                                                                             |
| Moderator gone       | If the moderator disconnects, queue control falls back to the host                                                                                                                                                                     |
| Attendees            | **Everyone sees the list** of who's in the town hall                                                                                                                                                                                   |
| Side chat            | Runs alongside the audio. **Moderators turn it on or off.** Each comment is at most **140 characters** (counted with `countCharacters`)                                                                                                |
| Chat signing         | Each comment is **signed by its author**; clients verify before showing it                                                                                                                                                             |
| Chat afterwards      | Gone when the town hall ends, unless it's recorded: then it's **published with the recording, timestamped** to play in sync                                                                                                            |
| Removal              | The moderator removes someone from the town hall. The server stops forwarding their audio and chat at once                                                                                                                             |
| Recording            | **Configurable** per town hall. If on, every participant is told before joining                                                                                                                                                        |
| Who records          | **Every admin present**, on their own device, for redundancy. **Never the server**: it keeps no audio                                                                                                                                  |
| Choosing             | Each recorder reports what its recording covers, gaps included. The organizer or the moderator picks one, even if none is complete                                                                                                     |
| Other recordings     | **Never discarded automatically.** Each admin decides whether to delete their own                                                                                                                                                      |
| Publishing           | The chosen recording is published as signed content and **kept on every admin's device**                                                                                                                                               |
| Peer backup          | It's also **chopped into chunks** held across the network, torrent-style, as a backup. **At most 2 MB per holder per town hall**                                                                                                       |
| Chunk holders        | The community's members, and **peers' users who attended**. A peer gets each chunk once and assigns it to its own attendees (fold at the border)                                                                                       |
| Recording format     | Mono Opus at **16 kbps**: about 7.2 MB an hour                                                                                                                                                                                         |
| Chunk size           | Fixed **256 KB**: 8 chunks per holder at the 2 MB cap. A signed manifest lists each chunk's hash                                                                                                                                       |
| Copies               | `r = min(r_need, r_max)`: `r_need = ceil(ln(1 − A) / ln(1 − p))` for **A = 99%** without admins, `r_max = floor(holders × 2 MB / size)`. Below `r_need`, admins cover the gap                                                          |
| Repair               | When a holder's account is removed or their device evicts chunks, the server re-copies them to new holders                                                                                                                             |
| Later                | Erasure coding (any `k` of `n` fragments rebuild it) instead of whole copies: about half the storage for the same availability                                                                                                         |
| Serving              | Like reeds: an online non-admin holder first, an admin only if no one else is online                                                                                                                                                   |
| Transcript           | Generated on the device that publishes the chosen recording, and published with it                                                                                                                                                     |
| Capacity             | **None by default.** Admins can set one. A capped town hall must be recorded, so those who couldn't get in can catch up                                                                                                                |
| Federation           | **Configurable** per town hall: **community only**, or **open to every connected peer's users**                                                                                                                                        |
| Peers' notifications | Each peer **notifies all its users** of an open town hall, from the one announcement it received                                                                                                                                       |
| Peers' users         | Attend like local members: they can raise a hand and speak, and chat                                                                                                                                                                   |
| Bans                 | A user banned from the host server **can't join** its town halls at all. An exception to bans never refusing reads                                                                                                                     |
| Fold at the border   | A peer's users cost the host server **one stream per active speaker per peer**, never one per remote listener. The peer distributes to its own users                                                                                   |
| Devices              | One device per user, as everywhere. No multi-device support                                                                                                                                                                            |
| Transport, v1        | Opus frames from WebCodecs over the existing protobuf WebSocket, if 00 holds up. No UDP, works behind nginx and Cloudflare unchanged                                                                                                   |
| Transport, later     | A WebRTC SFU on a separate media host (13), set up by script with an IP and a confirmation. The home server's reachability and upload stop mattering                                                                                   |
| E2E audio            | Compatible with recording: admins record what their own devices decrypt                                                                                                                                                                |
| Members' routers     | Never need configuring. Clients always connect out to the server                                                                                                                                                                       |
| Out of scope         | Video, screen sharing, breakout rooms, live captions                                                                                                                                                                                   |

## Open questions

| Topic           | Proposal                                                                                                                                                                                                                                                                                                                   |
| --------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Online fraction | Today presence (`online_users`) vanishes on disconnect, so nothing records it. Add a durable `last_seen_at` per user, set on connect and disconnect. Holders seen in the last 7 days are **active**; the server samples `online / active` hourly and `p` is the 7-day average. Holders are picked most recently seen first |
