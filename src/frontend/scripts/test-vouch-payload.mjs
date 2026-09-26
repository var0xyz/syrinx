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
  buildVouchUserPayload('alice@home1234/4a1e', 'bob@peer5678/9f3c', 'met at the cafe'),
  '---\n' +
    'subjectKeyID: bob@peer5678/9f3c\n' +
    'voucherKeyID: alice@home1234/4a1e\n' +
    '---\n' +
    'met at the cafe',
  'vouch user payload'
);

// An empty note drops the content but keeps the envelope.
assert.equal(
  buildVouchUserPayload('alice@home/k1', 'bob@peer/k2', ''),
  '---\n' +
    'subjectKeyID: bob@peer/k2\n' +
    'voucherKeyID: alice@home/k1\n' +
    '---\n',
  'vouch user payload, empty note'
);

assert.equal(
  buildVouchServerPayload(
    'bob@peer5678/9f3c',
    'SERVERKEY01',
    'BASE64USERSIG',
    '2026-09-25T12:00:00Z'
  ),
  '---\n' +
    'serverKeyFingerprint: SERVERKEY01\n' +
    'signedAt: 2026-09-25T12:00:00Z\n' +
    'subjectKeyID: bob@peer5678/9f3c\n' +
    '---\n' +
    'BASE64USERSIG',
  'vouch server payload'
);

assert.equal(
  buildVouchWithdrawalUserPayload('alice@home1234/0192f0c1-2b3d-7456-89ab-cdef01234567'),
  '---\n' +
    'vouchID: alice@home1234/0192f0c1-2b3d-7456-89ab-cdef01234567\n' +
    '---\n',
  'vouch withdrawal payload'
);

assert.equal(
  buildVouchWithdrawalServerPayload(
    'alice@home1234/0192f0c1-2b3d-7456-89ab-cdef01234567',
    'srv-fp',
    'WSIG',
    '2026-03-01T12:00:00Z'
  ),
  '---\n' +
    'serverKeyFingerprint: srv-fp\n' +
    'signedAt: 2026-03-01T12:00:00Z\n' +
    'vouchID: alice@home1234/0192f0c1-2b3d-7456-89ab-cdef01234567\n' +
    '---\nWSIG',
  'vouch withdrawal server payload'
);

// Domain separation without a type discriminator: the field sets differ,
// so neither signature can be read as the other.
assert.notEqual(
  buildVouchUserPayload('alice@home/k1', 'bob@peer/k2', ''),
  buildVouchWithdrawalUserPayload('alice@home/0192f0c1-2b3d-7456-89ab-cdef01234567'),
  'vouch and withdrawal payloads must differ'
);

// Re-vouching is allowed, so each withdrawal must name its own vouch.
assert.notEqual(
  buildVouchWithdrawalUserPayload('alice@home/0192f0c1-2b3d-7456-89ab-cdef01234567'),
  buildVouchWithdrawalUserPayload('alice@home/0192f0c1-2b3d-7456-89ab-cdef01234568'),
  'withdrawals for different vouches must differ'
);

console.log('All vouch payload vectors match the Go golden bytes.');
