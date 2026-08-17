/**
 * keyId (canonical key id) unit tests.
 */
import assert from 'node:assert/strict';
import {
  appendFingerprint,
  formatKeyId,
  formatServerKeyId,
  parseCanonicalId,
  parseKeyId,
} from '../src/lib/utils/identityRef.ts';

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

// parseCanonicalId: generic entityId@serverId[/subEntityId] tuple, covers
// both the 3-part user-key shape and the 2-part server-key shape. Callers
// destructure positionally and discard what they don't need, e.g.
// const [, serverId] = parseCanonicalId(raw) ?? [];
const threePart = parseCanonicalId(id);
assert.ok(threePart);
const [threeEntityId, threeServerId, threeSubEntityId] = threePart;
assert.equal(threeEntityId, 'abcd1234');
assert.equal(threeServerId, 'wxyz9876');
assert.equal(threeSubEntityId, 'FPR1');

const serverKeyId = formatServerKeyId('FPR1', 'wxyz9876');
assert.equal(serverKeyId, 'FPR1@wxyz9876');
const twoPart = parseCanonicalId(serverKeyId);
assert.ok(twoPart);
const [twoEntityId, twoServerId, twoSubEntityId] = twoPart;
assert.equal(twoEntityId, 'FPR1');
assert.equal(twoServerId, 'wxyz9876');
assert.equal(twoSubEntityId, null);

// A 2-part id is not a valid 3-part user-key id.
assert.equal(parseKeyId(serverKeyId), null);

for (const bad of [null, undefined, '', 'noAtSign', 'noAtSign/FPR1', '@wxyz9876', '@wxyz9876/FPR1', 'abcd1234@', 'abcd1234@/FPR1', 'abcd1234@wxyz9876/']) {
  assert.equal(parseCanonicalId(bad), null, `expected null for ${JSON.stringify(bad)}`);
}

console.log('keyId tests ok');
