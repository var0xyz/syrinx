# Admin actions 15 — Invite grants, and root's unlimited invites

## Status

Implemented.

## Depends on

[01](01_requests_and_approvals.md), [02](02_ledger.md),
[invites 02](../invites/02_lifecycle_api.md)

## Context

`MAX_INVITES_PER_USER` gives every member the same lifetime cap. Two cases
don't fit it:

- **Root** bootstraps the community and has to be able to bring people in
  for as long as it runs. Root has **no invite limit**, whatever the cap.
- A member who is bringing in a group (their team, a club) needs more than
  the cap. Admins can **give them more invites**.

## Root

Root's invites are never counted against `MAX_INVITES_PER_USER`. The check
is by identity (`isRootIdentity`), not by role, the same way root's
permissions are implied.

## The `invites` action

| Field | Value |
|-------|-------|
| Action | `invites` |
| Permission | `user.invites`, held by requester and approver |
| Targets | Exactly one local member or admin (never root, who has no limit) |
| Body | How many invites to add, written as a plain decimal from `1` to `100` (`maxInviteGrant`). `07`, `+5` or `1e1` are refused, so the signed bytes have one spelling |
| Reason | Required and public, like every action but messages |

It goes through the usual request → approval pipeline. Granting invites to
an admin is acting on an admin, so root approves it. A direct invitee of
the requester may approve: a grant removes nothing.

A grant is refused with `409` when the server has no cap
(`MAX_INVITES_PER_USER` unset or `-1`), when it is opened and again at
approval.

### Grants add up

Each executed grant adds its count to the target's limit:

```
limit = MAX_INVITES_PER_USER + Σ body of executed `invites` requests naming the user
```

Nothing new is stored. The executed request is the grant, so the limit is
read from `admin_requests` / `admin_request_targets` /
`admin_request_content` (`grantedInvitesSQL` in `admin_invites.go`), and a
server rebuilt from its admin records gets the same limits back. Raising
`MAX_INVITES_PER_USER` later raises everyone's limit by the same amount,
grants included.

There is no action that takes invites away. Revoking an unused invite still
doesn't free a slot.

### Enforcement

`insertInviteWith` computes the creator's limit inside the transaction,
after the per-creator advisory lock, so a grant approved during a burst of
creates is seen by the next one.

## Ledger

One entry when the request closes as executed:

```
[<requestID>] Invite grant: <count> more. Reason: <reason>
```

## API

`GET /api/invites/quota` (signature auth) answers `InviteQuota { limit }`:
how many invites the caller may create in all, revoked ones included. `-1`
means no limit (root, or a server without a cap).

## SPA

- **Admin actions modal**: "Give more invites", shown when the viewer holds
  `user.invites` and the server has a cap. A number field (1–100) and the
  reason.
- **Request list**: the `invites` badge, and "Lets them create N more
  invites than the server allows."
- **Invites panel**: starts from root's unlimited state or the server's
  cap, then shows the caller's own limit from `/invites/quota`.

## Schema

`'invites'` joins the `admin_requests.action` check, and `'user.invites'`
the `user_permissions` / `invite_permissions` checks. No new table or
column.

## Tests

`admin_invites_test.go`:

- Root creates invites past the cap; its quota reads `-1`.
- A grant raises only its target's limit, by its count, once approved; the
  ledger has its entry.
- Bodies other than `1`–`100` in plain decimal, two targets, a requester
  without `user.invites`, and a server without a cap are all refused.
