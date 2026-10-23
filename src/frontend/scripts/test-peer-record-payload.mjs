// Peer record payload parity against the Go golden bytes in
// peer_record_payload_test.go. Imports the real module, so this cannot pass
// while signing.ts drifts.
import assert from 'node:assert/strict';
import { buildPeerRecordPayload } from '../src/lib/services/signing.ts';

assert.equal(
  buildPeerRecordPayload(
    'home1234',
    'peer5678',
    'Peer',
    'fp@peer5678',
    'https://api.peer.example',
    'https://peer.example',
    'revoked',
    '2026-10-23T12:00:00Z'
  ),
  '{"baseURL":"https://api.peer.example","frontendURL":"https://peer.example","keyID":"fp@peer5678",' +
    '"name":"Peer","peerID":"peer5678","serverID":"home1234","signedAt":"2026-10-23T12:00:00Z",' +
    '"status":"revoked","type":"peer-record"}',
  'revoked peer record payload'
);

console.log('ok   peer record payload');
