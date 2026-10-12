# Admin actions 10 — Admin SPA

## Status

Proposed.

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
| `/admin/actions` | A pill switch between **Requests** and **Flagged**, like the search page. Requests: pending requests (action, targets, requester, reason) with **Approve** / **Reject** when the viewer is eligible, a disabled button with the reason when not, **Withdraw** on the viewer's own; closed requests below. Flagged: open flags ([08](08_flags.md)) |
| `/admin/broadcast` | Compose a message for **everyone** or **one user** → opens a `broadcast` or `message` request ([09](09_broadcasts.md)); the viewer's requests awaiting approval below |
| `/admin/mesh` | Server connections (moved from `/network/mesh`) |

Not in the nav:

| Route | Shows |
|-------|-------|
| `/admin/reserved-usernames` | Root only ([07](07_reserved_usernames.md)) |

**Invites** is a top-level nav item for every member, admins included.

On the profile of a **local** user other than root, admins see an **Admin**
button (the shield icon, `admin-16.svg`, and its label) next to Follow and
Block. It opens a modal listing every
action they can take on that user: suspend or reinstate, kick, promote or
demote, add a key, send a private message. Picking one asks for what that
action needs (invite-tree targets for suspend and kick, the key file for a
rebind, the message for a private message, a reason for the rest), then
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
- Promote, demote, add a key and private messages act on the profile's user
  only.

## Eligibility hints

The SPA mirrors the [01](01_requests_and_approvals.md) rules to decide which
buttons to show. The server's check is the one that counts: a 403 on decision
shows the rule that failed and leaves the request untouched.

## Offline

Admin screens are online-only. Pending requests change as other admins act,
and every action needs the server. The ledger stays offline-first
([02](02_ledger.md)).

## Tests (Playwright)

- An admin opens a kick on a user and two invitees. A second admin who isn't
  in the first admin's subtree approves it. The ledger shows the entry, and
  the targets' content disappears for a follower.
- The approve button is disabled for the requester and enabled for root.
- A non-admin doesn't see the admin nav.
