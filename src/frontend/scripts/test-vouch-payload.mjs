// Vouch payload parity against the Go golden bytes. Imports the real
// module, so this cannot pass while signing.ts drifts.
import assert from 'node:assert/strict';
import {
  buildVouchUserPayload,
  buildVouchServerPayload,
  buildVouchWithdrawalUserPayload,
  buildVouchWithdrawalServerPayload,
} from '../src/lib/services/signing.ts';

assert.equal(
  buildVouchUserPayload(
    'alice@home1234/4a1e',
    'bob@peer5678',
    'bob@peer5678/9f3c',
    'met at the cafe'
  ),
  '---\n' +
    'subjectKeyID: bob@peer5678/9f3c\n' +
    'subjectUserID: bob@peer5678\n' +
    'type: user_vouch\n' +
    'voucherKeyID: alice@home1234/4a1e\n' +
    '---\n' +
    'met at the cafe',
  'vouch user payload'
);

// An empty note drops the content but keeps the envelope.
assert.equal(
  buildVouchUserPayload('alice@home/k1', 'bob@peer', 'bob@peer/k2', ''),
  '---\n' +
    'subjectKeyID: bob@peer/k2\n' +
    'subjectUserID: bob@peer\n' +
    'type: user_vouch\n' +
    'voucherKeyID: alice@home/k1\n' +
    '---\n',
  'vouch user payload, empty note'
);

assert.equal(
  buildVouchServerPayload(
    'alice@home1234',
    'bob@peer5678',
    'bob@peer5678/9f3c',
    'SERVERKEY01',
    'BASE64USERSIG',
    '2026-09-25T12:00:00Z'
  ),
  '---\n' +
    'serverKeyFingerprint: SERVERKEY01\n' +
    'signedAt: 2026-09-25T12:00:00Z\n' +
    'subjectKeyID: bob@peer5678/9f3c\n' +
    'subjectUserID: bob@peer5678\n' +
    'type: user_vouch\n' +
    'voucherUserID: alice@home1234\n' +
    '---\n' +
    'BASE64USERSIG',
  'vouch server payload'
);

assert.equal(
  buildVouchWithdrawalUserPayload(
    'alice@home1234/beef',
    'bob@peer5678',
    'bob@peer5678/9f3c'
  ),
  '---\n' +
    'subjectKeyID: bob@peer5678/9f3c\n' +
    'subjectUserID: bob@peer5678\n' +
    'type: user_vouch_withdrawal\n' +
    'voucherKeyID: alice@home1234/beef\n' +
    '---\n',
  'vouch withdrawal payload'
);

assert.equal(
  buildVouchWithdrawalServerPayload(
    'alice@home1234/0192f0c1-2b3d-7456-89ab-cdef01234567',
    'alice@home1234',
    'bob@peer5678/9f3c',
    'srv-fp',
    'WSIG',
    '2026-03-01T12:00:00Z'
  ),
  '---\n' +
    'serverKeyFingerprint: srv-fp\n' +
    'signedAt: 2026-03-01T12:00:00Z\n' +
    'subjectKeyID: bob@peer5678/9f3c\n' +
    'type: user_vouch_withdrawal\n' +
    'vouchID: alice@home1234/0192f0c1-2b3d-7456-89ab-cdef01234567\n' +
    'voucherUserID: alice@home1234\n' +
    '---\nWSIG',
  'vouch withdrawal server payload'
);

// Domain separation: a withdrawal must never be replayable as a vouch.
assert.notEqual(
  buildVouchUserPayload('alice@home/k1', 'bob@peer', 'bob@peer/k2', ''),
  buildVouchWithdrawalUserPayload('alice@home/k1', 'bob@peer', 'bob@peer/k2'),
  'vouch and withdrawal payloads must differ'
);

console.log('All vouch payload vectors match the Go golden bytes.');
