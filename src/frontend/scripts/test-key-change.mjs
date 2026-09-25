/**
 * A key change is only a legitimate rotation when the new key proves the
 * old one approved it. Without that proof it is the substitution alarm, so
 * the two must never collapse into one message.
 */
import assert from 'node:assert/strict';
import { classifyKeyChange } from '../src/lib/utils/keyChange.ts';

const OLD = 'bob@peer/k1';
const NEW = 'bob@peer/k2';

const base = {
  vouchedKeyID: OLD,
  currentKeyID: NEW,
  predecessorID: null,
  handoffValid: false,
  vouchedKeyRevoked: false,
};

assert.equal(
  classifyKeyChange({ ...base, currentKeyID: OLD }),
  null,
  'same key: nothing to warn about'
);

assert.equal(
  classifyKeyChange({ ...base, predecessorID: OLD, handoffValid: true }),
  'rotation',
  'valid handoff from the vouched key is a rotation'
);

assert.equal(
  classifyKeyChange(base),
  'unexplained',
  'no handoff and no revocation is the alarm'
);

// A handoff that does not verify must not be treated as a rotation.
assert.equal(
  classifyKeyChange({ ...base, predecessorID: OLD, handoffValid: false }),
  'unexplained',
  'an unverified handoff is still unexplained'
);

// A predecessor pointing at some other key says nothing about this vouch.
assert.equal(
  classifyKeyChange({ ...base, predecessorID: 'bob@peer/k9', handoffValid: true }),
  'unexplained',
  'a handoff from a different key does not explain this change'
);

assert.equal(
  classifyKeyChange({ ...base, vouchedKeyRevoked: true }),
  'revocation',
  'a revoked vouched key with no successor is a revocation'
);

console.log('All key-change classification cases pass.');
