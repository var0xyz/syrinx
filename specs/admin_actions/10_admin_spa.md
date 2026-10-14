# Admin actions 10 — Admin SPA

## Status

Implemented.

## Depends on

[01](01_requests_and_approvals.md)–[09](09_broadcasts.md)

## Context

Every admin action so far is API only. This step adds the admin screens. The
public ledger page belongs to [02](02_ledger.md), and the flag menu item to
[08](08_flags.md).

## Routes

The side nav has an **Admin** section, shown only when the viewer's role is
admin or root, with three items:

| Route | Shows |
|-------|-------|
| `/admin/actions` | A pill switch between **Requests** and **Flagged**, like the search page. Requests: pending requests other than broadcasts and messages (action, targets, requester, reason) with **Approve** / **Reject** when the viewer is eligible, a disabled button with the reason when not, **Withdraw** on the viewer's own; closed requests below. Flagged: open flags ([08](08_flags.md)) |
| `/admin/broadcast` | Compose a message for **everyone** or **one user** → opens a `broadcast` or `message` request ([09](09_broadcasts.md)); below it every pending broadcast and message request, with the same buttons as Actions, then the closed ones. Each option shows only with its permission. The recipient field autocompletes this server's members like an @-mention |
| `/admin/mesh` | Server connections (moved from `/network/mesh`). Inviting, approving and disconnecting each show only with `mesh.invite`, `mesh.connect` and `mesh.disconnect` |

Not in the nav:

| Route | Shows |
|-------|-------|
| `/admin/reserved-usernames` | Root only ([07](07_reserved_usernames.md)) |

**Invites** is a top-level nav item for every member, admins included.

On the profile of a **local** user other than root, admins see an **Admin**
button (the shield icon, `admin-16.svg`, and its label) next to Follow and
Block. It opens a modal listing every
action they can take on that user: promote, change permissions or demote,
add a key, send a private message, suspend or reinstate, kick. Only the
actions whose permission the viewer holds are listed
([01](01_requests_and_approvals.md)). Picking one asks for what that
action needs (invite-tree targets for suspend and kick, the key file for a
rebind, the message for a private message, the permission picker for a
promotion or a permissions change, a reason for the rest), then
signs and opens the request. **Act on {username}** on a flagged reed opens the same
modal.

## Invite-tree picker

In the profile's admin modal, for suspend and kick:

- The tree from `GET /api/admin/users/{userID}/invite-tree`, drawn like a
  thread: the inviter on top, the user below it, invitees nested under
  whoever invited them.
- A checkbox per node. The chosen user is pre-checked. Root has no checkbox.
  Admins have one only when the viewer is root, and a lock icon otherwise.
- A reason is required. A kick shows a summary ("Kick 3 accounts and all
  their content; this can't be undone") before it signs and opens the
  request.
- Promote, permission changes, demote, add a key and private messages act
  on the profile's user only.

## Permission picker

A checkbox per permission, listing only the ones the viewer holds, and a
**Select all** checkbox above them. Selecting all still grants each
permission by name; there is no stored "all". It
appears in the profile's admin modal for a promotion (empty) or a
permissions change (pre-filled with the target's current set), and as a
second screen of the new-invite modal when **Admin** is picked
([05](05_promote_demote.md)).

## Permissions in the SPA

`GET /api/admin/users/{userID}/permissions` returns a user's role and
permissions: an admin may read anyone's, every user their own. The SPA
reads a target admin's to pre-fill a permissions change.

The SPA reads the viewer's own permissions once per app load, in the
background, and keeps them in `localStorage` for the session and the next
load. Pages render from that copy without waiting or checking; if the read
fails, the last copy stays. The server checks every action, so a stale copy
only shows or hides a button. **Broadcast** is left out of the nav and the
section tabs for an admin who holds neither `message.broadcast` nor
`message.user`.

## Eligibility hints

The SPA mirrors the [01](01_requests_and_approvals.md) rules, permissions
included, to decide which buttons to show. The server's check is the one that counts: a 403 on decision
shows the rule that failed and leaves the request untouched.

## Offline

Open requests always come from the server: they change as other admins
act, and deciding needs the server anyway. Closed requests never change, so
the SPA keeps them in IndexedDB (`adminRequests`), each verified before it's
stored (the requester's and decider's signatures and the server's
countersignature), and renders them from there. Each visit pages the
server's closed list back until a page holds nothing new, then fetches any
request this device saw open last time that has since closed. The ledger
stays offline-first ([02](02_ledger.md)).

## Tests (Playwright)

`tests/admin-actions.spec.ts`, against a fresh server with a minted root.
It needs `SYRINX_SERVER_KEY_FILE` (the server's public key, from
`ops print-key`), `SYRINX_ROOT_BUNDLE` (root's `syrinx-1-….sxi.gpg`) and
`SYRINX_ROOT_PASSPHRASE`, and skips without them. `SYRINX_E2E_BASE_URL`
points it at another SPA port.

- An admin opens a kick on a user and two invitees. Root, whom the admin
  didn't invite, approves it. The ledger shows both entries, and the
  targets' content disappears for a follower.
- The requester can only withdraw their own request; root can approve it.
- A non-admin doesn't see the admin nav.
- A lost device mints a key, an admin uploads it, root approves, and the
  device finishes recovery signed in as the member.
- A member flags a reed and can't flag it again; an admin dismisses the
  flag with a reason and it moves to the dismissed section. A suspended member sees the suspended screen. An approved broadcast
  reaches a member's mailbox.
