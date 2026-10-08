// Server key revocation payload parity against the Go golden bytes. Imports
// the real module, so this cannot pass while signing.ts drifts.
import assert from 'node:assert/strict';
import { buildServerKeyRevocationPayload } from '../src/lib/services/signing.ts';

assert.equal(
  buildServerKeyRevocationPayload('home', 'old@home', 'new@home', true, 'laptop stolen', '2026-10-07T12:00:00Z'),
  '{"compromised":true,"keyID":"old@home","reason":"laptop stolen","serverID":"home",' +
    '"signedAt":"2026-10-07T12:00:00Z","successor":"new@home","type":"server-key-revocation"}',
  'compromised revocation payload'
);

// A planned rotation: false stays in, the empty reason drops out.
assert.equal(
  buildServerKeyRevocationPayload('home', 'old@home', 'new@home', false, '', '2026-10-07T12:00:00Z'),
  '{"compromised":false,"keyID":"old@home","serverID":"home",' +
    '"signedAt":"2026-10-07T12:00:00Z","successor":"new@home","type":"server-key-revocation"}',
  'rotation payload'
);

console.log('ok   server key revocation payloads');
