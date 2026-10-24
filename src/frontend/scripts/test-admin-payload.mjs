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

const { buildLedgerEntryPayload, buildProfilePayload } = await import('../src/lib/services/signing.ts');

assert.equal(
  buildLedgerEntryPayload(1, undefined, '', 'mesh', 'a1@home1234', '', '', 'Server peer1234 connected',
    '', [], 'home1234', 'SERVERKEY01', '2026-10-12T11:00:00Z'),
  '{"id":1,"kind":"mesh","message":"Server peer1234 connected",' +
    '"serverID":"home1234","serverKeyFingerprint":"SERVERKEY01","signedAt":"2026-10-12T11:00:00Z",' +
    '"type":"ledger-entry","userID":"a1@home1234"}',
  'first ledger entry payload'
);

assert.equal(
  buildLedgerEntryPayload(7, 5, 'PREVSIG', 'request', 'a2@home1234', 'a1@home1234', '2026-10-12T10:00:00Z',
    '[r1] Kick. Reason: Spam', '', ['u1@home1234', 'u2@home1234'], 'home1234', 'SERVERKEY01',
    '2026-10-12T11:00:00Z'),
  '{"id":7,"kind":"request","message":"[r1] Kick. Reason: Spam","prevID":5,"prevSignature":"PREVSIG",' +
    '"requestedAt":"2026-10-12T10:00:00Z","requesterID":"a1@home1234",' +
    '"serverID":"home1234","serverKeyFingerprint":"SERVERKEY01","signedAt":"2026-10-12T11:00:00Z",' +
    '"targets":["u1@home1234","u2@home1234"],"type":"ledger-entry","userID":"a2@home1234"}',
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

const { buildReedFlagPayload, buildReedFlagServerPayload } = await import('../src/lib/services/signing.ts');

assert.equal(
  buildReedFlagPayload('a9@home1234/r1', 'k3@peer5678', 'k3@peer5678/4a1e', 'Scam link'),
  '{"keyID":"k3@peer5678/4a1e","note":"Scam link","reedID":"a9@home1234/r1",' +
    '"reporterID":"k3@peer5678","type":"reed-flag"}',
  'reed flag payload'
);

assert.equal(
  buildReedFlagServerPayload('a9@home1234/r1', 'k3@peer5678', 'SERVERKEY01', 'USERSIG', '2026-10-12T14:00:00Z'),
  '{"reedID":"a9@home1234/r1","reporterID":"k3@peer5678","serverKeyFingerprint":"SERVERKEY01",' +
    '"signedAt":"2026-10-12T14:00:00Z","type":"reed-flag","userSignature":"USERSIG"}',
  'reed flag server payload'
);

const { buildFlagDismissalPayload, buildFlagDismissalServerPayload, buildAdminRequestOpenedPayload } = await import(
  '../src/lib/services/signing.ts'
);

assert.equal(
  buildFlagDismissalPayload('01900000-0000-7000-8000-000000000001', 'a9@home1234/r1', 'Not a scam', 'a2@home1234/K2', [
    'k3@peer5678',
    'u1@home1234',
  ]),
  '{"dismissalID":"01900000-0000-7000-8000-000000000001","keyID":"a2@home1234/K2","reason":"Not a scam",' +
    '"reedID":"a9@home1234/r1","reporters":["k3@peer5678","u1@home1234"],"type":"flag-dismissal"}',
  'flag dismissal payload'
);

assert.equal(
  buildFlagDismissalServerPayload('01900000-0000-7000-8000-000000000001', 'a9@home1234/r1', 'SERVERKEY01', 'USERSIG', '2026-10-12T15:00:00Z'),
  '{"dismissalID":"01900000-0000-7000-8000-000000000001","reedID":"a9@home1234/r1","serverKeyFingerprint":"SERVERKEY01",' +
    '"signedAt":"2026-10-12T15:00:00Z","type":"flag-dismissal","userSignature":"USERSIG"}',
  'flag dismissal server payload'
);

assert.equal(
  buildAdminRequestOpenedPayload('5f0c8a52-4c4e-4b8e-9a51-0f1c2d3e4f50', 'REQSIG', 'home1234', 'SERVERKEY01', '2026-10-12T10:00:00Z'),
  '{"requestID":"5f0c8a52-4c4e-4b8e-9a51-0f1c2d3e4f50","requestSignature":"REQSIG",' +
    '"serverID":"home1234","serverKeyFingerprint":"SERVERKEY01","signedAt":"2026-10-12T10:00:00Z",' +
    '"type":"admin-request-opened"}',
  'admin request opened payload'
);

const { buildFlagWithdrawalPayload, buildFlagWithdrawalServerPayload } = await import('../src/lib/services/signing.ts');

assert.equal(
  buildFlagWithdrawalPayload('a9@home1234/r1', 'k3@peer5678', 'k3@peer5678/K3', 'FLAGSIG'),
  '{"flagSignature":"FLAGSIG","keyID":"k3@peer5678/K3","reedID":"a9@home1234/r1",' +
    '"reporterID":"k3@peer5678","type":"flag-withdrawal"}',
  'flag withdrawal payload'
);

assert.equal(
  buildFlagWithdrawalServerPayload('a9@home1234/r1', 'k3@peer5678', 'SERVERKEY01', 'USERSIG', '2026-10-12T16:00:00Z'),
  '{"reedID":"a9@home1234/r1","reporterID":"k3@peer5678","serverKeyFingerprint":"SERVERKEY01",' +
    '"signedAt":"2026-10-12T16:00:00Z","type":"flag-withdrawal","userSignature":"USERSIG"}',
  'flag withdrawal server payload'
);

console.log('flag payload parity: ok');

assert.equal(
  buildProfilePayload('a1@home1234', 'a1', 'a1@home1234/K1', 'home1234', 'SERVERKEY01', 'SIG', '',
    'admin', ['user.kick', 'user.suspend'], '', '2026-10-12T11:00:00Z', '2026-10-12T11:00:00Z'),
  '{"keyID":"a1@home1234/K1","memberSince":"2026-10-12T11:00:00Z","permissions":["user.kick","user.suspend"],' +
    '"role":"admin","serverID":"home1234","serverKeyFingerprint":"SERVERKEY01","signedAt":"2026-10-12T11:00:00Z",' +
    '"type":"identity-server","userID":"a1@home1234","userSignature":"SIG","username":"a1"}',
  'admin profile payload'
);
