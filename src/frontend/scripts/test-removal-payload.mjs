// Reed removal countersignature payload against the Go golden bytes.
import assert from 'node:assert/strict';
import { buildReedRemovalServerPayload } from '../src/lib/services/signing.ts';

assert.equal(
  buildReedRemovalServerPayload('home', 'a@home/r0', 'a@home/k1', 'SERVERKEY01', 'SIG', '2026-10-07T12:00:00Z'),
  '---\n' +
    'authorKeyID: a@home/k1\n' +
    'reedID: a@home/r0\n' +
    'serverID: home\n' +
    'serverKeyFingerprint: SERVERKEY01\n' +
    'signedAt: 2026-10-07T12:00:00Z\n' +
    'type: reed\n' +
    'userSignature: U0lH\n' +
    '---\n',
  'reed removal server payload'
);

console.log('ok   removal payloads');
