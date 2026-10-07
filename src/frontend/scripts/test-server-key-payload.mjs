// Server key revocation payload parity against the Go golden bytes. Imports
// the real module, so this cannot pass while signing.ts drifts.
import assert from 'node:assert/strict';
import { buildServerKeyRevocationPayload } from '../src/lib/services/signing.ts';

assert.equal(
  buildServerKeyRevocationPayload('home', 'old@home', 'new@home', true, 'laptop stolen', '2026-10-07T12:00:00Z'),
  '---\n' +
    'compromised: true\n' +
    'keyID: old@home\n' +
    'serverID: home\n' +
    'signedAt: 2026-10-07T12:00:00Z\n' +
    'successor: new@home\n' +
    'type: server-key-revocation\n' +
    '---\n' +
    'laptop stolen',
  'compromised revocation payload'
);

// A planned rotation: not compromised, no reason, so no content.
assert.equal(
  buildServerKeyRevocationPayload('home', 'old@home', 'new@home', false, '', '2026-10-07T12:00:00Z'),
  '---\n' +
    'compromised: false\n' +
    'keyID: old@home\n' +
    'serverID: home\n' +
    'signedAt: 2026-10-07T12:00:00Z\n' +
    'successor: new@home\n' +
    'type: server-key-revocation\n' +
    '---\n',
  'rotation payload'
);

console.log('ok   server key revocation payloads');
