// WebSocket handshake payload parity against the Go golden bytes. Imports
// the real module, so this cannot pass while signing.ts drifts.
import assert from 'node:assert/strict';
import { buildRealtimeAuthPayload } from '../src/lib/services/signing.ts';

assert.equal(
  buildRealtimeAuthPayload('Server01', 'alice@Server01', '1767225600', 'NONCE0123456789ab'),
  '{"nonce":"NONCE0123456789ab","serverID":"Server01","timestamp":"1767225600",' +
    '"type":"realtime-auth","userID":"alice@Server01"}',
  'realtime auth payload'
);

console.log('ok   realtime auth payload');
