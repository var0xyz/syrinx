// Follow payload parity against the Go golden bytes in
// follow_payload_test.go. Imports the real module, so this cannot pass
// while signing.ts drifts.
import assert from 'node:assert/strict';
import { buildFollowServerPayload, buildFollowUserPayload } from '../src/lib/services/signing.ts';

assert.equal(
  buildFollowUserPayload('ann@home1234', 'bob@peer5678', 'ann@home1234/fp', false),
  '{"follow":false,"followeeID":"bob@peer5678","followerID":"ann@home1234","keyID":"ann@home1234/fp","type":"follow"}',
  'unfollow user payload'
);
assert.equal(
  buildFollowServerPayload('ann@home1234', 'bob@peer5678', true, 'SERVERFP', 'c2ln', '2026-10-23T12:00:00Z'),
  '{"follow":true,"followeeID":"bob@peer5678","followerID":"ann@home1234","serverKeyFingerprint":"SERVERFP",' +
    '"signedAt":"2026-10-23T12:00:00Z","type":"follow","userSignature":"c2ln"}',
  'follow server payload'
);

console.log('ok   follow payload');
