// Peer record payload parity against the Go golden bytes in
// peer_record_payload_test.go. Imports the real module, so this cannot pass
// while signing.ts drifts.
import assert from 'node:assert/strict';
import { buildPeerRecordPayload } from '../src/lib/services/signing.ts';

const at = '2026-10-23T12:00:00Z';
const approved = '2026-10-01T09:30:00Z';
const none = {
  peerID: 'peer5678',
  name: 'Peer',
  baseURL: '',
  frontendURL: 'https://peer.example',
  keyID: '',
  invitedBy: '',
  invitedAt: '',
  approvedBy: '',
  approvedAt: approved,
  peerApprovedAt: '',
  disconnectRequestedBy: '',
  disconnectRequestedAt: '',
  disconnectReason: '',
  revokedBy: '',
  revokedAt: '',
  revokedReason: '',
  purgedAt: '',
};

assert.equal(
  buildPeerRecordPayload(
    'home1234',
    {
      ...none,
      baseURL: 'https://api.peer.example',
      keyID: 'fp@peer5678',
      invitedBy: 'a1@home1234',
      invitedAt: approved,
      approvedBy: 'a2@home1234',
      peerApprovedAt: approved,
      disconnectRequestedBy: 'a1@home1234',
      disconnectRequestedAt: at,
      disconnectReason: 'spam',
      revokedBy: 'a2@home1234',
      revokedAt: at,
      revokedReason: 'spam',
      purgedAt: at,
    },
    at
  ),
  '{"approvedAt":"2026-10-01T09:30:00Z","approvedBy":"a2@home1234","baseURL":"https://api.peer.example",' +
    '"disconnectReason":"spam","disconnectRequestedAt":"2026-10-23T12:00:00Z","disconnectRequestedBy":"a1@home1234",' +
    '"frontendURL":"https://peer.example","invitedAt":"2026-10-01T09:30:00Z","invitedBy":"a1@home1234","keyID":"fp@peer5678",' +
    '"name":"Peer","peerApprovedAt":"2026-10-01T09:30:00Z","peerID":"peer5678","purgedAt":"2026-10-23T12:00:00Z",' +
    '"revokedAt":"2026-10-23T12:00:00Z","revokedBy":"a2@home1234","revokedReason":"spam","serverID":"home1234",' +
    '"signedAt":"2026-10-23T12:00:00Z","type":"peer-record"}',
  'peer record with every field'
);

assert.equal(
  buildPeerRecordPayload('home1234', none, at),
  '{"approvedAt":"2026-10-01T09:30:00Z","frontendURL":"https://peer.example","name":"Peer","peerID":"peer5678",' +
    '"serverID":"home1234","signedAt":"2026-10-23T12:00:00Z","type":"peer-record"}',
  'peer record with only the required fields'
);

console.log('ok   peer record payload');
