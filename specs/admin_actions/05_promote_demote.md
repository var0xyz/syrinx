# Admin actions 05 — Promote, demote and permissions

## Status

Proposed.

## Depends on

[01](01_requests_and_approvals.md), [02](02_ledger.md);
[roles 03](../roles/03_profile_role.md)

## Context

Today the admin role can only come from an admin invite at signup, and it
can never be taken away. This step adds both directions and lets an
admin's permissions ([01](01_requests_and_approvals.md)) change, under the
same two-person rule.

## Executors

| Action | Permission | Precondition | Effect |
|--------|------------|--------------|--------|
| `promote` | `admin.create` | Target role is `user` | `role = admin`; `user_permissions` = the request's permission set |
| `permissions` | `admin.create` to add, `admin.demote` to remove | Target role is `admin` | `user_permissions` = the request's permission set |
| `demote` | `admin.demote` | Target role is `admin` | `role = user`; `user_permissions` cleared |

The permission set is the request's `body`
([01](01_requests_and_approvals.md)). A `promote` may grant an empty set: an
admin without permissions can still review flags. Nobody grants a
permission they don't hold, and the approver must hold every permission the
request grants too.

Root is never a target ([01](01_requests_and_approvals.md)). A
`permissions` change or a demotion is acting on an admin, so approver
eligibility already requires root.

## Admin invites

An admin invite carries the permissions it grants, stored in
`invite_permissions`. Creating one needs `admin.create`, and an inviter can
only grant permissions they hold. It needs no approval, but it is logged
when created, with its permission set, and when claimed
([02](02_ledger.md)). On claim, the permissions move to `user_permissions`.

The inviter doesn't sign the permission set: the invite payload is
unchanged. The ledger entry the server writes when the invite is created is
the public record of what it grants.

## SPA

- **Invites:** picking **Admin** in the new-invite modal opens a second
  screen with a checkbox per permission. Only the permissions the viewer
  holds are listed.
- **Promote / change permissions:** the profile's admin modal shows the
  same picker, pre-filled with the target's current permissions for a
  change.

## Profile countersignature

The role is bound on the profile server countersignature
([roles 03](../roles/03_profile_role.md)). The executor re-countersigns the
target's current profile with the new role and a fresh server timestamp,
the same way a profile update does, and pushes it through the existing
profile-update fan-out. Newest server timestamp wins, so holders pick up the
new role.

The user's own profile signature is unchanged: users never sign their role.

## Side effects of demotion

- The demoted user's pending requests stay valid and can still be approved:
  they held the permission when they opened them
  ([01](01_requests_and_approvals.md)). They can't approve anything
  themselves anymore.
- Unclaimed admin invites they created are revoked. Their unclaimed user
  invites stay.
- Past approvals and requests stay valid. Kick certificates they signed were
  made while they were an admin, and the ledger records that.

## Tests

- Promote: the role changes, the permissions are stored, and the profile is
  re-countersigned with a newer timestamp.
- Promote granting a permission the requester or approver lacks is refused.
- Permissions: adding needs `admin.create`, removing needs `admin.demote`,
  and root must approve.
- Demote: needs root to approve; an admin approver is refused; permissions
  are cleared.
- Demote revokes unclaimed admin invites only.
- Admin invite: creating one without `admin.create`, or granting a
  permission the inviter lacks, is refused; claiming it stores the
  permissions and writes a ledger entry.
