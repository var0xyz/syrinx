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

Shown in the side nav only when the viewer's role is admin or root:

| Route | Shows |
|-------|-------|
| `/admin/requests` | Pending requests: action, targets, requester, reason. **Approve** / **Reject** (with reason) when the viewer is eligible, a disabled button with the reason when not, **Withdraw** on the viewer's own |
| `/admin/users/{userID}` | Entry point for acting on a user: invite tree + action bar |
| `/admin/rebind` | Upload `<userID>.pub.txt.gz`, compare fingerprint, write reason |
| `/admin/broadcast` | Compose message → opens a broadcast request |
| `/admin/flags` | Open flags ([08](08_flags.md)) |
| `/admin/reserved-usernames` | Root only ([07](07_reserved_usernames.md)) |

Profiles get an **Admin** item in their menu that leads to
`/admin/users/{userID}`.

## Invite-tree picker

On `/admin/users/{userID}`:

- The tree from `GET /api/admin/users/{userID}/invite-tree`, drawn like a
  thread: the inviter on top, the user below it, invitees nested under
  whoever invited them.
- A checkbox per node. The chosen user is pre-checked. Root has no checkbox.
  Admins have one only when the viewer is root, and a lock icon otherwise.
- Action bar: **Suspend**, **Reinstate**, **Kick**, **Promote**, **Demote**.
  Each is enabled only when it fits every checked node. Promote and demote
  are single-target.
- Choosing an action asks for a reason (required), shows a summary ("Kick 3
  accounts and all their content; this can't be undone"), then signs and
  opens the request.

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
