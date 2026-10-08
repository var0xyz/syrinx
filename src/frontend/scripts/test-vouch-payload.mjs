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
  '{"note":"met at the cafe","subjectKeyID":"bob@peer5678/9f3c","voucherKeyID":"alice@home1234/4a1e"}',
  'vouch user payload'
);

// An empty note is left out of the payload entirely.
assert.equal(
  buildVouchUserPayload('alice@home/k1', 'bob@peer/k2', ''),
  '{"subjectKeyID":"bob@peer/k2","voucherKeyID":"alice@home/k1"}',
  'vouch user payload, empty note'
);

assert.equal(
  buildVouchServerPayload(
    'bob@peer5678/9f3c',
    'SERVERKEY01',
    'USERSIG',
    '2026-09-25T12:00:00Z'
  ),
  '{"serverKeyFingerprint":"SERVERKEY01","signedAt":"2026-09-25T12:00:00Z",' +
    '"subjectKeyID":"bob@peer5678/9f3c","userSignature":"USERSIG"}',
  'vouch server payload'
);

assert.equal(
  buildVouchWithdrawalUserPayload('alice@home1234/0192f0c1-2b3d-7456-89ab-cdef01234567'),
  '{"vouchID":"alice@home1234/0192f0c1-2b3d-7456-89ab-cdef01234567"}',
  'vouch withdrawal payload'
);

assert.equal(
  buildVouchWithdrawalServerPayload(
    'alice@home1234/0192f0c1-2b3d-7456-89ab-cdef01234567',
    'srv-fp',
    'WSIG',
    '2026-03-01T12:00:00Z'
  ),
  '{"serverKeyFingerprint":"srv-fp","signedAt":"2026-03-01T12:00:00Z",' +
    '"userSignature":"WSIG","vouchID":"alice@home1234/0192f0c1-2b3d-7456-89ab-cdef01234567"}',
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
