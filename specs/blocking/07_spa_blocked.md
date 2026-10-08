# Blocking 07 — SPA, blocked side: verify, purge, blocked profile, forget

## Status

Proposed.

## Depends on

[04](04_realtime.md) (and [03](03_enforcement.md) for the 403 path)

## Store

`blockedBy`, keyed by `userID`, holding verified certificates.
`blockedByRepository` (`lib/repositories/blockedBy.ts`) writes through
`dbService.put('blockedBy', cert, verifyBlock)`, so nothing unverified is
stored. `verifyBlock` (`lib/verifiers/`) checks the blocking user's signature over
`buildBlockUserPayload` and the countersignature over
`buildBlockServerPayload`, against the server key chosen by fingerprint,
and that `blockedUserID` is the current user.

## Where a certificate comes from

- `USER_BLOCKED` push or catch-up.
- `USER_BLOCKED` with a request ID, answering `REQUEST_REED`,
  `REQUEST_THREAD`, `SUBSCRIBE_PROFILE`, `PROFILE_PAGE` or
  `SUBSCRIBE_REED`; the pending request resolves as blocked.
- A **403** body with `type: "block"` from any `apiService` call.

All three go through `commitBlockLocally(cert)`, which fetches the
blocking user's key if it isn't cached (`/keys/{id}` stays open to the blocked
user), verifies and stores the certificate, purges, then acks with
`USER_BLOCKED_ACK`. An invalid certificate is dropped and not acked.

## Purge

The blocking user's data, plus the forced unfollow. Nothing the user signed:

| Dropped | Kept |
|---------|------|
| Reeds authored by the blocking user (`reedsService.deleteReedsByAuthor`) | The blocking user's public keys |
| Profile (`user`) and `userInfo` | The user's own reeds, replies, echoes |
| The `following` row, `pendingFollows` / `unfollow` entries for the blocking user | The user's likes, ripples, mentions |
| The blocking user's membership in every user list (`userListsRepository.removeMember`), as a manual unfollow does | |
| Pending evictions for the blocking user (their reeds are already gone) | |
| Cached reed requests for the blocking user's reeds (`reedRequests`) | |

No `EVICTION` messages are sent: the server already dropped the
allocations at block time ([02](02_api.md)). Reeds by others that quote or
reply to a blocking user's reed stay; their reference renders as unavailable,
the same as a reference to content not held.

A blocking user-authored reed arriving later (an old relay answer, a peer's
stale push) is refused at `storeReed` while a `blockedBy` entry exists.
Feeds, threads and the reed detail page hide the blocking user's reeds the same
way, so nothing shows between the push and the purge.

## Blocked profile page

`/profile/[userId]` checks `blockedBy` **first**, from IndexedDB, before
any fetch, and renders the blocked view ([00](00_design.md#the-blocked-profile-page)):

- the blocking user's identicon and user ID; the date from the countersignature;
- no **Follow** or **Unfollow** button: **Block** / **Unblock** is the only
  relationship action on the page;
- **Block** / **Unblock** for the user's own block of the blocking user
  ([08](08_spa_blocking_user.md));
- **Forget this block**.

The background fetch still runs, as for any profile (offline-first). A 200
means the block was lifted and the lift missed: drop the certificate and
render the profile normally. A 403 refreshes nothing.

## Forget

`forgetBlock(userID)`:

1. Queue the blocking user's keys in `pendingKeyEvictions` and delete each on
   `KEY_EVICTION_ACK`, as manual eviction does (`evictUsers`). The server
   then stops owing the user the key's revocation.
2. Delete the `blockedBy` entry.
3. Leave the profile page.

The server never pushes this
certificate again ([04](04_realtime.md)); opening the profile again gets the
403 + certificate and `commitBlockLocally` runs from step one.

## Unblocked

`USER_UNBLOCKED`: delete the `blockedBy` entry if there is one, then ack.
Nothing comes back: not the follow, not list memberships, not content. The
user may follow again and content is fetched as they browse.

## Stored users

`/account/storage` lists a blocking user with only their key's size and a
"blocked you" marker. Evicting them there is the same as **Forget**.

## Tests

- Push → certificate stored, blocking user's reeds/profile/info, follow and list
  memberships gone, key kept, user's own reeds/likes untouched, ack sent.
- Unblock → certificate gone, follow and list memberships not restored.
- Tampered certificate or one naming another user → refused, nothing purged,
  no ack.
- Profile page renders blocked offline, with no Follow or Unfollow.
- Forget → key and certificate gone; reopening the profile restores both.
- Late blocking user reed → not stored.
