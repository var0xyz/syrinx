// Block payload parity against the Go golden bytes. Imports the real
// module, so this cannot pass while signing.ts drifts.
import assert from 'node:assert/strict';
import { buildBlockUserPayload, buildBlockServerPayload } from '../src/lib/services/signing.ts';

assert.equal(
  buildBlockUserPayload('k3x9@home1234', 'p7q2@peer5678', 'k3x9@home1234/4a1e'),
  '{"blockedUserID":"p7q2@peer5678","keyID":"k3x9@home1234/4a1e","type":"block","userID":"k3x9@home1234"}',
  'block user payload'
);

assert.equal(
  buildBlockServerPayload('k3x9@home1234', 'p7q2@peer5678', 'SERVERKEY01', 'USERSIG', '2026-10-08T12:00:00Z'),
  '{"blockedUserID":"p7q2@peer5678","serverKeyFingerprint":"SERVERKEY01",' +
    '"signedAt":"2026-10-08T12:00:00Z","type":"block","userID":"k3x9@home1234","userSignature":"USERSIG"}',
  'block server payload'
);

console.log('block payload parity: ok');
