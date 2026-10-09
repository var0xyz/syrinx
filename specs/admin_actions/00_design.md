# Admin actions 00 — Design + locked model

## Status

Proposed.

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

Every action (except [reserved usernames](#reserved-usernames) and flag
review) is a **request** with two signatures:

1. The **requester** (admin or root) picks the action and targets, writes a
   reason, and signs the request.
2. An eligible **approver** signs an approval over the same request.
3. The server executes it, countersigns with a server-authoritative
   timestamp, and writes the result to the [ledger](#public-ledger).

A request can also be **rejected** by any eligible approver, or
**withdrawn** by its requester. Both are final and go in the ledger with
the reason.

**Approver eligibility**, checked when the approval is submitted, not when
the request was opened:

| Rule                                                                                               | Why                                                           |
| -------------------------------------------------------------------------------------------------- | ------------------------------------------------------------- |
| Role is admin or root                                                                              | —                                                             |
| Not the requester, **except root**                                                                 | Root approving its own request matches federation today       |
| Not in the requester's **invite subtree** (anyone the requester invited, directly or further down) | Stops a requester inviting a puppet admin to approve for them |
| Not a target of the request                                                                        | —                                                             |
| **Root**, if any target is an admin                                                                | Only root acts on admins                                      |

No request may target root.

Requests **never expire**. A target that leaves before approval (self-
deletion, or a kick from another request) **drops out** of the request. A
request with no targets left is **moot**. It is closed and listed as moot
in the ledger, with no approval needed.

### Public ledger

Every executed, rejected, withdrawn or moot request is an entry that **every
local member** can read: action, targets, requester, approver, reason,
outcome and server timestamp. Entries are the signed request and approval
plus the server countersignature, so a member can verify them, and can see
that an approval came from a real admin, the same way they would any other
signed record.

Flags and reserved usernames are **not** ledger entries.

### Suspend / reinstate

**Reversible and local.** While a user is suspended, the server:

- rejects every signed operation from them in the auth middleware (the same
  place that rejects revoked keys today);
- refuses their WebSocket handshake.

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
2. both signers have a server-countersigned **admin or root role**
   (profile countersignature);
3. requester and approver differ, unless the requester is root;
4. the server countersignature verifies against that server's key, by
   fingerprint.

This stops a single rogue admin or one stolen admin key. It does **not**
stop a compromised server: peers learn roles only from the server's profile
countersignature, so such a server can mint two "admins" of its own. That
is visible in the public ledger, but not prevented. See
[Open questions](#open-questions).

**Fanout.** The removal follows the existing account-removal path: live
dispatch plus `SYNC_REQUEST` catch-up locally, and **one delivery per peer**
for federated holders.

The kicked user's own unclaimed invites are revoked.

### Promote / demote

`user → admin` and `admin → user`, approved like any other request. Demoting
an admin is acting on an admin, so root must approve. The new role goes on a
fresh profile countersignature, as at signup today. Root's role never
changes.

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
3. The user downloads `<userID>.pub.txt.gz`: the armored public key as plain
   text, gzip-compressed, with the user ID in the filename.

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

**Root only**, no approval, not in the ledger. Matching is case-insensitive,
the same as the existing `LOWER(username)` uniqueness. A reservation only
blocks **future** signups and renames; a user who already holds the name
keeps it.

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

### Broadcasts

An admin writes a message for every **local** member. On approval, the
server encrypts it into each member's mailbox. Broadcasts are not reeds:
they don't appear in feeds, can't be replied to, and never reach peers.
Each broadcast is a ledger entry; its body is the reason.

## Steps (outline)

| #   | Scope                                                                                                                        |
| --- | ---------------------------------------------------------------------------------------------------------------------------- |
| 01  | `admin_requests` / approvals schema, signed request + approval payloads (`canonicalJSON`), eligibility checks, moot handling |
| 02  | Ledger read API (members only), verification on the SPA side                                                                 |
| 03  | `users.suspended`, auth-middleware + WebSocket refusal, suspend/reinstate execution                                          |
| 04  | Invite-tree query, kick execution, dual-admin removal certificate, holder verification, fanout, invite revocation            |
| 05  | Role change execution + fresh profile countersignature                                                                       |
| 06  | Lost-device page, file format, upload endpoint, restricted key enforcement, admin-rebound revocation, vouch warning          |
| 07  | `reserved_usernames`, signup/rename check, root-only endpoints                                                               |
| 08  | `reed_flags`, flag API, visibility rules                                                                                     |
| 09  | Broadcast execution into `user_mailbox`                                                                                      |
| 10  | Admin SPA: requests, approvals, tree picker, ledger, flags, broadcasts, reserved names                                       |

## Security notes

- The kick removal is the first purge not signed by the author. Add it to
  `RISKS.md` when step 04 lands, with the two-signature requirement as
  the mitigation.
- Admin rebind is an account takeover in the hands of two admins. The
  mitigations are the two-person rule, the public ledger, the visible
  admin-rebound revocation and the vouch reset; record it in `RISKS.md`
  with step 06.
- Approver eligibility is checked when the approval is submitted. An admin
  the requester invites after opening the request is still in their
  subtree, so it can't approve.

## Open questions

- **Role provenance.** Should a verifier trace an admin's role back to its
  origin (an admin-signed invite or an approved promotion, back to root)
  instead of trusting the server's profile countersignature? That would
  make a kick unforgeable without real admin keys, at the cost of a longer
  chain to fetch and verify.
