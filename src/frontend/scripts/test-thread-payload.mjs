// Thread record payload parity against the Go golden bytes. Imports the
// real module, so this cannot pass while signing.ts drifts.
import assert from 'node:assert/strict';
import { buildThreadUserPayload, buildThreadServerPayload } from '../src/lib/services/signing.ts';

const ids = Array.from({ length: 11 }, (_, i) => `a@home/r${i}`);

// Index keys sort as strings: 10 precedes 2.
assert.equal(
  buildThreadUserPayload('home', ids[0], ids),
  '---\n' +
    '0: a@home/r0\n' +
    '1: a@home/r1\n' +
    '10: a@home/r10\n' +
    '2: a@home/r2\n' +
    '3: a@home/r3\n' +
    '4: a@home/r4\n' +
    '5: a@home/r5\n' +
    '6: a@home/r6\n' +
    '7: a@home/r7\n' +
    '8: a@home/r8\n' +
    '9: a@home/r9\n' +
    'serverID: home\n' +
    'threadID: a@home/r0\n' +
    'type: thread\n' +
    '---\n',
  'thread user payload'
);

assert.equal(
  buildThreadServerPayload('home', 'a@home/r0', 'a@home/k1', 'SERVERKEY01', 'SIG', '2026-10-07T12:00:00Z'),
  '---\n' +
    'authorKeyID: a@home/k1\n' +
    'serverID: home\n' +
    'serverKeyFingerprint: SERVERKEY01\n' +
    'signedAt: 2026-10-07T12:00:00Z\n' +
    'threadID: a@home/r0\n' +
    'type: thread\n' +
    'userSignature: U0lH\n' +
    '---\n',
  'thread server payload'
);

console.log('ok   thread payloads');
