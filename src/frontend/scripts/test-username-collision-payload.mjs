// Username collision payload parity against the Go golden bytes in
// username_collision_test.go. Imports the real module, so this cannot pass
// while signing.ts drifts.
import assert from 'node:assert/strict';
import { buildUsernameCollisionPayload } from '../src/lib/services/signing.ts';

assert.equal(
  buildUsernameCollisionPayload(
    'home1234',
    'alice',
    'old@home1234',
    'new@home1234',
    '2026-10-23T12:00:00Z'
  ),
  '{"loserID":"new@home1234","serverID":"home1234","signedAt":"2026-10-23T12:00:00Z",' +
    '"type":"username-collision","username":"alice","winnerID":"old@home1234"}',
  'username collision payload'
);

console.log('ok   username collision payload');
