/**
 * Eviction must never drop a user this device verified out of band. Losing
 * their records sends key verification back to the server the vouch exists
 * to check, so the protected set is a security property, not a cache hint.
 */
import assert from 'node:assert/strict';
import { buildProtectedUserIDs } from '../src/lib/utils/evictionProtection.ts';

const empty = { viewerID: null, following: [], lists: [], vouches: [] };

assert.deepEqual(buildProtectedUserIDs(empty), new Set(), 'nothing to protect');

assert.ok(
  buildProtectedUserIDs({ ...empty, viewerID: 'me@home' }).has('me@home'),
  'the viewer is protected'
);

// A vouched subject is protected even when not followed and in no list.
const vouched = buildProtectedUserIDs({
  ...empty,
  viewerID: 'me@home',
  vouches: [{ subjectUserID: 'bob@peer', voucherUserID: 'me@home' }],
});
assert.ok(vouched.has('bob@peer'), 'a user you vouched for is protected');

// The voucher is protected too: a green check needs their identity resolvable.
const inbound = buildProtectedUserIDs({
  ...empty,
  viewerID: 'me@home',
  vouches: [{ subjectUserID: 'me@home', voucherUserID: 'carol@x' }],
});
assert.ok(inbound.has('carol@x'), 'someone who vouched for you is protected');

// An unrelated user stays evictable, or quota pressure has nothing to free.
const mixed = buildProtectedUserIDs({
  viewerID: 'me@home',
  following: [{ userId: 'followed@x' }],
  lists: [{ memberIds: ['listed@x'] }],
  vouches: [{ subjectUserID: 'bob@peer', voucherUserID: 'me@home' }],
});
assert.ok(!mixed.has('stranger@x'), 'an unrelated user remains evictable');
assert.deepEqual(
  mixed,
  new Set(['me@home', 'followed@x', 'listed@x', 'bob@peer']),
  'protected set is exactly the interested parties'
);

console.log('All eviction-protection cases pass.');
