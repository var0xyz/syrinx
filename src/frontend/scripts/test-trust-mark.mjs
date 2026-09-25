/**
 * Trust-mark ladder tests against the real module. The mark must never be
 * coloured by a vouch that is withdrawn or names a key the subject has
 * replaced.
 */
import assert from 'node:assert/strict';
import { trustMarkFrom } from '../src/lib/utils/trustMark.ts';

const trustMarkFor = trustMarkFrom;

const KEY = 'bob@peer/k1';
const OLD = 'bob@peer/k0';
const me = 'me@home';
const vouch = (voucherUserID, extra = {}) => ({
  voucherUserID,
  subjectKeyID: KEY,
  withdrawn: false,
  ...extra,
});

assert.equal(trustMarkFor([], KEY, me, new Set()), 'none', 'no vouches');

assert.equal(
  trustMarkFor([vouch('stranger@x')], KEY, me, new Set()),
  'grey',
  'a stranger vouched'
);

assert.equal(
  trustMarkFor([vouch('carol@x')], KEY, me, new Set(['carol@x'])),
  'green',
  'a root vouched'
);

assert.equal(
  trustMarkFor([vouch(me)], KEY, me, new Set()),
  'blue',
  'you vouched'
);

// The ladder is strict, so the closest evidence present always wins.
assert.equal(
  trustMarkFor([vouch('stranger@x'), vouch('carol@x')], KEY, me, new Set(['carol@x'])),
  'green',
  'green beats grey'
);
assert.equal(
  trustMarkFor([vouch('stranger@x'), vouch('carol@x'), vouch(me)], KEY, me, new Set(['carol@x'])),
  'blue',
  'blue beats green'
);

// A withdrawn vouch is retracted evidence and colours nothing.
assert.equal(
  trustMarkFor([vouch(me, { withdrawn: true })], KEY, me, new Set()),
  'none',
  'withdrawn own vouch'
);
assert.equal(
  trustMarkFor([vouch(me, { withdrawn: true }), vouch('stranger@x')], KEY, me, new Set()),
  'grey',
  'withdrawal drops blue to grey'
);

// A vouch on a superseded key is stale: otherwise a substituted key would
// inherit the mark, which is the exact failure this feature prevents.
assert.equal(
  trustMarkFor([vouch(me, { subjectKeyID: OLD })], KEY, me, new Set()),
  'none',
  'own vouch on an older key'
);
assert.equal(
  trustMarkFor([vouch('carol@x', { subjectKeyID: OLD })], KEY, me, new Set(['carol@x'])),
  'none',
  'root vouch on an older key'
);

// A demoted root is excluded from the root set, so it stops colouring
// green while its public vouch still stands.
assert.equal(
  trustMarkFor([vouch('carol@x')], KEY, me, new Set()),
  'grey',
  'demoted root falls back to grey'
);

console.log('All trust-mark ladder cases pass.');
