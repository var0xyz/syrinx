// Reed removal countersignature payload against the Go golden bytes.
import assert from 'node:assert/strict';
import { buildReedRemovalServerPayload } from '../src/lib/services/signing.ts';

assert.equal(
  buildReedRemovalServerPayload('home', 'a@home/r0', 'a@home/k1', 'SERVERKEY01', 'SIG', '2026-10-07T12:00:00Z'),
  '{"authorKeyID":"a@home/k1","reedID":"a@home/r0","serverID":"home","serverKeyFingerprint":"SERVERKEY01",' +
    '"signedAt":"2026-10-07T12:00:00Z","type":"reed","userSignature":"SIG"}',
  'reed removal server payload'
);

console.log('ok   removal payloads');
