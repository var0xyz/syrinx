# Admin actions 00 — Design + locked model

## Status

Implemented.

## Depends on

[roles](../roles/README.md) (`users.role`, role bound on the profile
countersignature).

## Context

Roles exist in code, but an admin can do nothing to an account today. A
closed community needs a way to remove people who misbehave, pause someone
while things are sorted out, grow and shrink its set of admins, and help a
member who lost their only device. It also needs those powers to be
**accountable**: one admin alone must not be able to act, and everyone must
be able to see what was done and why.

Two Syrinx properties shape everything below:

- **The server holds no content.** Removing an account's reeds means a
  signed removal that holders verify and honour. Nothing is deleted on the
  server.
- **Trust is cryptographic.** `docs/deletion.md` lets clients purge content
  only on the **author's** signature, so a compromised server can't wipe the
  distributed store. Kicks are the one exception, and they replace the
  author's signature with **two admin signatures**, not with the server's
  word (see [Kick](#kick)).

## Non-goals

- Removing, hiding or de-listing individual reeds.
- Refusing a remote user's content, or any action on another server's user.
- Automatic actions (rate limits, spam heuristics, flag thresholds).
- Linking an action to a flag.
- Expiring requests or suspensions.

## Design

### Requests and approvals

Every action (except flag review) is a **request** with two signatures:

1. The **requester** (admin or root) picks the action and targets, writes a
   reason, and signs the request.
2. An eligible **approver** signs an approval over the same request.
3. The server executes it, countersigns with a server-authoritative
   timestamp, and writes the result to the [ledger](#public-ledger).

The server also countersigns the request when it is opened, which stamps
when it was proposed. A request can also be **rejected** by any eligible
approver, or **withdrawn** by its requester. Both are final and stay out of
the ledger.

**Permissions.** An admin can do only what their permissions allow, and an
admin can only grant permissions they hold. Root holds every permission.

| Permission          | Allows                                               |
| ------------------- | ---------------------------------------------------- |
| `admin.create`      | Promoting, granting permissions, admin invites       |
| `admin.demote`      | Demoting, removing permissions                       |
| `user.kick`         | Kicking                                              |
| `user.suspend`      | Suspending and reinstating                           |
| `user.rebind`       | Adding a key                                         |
| `message.unicast`   | Private messages                                     |
| `message.broadcast` | Broadcasts                                           |
| `mesh.invite`       | Creating and canceling server invites                |
| `mesh.connect`      | Approving or rejecting a server connection           |
| `mesh.disconnect`   | Disconnecting from a peer                            |

Both the requester and the approver need the action's permission. A request
stays valid when its requester later loses the permission: they held it
when they opened it. Reviewing flags needs no permission.

**Approver eligibility**, checked when the approval is submitted, not when
the request was opened:

| Rule                                                                                               | Why                                                           |
| -------------------------------------------------------------------------------------------------- | ------------------------------------------------------------- |
| Role is admin or root                                                                              | —                                                             |
| Holds the action's **permission**                                                                  | —                                                             |
| Not the requester, **except root**                                                                 | Root approving its own request matches federation today       |
| On a kick or a permission change (promote, demote, permissions): not invited **directly** by the requester | Stops a requester inviting a puppet admin to approve for them. Only one level, since most admins descend from a few inviters |
| Not a target of the request                                                                        | —                                                             |
| **Root**, if any target is an admin                                                                | Only root acts on admins                                      |

No request may target root.

Requests **never expire**. A target that leaves before approval (self-
deletion, or a kick from another request) **drops out** of the request. A
request with no targets left is **moot**. It is closed and listed as moot
in the ledger, with no approval needed.

### Public ledger

Every admin action is logged, and every **signed-in local member** can read
the log. The only admin action left out is a private message. Logged:

- every approved request: kicks, suspensions and reinstatements,
  promotions and demotions, key rebinds (an admin adding a key to an
  account) and broadcasts. Pending, rejected, withdrawn and moot requests
  aren't public: a pending kick could push its target to misbehave;
- mesh: a server invite created or canceled, a connection approved or
  rejected by this server's admin (whatever the peer did on their side),
  and a disconnection;
- claimed admin invites, with the inviter, the new admin and the
  permissions granted;
- server key rotations, with their reason, credited to the server operator
  rather than a member.

Each entry is **signed by the server**: its ID, the ID and signature of the
entry before it, the server timestamp, the user ID of the admin who acted,
a message built by the server, the user IDs the entry is about, and its
content (a broadcast's text, or a permission set). Messages never carry
usernames. An approved request is one entry, with the reason, the
requester and approver, and when it was proposed and approved. Every member
gets each new entry over the WebSocket and stores it locally. That local
copy is the guarantee: the server can't rewrite or drop an entry without
members noticing when they compare. New members backfill the whole ledger
from a paginated endpoint at signup.

Private messages and flags are **not** ledger entries.

### Suspend / reinstate

**Reversible and local.** While a user is suspended, the server:

- rejects every signed operation from them in the auth middleware (the same
  place that rejects revoked keys today), ledger reads included;
- refuses their WebSocket handshake.

Both refusals carry the **suspension certificate**: the approved request
with its reason, both admin signatures and the server's. That is how the
user learns why; they see nothing else of the network.

Nothing is sent to peers, and nothing changes for them. The account, keys,
follows and content stay as they are. Their reeds stay wherever they are
already held; the user's own device just can't serve them while the
WebSocket is refused.

Only a **reinstate** request, approved like any other, lifts a suspension.

### Kick

**Final.** The account is removed, with **all** its content, and its
username is freed. The person can sign up again with a new invite, as a new
user ID.

**Target selection.** The admin opens the kick screen on one user and sees
that user's **invite tree** as a thread: their inviter on top, the user,
then everyone they invited, nested. The admin ticks who goes. That can be
the user alone, their invitees, the inviter (inviting someone carries
responsibility for them), or any mix. Suspend uses the same screen.

**Removal certificate.** A kick produces one removal per target, shaped like
a self-deletion's `account_removals` certificate, but the author signature
is replaced by:

- the **requester's** signature over the request;
- the **approver's** signature over the approval;
- the server countersignature (timestamp, server-key fingerprint), as today.

The removal binds the target user ID, both admin IDs and key IDs, the
reason, and the request ID. A holder (local or peer) purges a kicked
account's reeds only if:

1. both admin signatures verify against keys those admins held when the
   request and approval were signed;
2. requester and approver differ, unless the requester is root;
3. the server countersignature verifies against that server's key, by
   fingerprint.

Holders don't check the admins' roles or permissions: the home server
checked them when the kick was approved. Its local members can check that
afterwards by replaying the ledger, which records every promotion,
permission change, demotion and admin invite.

This stops a single rogue admin or one stolen admin key. It does **not**
stop a compromised server, which can mint two "admins" of its own. That is
visible in the public ledger, but not prevented.

**Fanout.** The removal follows the existing account-removal path: live
dispatch plus `SYNC_REQUEST` catch-up locally, and **one delivery per peer**
for federated holders.

The kicked user's own unclaimed invites are revoked.

### Promote / demote / permissions

`user → admin` and `admin → user`, approved like any other request. A
promotion carries the new admin's permissions, and a separate request
changes an existing admin's permissions. Demoting an admin or changing
their permissions is acting on an admin, so root must approve. The new role
goes on a fresh profile countersignature, as at signup today. Root's role
never changes.

An admin invite grants the admin role with the permissions the inviter
picks, from among their own. It needs no approval, but its creation and its
claim are both logged.

### Lost-device key rebind

For a user who lost their only device **and** has no key backup. Users who
still have a `.sxi.gpg` use account recovery as today.

**Lost-device page** (public, no session):

1. The user enters their **user ID**, with or without `@serverID`; a bare ID
   gets this server's ID appended. There is no username lookup, and the
   page never tells anyone whether an ID exists. A user who doesn't
   remember their ID asks an admin, whom they have to contact anyway.
2. The client mints a key pair and keeps the private key locally as a
   **pending rebind**.
3. The user downloads `<userID>.pub.txt`: the armored public key as plain
   text, with the user ID in the filename.

The file is not signed. Nothing proves who made it, and that's on purpose:
the admin's out-of-band check (in person, a call, a known channel) is the
trust, which is why it needs two admins.

**Admin side.** The admin uploads the file. The UI shows the user from the
filename and the key's fingerprint, so the admin can read it back to the
user. The admin writes a reason, which opens a rebind request.

**On approval**, the server:

1. revokes the user's active key as **lost**, not compromised, so old reeds
   stay valid, marked **admin-rebound** and binding the request ID;
2. binds the uploaded key as the user's key, flagged **restricted**.

**Restricted key.** Its only valid operation is a revoke-with-successor
that rotates to a new key. The auth middleware refuses everything else it
signs, reeds included. When the pending-rebind device next signs in, the
client immediately mints a fresh key and rotates to it, which ends the
restriction.

**Vouches.** Clients that hold a vouch for the old key see an
"admin-rebound" revocation and show a warning. The vouch does **not** carry
over to the new key; the voucher has to verify the person again.

### Reserved usernames

A short list fixed in code, plus the server's own name; nobody reserves or
releases names at runtime. Matching is case-insensitive, the same as the
existing `LOWER(username)` uniqueness.

`root` (case-insensitive) is reserved for the **root identity** permanently,
outside the reservation list: no one else can ever take it, even after root
renames, and root can always take it back.

### Flags

Any member can flag a **reed ID**. The server stores only the reed ID, the
reporter and an optional short note, and never the content. An admin
reviewing a flag opens the reed and loads it from the network like any
reader.

| Reed author | Who sees the flag                    |
| ----------- | ------------------------------------ |
| Normal user | All admins, including who flagged it |
| Admin       | All admins **except** that author    |
| Root        | Can't be flagged                     |

Flags are private and never appear in the ledger. Acting on an account is
always a separate request with its own reason; it never references a flag.

### Broadcasts and private messages

An admin writes a message either for every **local** member (a broadcast)
or for **one** local member (a private message). Both are requests and need
a second admin's approval. On approval, the server encrypts the message into
the recipients' mailboxes. Neither is a reed: they don't appear in feeds,
can't be replied to, and never reach peers.

Neither takes a reason. A broadcast's ledger entry, written on approval,
carries its message; a rejected one never reaches the ledger. A private message
is **never** in the ledger: only admins see it, through the pending and
closed requests list.

## Steps (outline)

| #   | Scope                                                                                                                        |
| --- | ---------------------------------------------------------------------------------------------------------------------------- |
| 01  | `admin_requests` / approvals schema, signed request + approval payloads (`canonicalJSON`), eligibility checks, moot handling |
| 02  | `ledger_entries`, server-signed entries, `LEDGER_ENTRY` WebSocket push, paginated backfill, local copy and comparison        |
| 03  | `users.suspended`, auth-middleware + WebSocket refusal, suspend/reinstate execution                                          |
| 04  | Invite-tree query, kick execution, dual-admin removal certificate, holder verification, fanout, invite revocation            |
| 05  | Role change execution + fresh profile countersignature                                                                       |
| 06  | Lost-device page, file format, upload endpoint, restricted key enforcement, admin-rebound revocation, vouch warning          |
| 07  | Fixed reserved-name list, signup/rename check                                                                                |
| 08  | `reed_flags`, flag API, visibility rules                                                                                     |
| 09  | Broadcast and private-message execution into `user_mailbox`                                                                  |
| 10  | Admin SPA: requests, approvals, tree picker, ledger, flags, broadcasts                                                       |

## Security notes

- The kick removal is the first purge not signed by the author. Add it to
  `RISKS.md` when step 04 lands, with the two-signature requirement as
  the mitigation.
- Admin rebind is an account takeover in the hands of two admins. The
  mitigations are the two-person rule, the public ledger, the visible
  admin-rebound revocation and the vouch reset; record it in `RISKS.md`
  with step 06.
- Approver eligibility is checked when the approval is submitted. An admin
  the requester invites directly after opening a kick or a permission
  change still can't approve it.

## Decisions

- **Role provenance.** Verifiers don't trace an admin's role back to its
  origin. They trust that the home server gated the action, and local
  members can audit that from the ledger.
