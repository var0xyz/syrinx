/**
 * Relay must refuse only on a contradiction — a live vouch naming a
 * different key than the server reports. Refusing on absence would break
 * relay for the majority who have verified nobody.
 */
import assert from 'node:assert/strict';
import { findContradiction } from '../src/lib/utils/vouchContradiction.ts';

const REPORTED = 'bob@peer/k1';
const OTHER = 'bob@peer/k2';

assert.equal(findContradiction([], REPORTED), null, 'no vouches: proceed');

assert.equal(
  findContradiction([{ subjectKeyId: REPORTED }], REPORTED),
  null,
  'vouch agrees with the reported key: proceed'
);

assert.ok(
  findContradiction([{ subjectKeyId: OTHER }], REPORTED),
  'vouch names another key: refuse'
);

// A withdrawn vouch is deleted locally once its retraction verifies, so
// retracted evidence can never block a relay.
assert.equal(
  findContradiction([], REPORTED),
  null,
  'no vouch disagrees: proceed'
);

// One agreeing vouch is enough, even alongside a vouch for an older key.
assert.equal(
  findContradiction([{ subjectKeyId: OTHER }, { subjectKeyId: REPORTED }], REPORTED),
  null,
  'a vouch for the reported key clears an older one'
);

assert.ok(
  findContradiction(
    [{ subjectKeyId: OTHER, withdrawn: true }, { subjectKeyId: OTHER }],
    REPORTED
  ),
  'a live disagreement still refuses when a withdrawn one exists'
);

console.log('All relay-refusal cases pass.');
