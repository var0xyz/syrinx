# Blocking 07 — SPA, blocked side: verify, purge, blocked profile, evict

## Status

Implemented (`lib/services/blockedBy.ts`, `lib/repositories/blockedBy.ts`,
`lib/components/BlockedProfile.svelte`).

## Depends on

[04](04_realtime.md) (and [03](03_enforcement.md) for the 403 path)

## Store

`blockedBy`, keyed by `userID`, holding verified certificates.
`blockedByRepository` (`lib/repositories/blockedBy.ts`) writes through
`dbService.put('blockedBy', cert, verifyBlock)`, so nothing unverified is
stored. `verifyBlock` (`lib/verifiers/`) checks that `blockedUserID` is the
current user, that the signing key belongs to `userID`, that the
countersignature is by `userID`'s own home server, and both signatures:
the user's over `buildBlockUserPayload` and the server's over
`buildBlockServerPayload`, against the server key chosen by fingerprint.

## Where a certificate comes from

- `USER_BLOCKED` push or catch-up.
- `USER_BLOCKED` with a request ID, answering `REQUEST_REED`,
  `REQUEST_THREAD`, `SUBSCRIBE_PROFILE`, `PROFILE_PAGE` or
  `SUBSCRIBE_REED`; the pending request resolves as blocked.
- A **403** body with `type: "block"` from any `apiService` call:
  `requestRaw` hands it to the reporter `setBlockedReporter` installs, and
  throws with `status: 403` and the certificate as `body`.

All three go through `receiveBlock(cert)`: `commitBlockLocally` fetches
the blocking user's key if it isn't cached (`/keys/{id}` stays open to the
blocked user), verifies and stores the certificate and purges; then
`USER_BLOCKED_ACK` is sent. An invalid certificate is dropped and not
acked. Every change bumps `blockedByChanged`, which an open profile page
watches.

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

A reed by the blocking user arriving later (an old relay answer, a peer's
stale push) is refused at `storeReed` while a `blockedBy` entry exists.

## Blocked profile page

`/profile/[userId]` checks `blockedBy` from IndexedDB, before any fetch
(after the local account-removal checks, as the server answers 410 before
403), and renders `BlockedProfile` ([00](00_design.md#the-blocked-profile-page)):

- the blocking user's identicon and user ID; the date from the countersignature;
- no **Follow** or **Unfollow** button: **Block** / **Unblock** is the only
  relationship action on the page;
- **Block** / **Unblock** for the user's own block of the blocking user
  ([08](08_spa_blocking_user.md)).

The background fetch still runs, as for any profile (offline-first). A 200
means the block was lifted and the lift missed: drop the certificate and
reload the page data. A 403 refreshes nothing. A profile that was showing
normally switches to the blocked view when a block arrives over HTTP or WS.

## Unblocked

`USER_UNBLOCKED`: delete the `blockedBy` entry if there is one, then ack.
Nothing comes back: not the follow, not list memberships, not content. The
user may follow again and content is fetched as they browse.

## Stored users

`/account/storage` lists a blocking user with only their key's size and a
"blocked you" marker. Evicting them there (`forgetBlock`):

1. Queues their keys in `pendingKeyEvictions` and deletes each on
   `KEY_EVICTION_ACK`, as manual eviction does (`evictUsers`). The server
   then stops owing the user the key's revocation.
2. Deletes the `blockedBy` entry.

The server never pushes this certificate again ([04](04_realtime.md));
opening the profile again gets the 403 + certificate and
`commitBlockLocally` runs from the start. The profile page offers no way to
do this: the server would hand the certificate back at once.

## Checks

`npm run check` and `npm run build`; behavior is checked by hand on the
two-instance setup:

- Push → certificate stored, blocking user's reeds/profile/info, follow and list
  memberships gone, key kept, user's own reeds/likes untouched, ack sent.
- Unblock → certificate gone, follow and list memberships not restored.
- Tampered certificate or one naming another user → refused, nothing purged,
  no ack.
- Profile page renders blocked offline, with no Follow or Unfollow.
- Evicting from Stored users → key and certificate gone; reopening the
  profile restores both.
- Late blocking user reed → not stored.
