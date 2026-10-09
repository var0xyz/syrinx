# Admin actions 05 — Promote / demote

## Status

Proposed.

## Depends on

[01](01_requests_and_approvals.md), [02](02_ledger.md);
[roles 03](../roles/03_profile_role.md)

## Context

Today the admin role can only come from an admin invite at signup, and it
can never be taken away. This step adds both directions, under the same
two-person rule.

## Executors

| Action | Precondition | Effect |
|--------|--------------|--------|
| `promote` | Target role is `user` | `role = admin` |
| `demote` | Target role is `admin` | `role = user` |

Root is never a target ([01](01_requests_and_approvals.md)). Demoting
is acting on an admin, so approver eligibility already requires root.

## Profile countersignature

The role is bound on the profile server countersignature
([roles 03](../roles/03_profile_role.md)). The executor re-countersigns the
target's current profile with the new role and a fresh server timestamp,
the same way a profile update does, and pushes it through the existing
profile-update fan-out. Newest server timestamp wins, so holders pick up the
new role.

The user's own profile signature is unchanged: users never sign their role.

## Side effects of demotion

- The demoted user's pending requests stay. They can't approve anymore, and
  their requests can only be rejected or withdrawn
  ([01](01_requests_and_approvals.md)).
- Unclaimed admin invites they created are revoked. Their unclaimed user
  invites stay.
- Past approvals and requests stay valid. Kick certificates they signed were
  made while they were an admin, and the ledger records that.

## Tests

- Promote: the role changes and the profile is re-countersigned with a newer
  timestamp.
- Demote: needs root to approve; an admin approver is refused.
- Demote revokes unclaimed admin invites only.
