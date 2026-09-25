/**
 * A key change is only a legitimate rotation when the new key proves the
 * old one approved it. Without that proof it is the substitution alarm, so
 * the two must never collapse into one message.
 */
import assert from 'node:assert/strict';
import { classifyKeyChange, isKeyChangeAlarm } from '../src/lib/utils/keyChange.ts';

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
  'same key, not revoked: nothing to warn about'
);

// The id matching is not enough on its own: a revoked key still in use
// leaves the vouch naming a key nobody should be encrypting to, so the
// attestation no longer rules out interception.
assert.equal(
  classifyKeyChange({ ...base, currentKeyID: OLD, vouchedKeyRevoked: true }),
  'vouched-key-revoked',
  'the vouched key is current but revoked'
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

// Only the substitution case is an alarm; the rest are informational, and
// collapsing them would put a red warning on an ordinary rotation.
assert.equal(isKeyChangeAlarm('unexplained'), true, 'substitution is the alarm');
assert.equal(isKeyChangeAlarm('rotation'), false, 'a rotation is not an alarm');
assert.equal(isKeyChangeAlarm('revocation'), false, 'a revocation is not an alarm');
assert.equal(
  isKeyChangeAlarm('vouched-key-revoked'),
  false,
  'a revoked-but-current key is not an alarm'
);
assert.equal(isKeyChangeAlarm(null), false, 'no change is not an alarm');

console.log('All key-change classification cases pass.');
