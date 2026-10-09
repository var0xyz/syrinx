/**
 * A revocation drops content its key signed at or after it, and keeps
 * what it signed before. Also covers the isKeyValidAt comparison.
 */
import assert from 'node:assert/strict';
import {
  signedBeforeRevocation,
  reedsSignedAfterRevocation,
} from '../src/lib/utils/keyRevocation.ts';

const unix = (iso) => Date.parse(iso) / 1000;
const REVOKED_AT = unix('2026-01-02T00:00:00Z');
const KEY = 'alice@a/k1';
const NEXT = 'alice@a/k2';

assert.equal(signedBeforeRevocation(unix('2026-01-01T00:00:00Z'), REVOKED_AT), true, 'before: valid');
assert.equal(signedBeforeRevocation(REVOKED_AT, REVOKED_AT), false, 'at the instant: invalid');
assert.equal(signedBeforeRevocation(unix('2026-01-03T00:00:00Z'), REVOKED_AT), false, 'after: invalid');

const reed = (id, keyID, iso) => ({
  id,
  userSignature: { id: keyID },
  serverSignature: { signedAt: typeof iso === 'number' ? iso : unix(iso) },
});

const held = [
  reed('before', KEY, '2026-01-01T00:00:00Z'),
  reed('at', KEY, REVOKED_AT),
  reed('after', KEY, '2026-01-03T00:00:00Z'),
  reed('successor', NEXT, '2026-01-03T00:00:00Z'),
];

assert.deepEqual(
  reedsSignedAfterRevocation(held, KEY, REVOKED_AT).map((r) => r.id),
  ['at', 'after'],
  'drops what the key signed at or after its revocation, keeps the rest'
);

assert.deepEqual(
  reedsSignedAfterRevocation(held, 'bob@b/k1', REVOKED_AT),
  [],
  'another key revoked: nothing dropped'
);

console.log('key-revocation: ok');
