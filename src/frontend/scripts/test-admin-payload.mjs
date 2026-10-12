// Admin request payload parity against the Go golden bytes. Imports the
// real module, so this cannot pass while signing.ts drifts.
import assert from 'node:assert/strict';
import {
  buildAdminRequestPayload,
  buildAdminDecisionPayload,
  buildAdminRequestServerPayload
} from '../src/lib/services/signing.ts';

const requestID = '5f0c8a52-4c4e-4b8e-9a51-0f1c2d3e4f50';

assert.equal(
  buildAdminRequestPayload(requestID, 'kick', 'a1@home1234', 'a1@home1234/4a1e', 'Spam',
    ['u1@home1234', 'u2@home1234'], '', '2026-10-12T09:30:00Z'),
  '{"action":"kick","createdAt":"2026-10-12T09:30:00Z","keyID":"a1@home1234/4a1e",' +
    '"reason":"Spam","requestID":"5f0c8a52-4c4e-4b8e-9a51-0f1c2d3e4f50","requesterID":"a1@home1234",' +
    '"targets":["u1@home1234","u2@home1234"],"type":"admin-request"}',
  'admin request payload'
);

assert.equal(
  buildAdminRequestPayload(requestID, 'broadcast', 'a1@home1234', 'a1@home1234/4a1e', '', [],
    'Maintenance tonight', '2026-10-12T09:30:00Z'),
  '{"action":"broadcast","body":"Maintenance tonight","createdAt":"2026-10-12T09:30:00Z",' +
    '"keyID":"a1@home1234/4a1e","requestID":"5f0c8a52-4c4e-4b8e-9a51-0f1c2d3e4f50",' +
    '"requesterID":"a1@home1234","type":"admin-request"}',
  'broadcast request payload'
);

assert.equal(
  buildAdminDecisionPayload(requestID, 'approve', 'a2@home1234', 'a2@home1234/9b2f', 'REQSIG'),
  '{"deciderID":"a2@home1234","decision":"approve","keyID":"a2@home1234/9b2f",' +
    '"requestID":"5f0c8a52-4c4e-4b8e-9a51-0f1c2d3e4f50","requestSignature":"REQSIG","type":"admin-decision"}',
  'admin decision payload'
);

assert.equal(
  buildAdminRequestServerPayload(requestID, 'executed', 'REQSIG', 'DECSIG', 'home1234', 'SERVERKEY01',
    '2026-10-12T10:00:00Z'),
  '{"decisionSignature":"DECSIG","requestID":"5f0c8a52-4c4e-4b8e-9a51-0f1c2d3e4f50",' +
    '"requestSignature":"REQSIG","serverID":"home1234","serverKeyFingerprint":"SERVERKEY01",' +
    '"signedAt":"2026-10-12T10:00:00Z","state":"executed","type":"admin-request-server"}',
  'admin server payload'
);

assert.equal(
  buildAdminRequestServerPayload(requestID, 'moot', 'REQSIG', '', 'home1234', 'SERVERKEY01',
    '2026-10-12T10:00:00Z'),
  '{"requestID":"5f0c8a52-4c4e-4b8e-9a51-0f1c2d3e4f50","requestSignature":"REQSIG",' +
    '"serverID":"home1234","serverKeyFingerprint":"SERVERKEY01",' +
    '"signedAt":"2026-10-12T10:00:00Z","state":"moot","type":"admin-request-server"}',
  'moot server payload'
);

console.log('admin payload parity: ok');

const { buildLedgerEntryPayload } = await import('../src/lib/services/signing.ts');

assert.equal(
  buildLedgerEntryPayload(1, undefined, '', 'a1@home1234', 'Admin invite inv1 created',
    'admin.create,user.kick', [], 'home1234', 'SERVERKEY01', '2026-10-12T11:00:00Z'),
  '{"content":"admin.create,user.kick","id":1,"message":"Admin invite inv1 created",' +
    '"serverID":"home1234","serverKeyFingerprint":"SERVERKEY01","signedAt":"2026-10-12T11:00:00Z",' +
    '"type":"ledger-entry","userID":"a1@home1234"}',
  'first ledger entry payload'
);

assert.equal(
  buildLedgerEntryPayload(7, 5, 'PREVSIG', '', 'Closed request r1: every target left', '',
    ['u1@home1234', 'u2@home1234'], 'home1234', 'SERVERKEY01', '2026-10-12T11:00:00Z'),
  '{"id":7,"message":"Closed request r1: every target left","prevID":5,"prevSignature":"PREVSIG",' +
    '"serverID":"home1234","serverKeyFingerprint":"SERVERKEY01","signedAt":"2026-10-12T11:00:00Z",' +
    '"targets":["u1@home1234","u2@home1234"],"type":"ledger-entry"}',
  'chained ledger entry payload'
);

console.log('ledger payload parity: ok');

const { buildAccountKickServerPayload } = await import('../src/lib/services/signing.ts');

assert.equal(
  buildAccountKickServerPayload('home1234', 'u1@home1234', requestID, 'REQSIG', 'DECSIG', 'SERVERKEY01',
    '2026-10-12T12:00:00Z'),
  '{"decisionSignature":"DECSIG","requestID":"5f0c8a52-4c4e-4b8e-9a51-0f1c2d3e4f50",' +
    '"requestSignature":"REQSIG","serverID":"home1234","serverKeyFingerprint":"SERVERKEY01",' +
    '"signedAt":"2026-10-12T12:00:00Z","type":"account-kick","userID":"u1@home1234"}',
  'kick server payload'
);

console.log('kick payload parity: ok');

const { buildAdminRevocationServerPayload } = await import('../src/lib/services/signing.ts');

assert.equal(
  buildAdminRevocationServerPayload('u1@home1234', 'u1@home1234/OLDKEY', requestID, 'REQSIG', 'DECSIG',
    'home1234', 'SERVERKEY01', '2026-10-12T13:00:00Z'),
  '{"decisionSignature":"DECSIG","keyID":"u1@home1234/OLDKEY","reason":"lost",' +
    '"requestID":"5f0c8a52-4c4e-4b8e-9a51-0f1c2d3e4f50","requestSignature":"REQSIG","serverID":"home1234",' +
    '"serverKeyFingerprint":"SERVERKEY01","signedAt":"2026-10-12T13:00:00Z","type":"admin-revocation",' +
    '"userID":"u1@home1234"}',
  'admin revocation payload'
);

console.log('admin revocation payload parity: ok');
