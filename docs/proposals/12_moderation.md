# Moderation (future)

## Status

Future — not planned for the PoC. This is a placeholder to capture intent,
not an accepted design.

## Depends on

—

## Context

The PoC deliberately focused on getting the **cryptographic foundation**
right: signed identities, signed reeds/countersignatures, signed key
revocations, and verify-before-store. Moderation was intentionally left out
so that trust and authenticity could be nailed down first.

There is currently no notion of a privileged user. Every account is equal,
and the server does not adjudicate content.

## Intent

In the future, Syrinx should support **admin users** with privileges to
moderate content. At minimum they would be able to:

- **Block users** — prevent a bad actor from continuing to interact
  (post reeds, follow, sign up peers via invites, etc.).

## Open questions

- **Should admins be able to delete reeds?** Unresolved. This sits in
  tension with the project's core stance on data permanence (see the
  README, "Why would I use a system that can't delete my data if I want
  to?") and with the signed-deletion trust model, where a reed is only
  removed via the **author's** signature plus a server countersignature
  (see [deletion/00_design.md](deletion/00_design.md)). Server-initiated
  removal would be a new trust layer and needs its own design before it is
  adopted, if ever.
- **What does "block" mean cryptographically?** Blocking is a server-side
  gate on API access; it does not (and cannot) retract content already
  distributed to peers. The scope and attestation of a block need to be
  defined.
- **How are admins provisioned and their privileges attested?** Admin
  status should fit the existing signed-identity model rather than being an
  out-of-band flag.

## Non-goals (for now)

- Any schema, API, or SPA implementation.
- Automated / algorithmic moderation.
