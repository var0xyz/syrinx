// Thread record payload parity against the Go golden bytes. Imports the
// real module, so this cannot pass while signing.ts drifts.
import assert from 'node:assert/strict';
import {
  buildThreadUserPayload,
  buildThreadServerPayload,
  buildThreadRemovalUserPayload,
  buildThreadRemovalServerPayload,
} from '../src/lib/services/signing.ts';

const ids = Array.from({ length: 11 }, (_, i) => `a@home/r${i}`);

assert.equal(
  buildThreadUserPayload('home', ids[0], ids),
  '{"reedIDs":["a@home/r0","a@home/r1","a@home/r2","a@home/r3","a@home/r4","a@home/r5",' +
    '"a@home/r6","a@home/r7","a@home/r8","a@home/r9","a@home/r10"],' +
    '"serverID":"home","threadID":"a@home/r0","type":"thread"}',
  'thread user payload'
);

assert.equal(
  buildThreadServerPayload('home', 'a@home/r0', 'a@home/k1', 'SERVERKEY01', 'SIG', '2026-10-07T12:00:00Z'),
  '{"authorKeyID":"a@home/k1","serverID":"home","serverKeyFingerprint":"SERVERKEY01",' +
    '"signedAt":"2026-10-07T12:00:00Z","threadID":"a@home/r0","type":"thread","userSignature":"SIG"}',
  'thread server payload'
);

assert.equal(
  buildThreadRemovalUserPayload('home', 'a@home/r0', 'TSIG'),
  '{"serverID":"home","threadID":"a@home/r0","threadSignature":"TSIG","type":"thread_removal"}',
  'thread removal user payload'
);

assert.equal(
  buildThreadRemovalServerPayload('home', 'a@home/r0', 'a@home/k1', 'SERVERKEY01', 'SIG', '2026-10-07T12:00:00Z'),
  '{"authorKeyID":"a@home/k1","serverID":"home","serverKeyFingerprint":"SERVERKEY01",' +
    '"signedAt":"2026-10-07T12:00:00Z","threadID":"a@home/r0","type":"thread_removal","userSignature":"SIG"}',
  'thread removal server payload'
);

console.log('ok   thread payloads');
