/**
 * keyId (canonical key id) unit tests.
 */
import assert from 'node:assert/strict';
import { appendFingerprint, formatKeyId, parseKeyId } from '../src/lib/utils/keyId.ts';

// Round trip.
const id = formatKeyId('abcd1234', 'wxyz9876', 'FPR1');
assert.equal(id, 'abcd1234@wxyz9876/FPR1');

const parsed = parseKeyId(id);
assert.ok(parsed);
assert.equal(parsed.userId, 'abcd1234');
assert.equal(parsed.serverId, 'wxyz9876');
assert.equal(parsed.fingerprint, 'FPR1');

// appendFingerprint builds the same id from an already-canonical userID.
assert.equal(appendFingerprint('abcd1234@wxyz9876', 'FPR1'), id);

// Splits on the LAST "@" and LAST "/", so an "@" in a prefix still parses.
const weird = parseKeyId('weird@user@serverID/FPR1');
assert.ok(weird);
assert.equal(weird.userId, 'weird@user');
assert.equal(weird.serverId, 'serverID');
assert.equal(weird.fingerprint, 'FPR1');

// Malformed inputs.
for (const bad of [null, undefined, '', 'noSlash@server', 'abcd1234@wxyz9876/', 'noAtSign/FPR1', '@wxyz9876/FPR1', 'abcd1234@/FPR1']) {
  assert.equal(parseKeyId(bad), null, `expected null for ${JSON.stringify(bad)}`);
}

console.log('keyId tests ok');
