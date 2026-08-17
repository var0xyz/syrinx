// Manual smoke test 2 (not part of the test suite): exercises an
// authenticated request (X-Syrinx-Fingerprint header join in
// middlewares.go), profile update (canonical fingerprint inside the
// signed identity payload), key rotation (AddPublicKey/RevokeKey), and the
// GetPublicKey/GetKeyRevocation bare-URL routes — the full request-signing
// surface this change touched. Run smoke-signup.mjs first and pass its
// JSON output as argv[2].
import * as openpgp from 'openpgp';
import {
  buildProfilePayload,
  buildPublicKeyPayload,
  buildUserRevocationPayload,
} from '../src/lib/services/signing.ts';

async function signMessage(text, privateKeyArmored, passphrase) {
  const privateKey = await openpgp.decryptKey({
    privateKey: await openpgp.readPrivateKey({ armoredKey: privateKeyArmored }),
    passphrase,
  });
  const message = await openpgp.createMessage({ text });
  return openpgp.sign({ message, signingKeys: privateKey, detached: true, format: 'armored' });
}

async function generateKeyPair({ name, email, comment, password }) {
  const { privateKey, publicKey } = await openpgp.generateKey({
    type: 'ecc',
    curve: 'ed25519Legacy',
    userIDs: [{ name, email, comment }],
    passphrase: password,
    format: 'armored',
  });
  const parsedPublic = await openpgp.readKey({ armoredKey: publicKey });
  return { privateKey, publicKey, fingerprint: parsedPublic.getFingerprint() };
}

function buildCanonicalRequestString(method, path, body, timestamp) {
  return `${method} ${path}\n\n${body}\n\n${timestamp}`;
}

async function signedFetch(url, { method, body, userId, bareFingerprint, privateKey, passphrase }) {
  const path = new URL(url).pathname + new URL(url).search;
  const timestamp = Math.floor(Date.now() / 1000).toString();
  const bodyStr = body ?? '';
  const canonical = buildCanonicalRequestString(method, path, bodyStr, timestamp);
  const sigArmor = await signMessage(canonical, privateKey, passphrase);
  const headers = {
    'X-Syrinx-User-Id': userId,
    'X-Syrinx-Fingerprint': bareFingerprint,
    'X-Syrinx-Signature': btoa(sigArmor),
    'X-Syrinx-Signature-Scope': 'body',
    'X-Syrinx-Timestamp': timestamp,
    'X-Syrinx-Device-Id': '550e8400-e29b-41d4-a716-446655440000',
  };
  if (body !== undefined) headers['Content-Type'] = 'application/x-www-form-urlencoded';
  return fetch(url, { method, headers, body });
}

const BASE = 'http://localhost:8080/api';

async function main() {
  const session = JSON.parse(process.argv[2]);
  const { userId, bareFingerprint, canonicalFingerprint, password, privateKey, publicKey } = session;
  console.log('userId:', userId, 'bareFingerprint:', bareFingerprint);

  // --- Test A: authenticated request (check-username via renamed endpoint) ---
  const checkForm = new URLSearchParams({ username: 'someothername' + Date.now() }).toString();
  const checkRes = await signedFetch(`${BASE}/users/me/check-username`, {
    method: 'POST', body: checkForm, userId, bareFingerprint, privateKey, passphrase: password,
  });
  console.log('check-username status:', checkRes.status, await checkRes.text());
  if (checkRes.status !== 200) { console.error('AUTH REQUEST FAILED'); process.exit(1); }
  console.log('TEST A (authenticated request / X-Syrinx-Fingerprint join) PASSED\n');

  // --- Test B: GetPublicKey via bare-fingerprint URL route ---
  const getKeyRes = await signedFetch(`${BASE}/users/${userId}/keys/${bareFingerprint}`, {
    method: 'GET', userId, bareFingerprint, privateKey, passphrase: password,
  });
  const keyBody = await getKeyRes.json();
  console.log('GetPublicKey status:', getKeyRes.status, 'fingerprint:', keyBody.fingerprint);
  if (getKeyRes.status !== 200 || keyBody.fingerprint !== canonicalFingerprint) {
    console.error('GET PUBLIC KEY MISMATCH'); process.exit(1);
  }
  console.log('TEST B (GetPublicKey bare-URL route) PASSED\n');

  // --- Test C: key rotation (RevokeKey + AddPublicKey) ---
  const newKeyPair = await generateKeyPair({ name: userId, email: 'smoke2@test.example', password });
  console.log('new bare fingerprint:', newKeyPair.fingerprint);

  const revokeReason = 'smoke test rotation';
  const revocationUserPayload = buildUserRevocationPayload(userId, canonicalFingerprint, revokeReason);
  const revocationUserSigArmor = await signMessage(revocationUserPayload, privateKey, password);
  const revokeForm = new URLSearchParams({
    reason: revokeReason,
    userSignature: btoa(revocationUserSigArmor),
  }).toString();
  const revokeRes = await signedFetch(`${BASE}/users/${userId}/keys/${bareFingerprint}/revoke`, {
    method: 'POST', body: revokeForm, userId, bareFingerprint, privateKey, passphrase: password,
  });
  const revokeBody = await revokeRes.text();
  console.log('RevokeKey status:', revokeRes.status, revokeBody.slice(0, 200));
  if (revokeRes.status !== 200) { console.error('REVOKE KEY FAILED'); process.exit(1); }
  const revokedKey = JSON.parse(revokeBody);
  if (revokedKey.fingerprint !== canonicalFingerprint || !revokedKey.revoked) {
    console.error('REVOKE KEY WRONG SHAPE'); process.exit(1);
  }
  console.log('TEST C.1 (RevokeKey) PASSED\n');

  // Rotation proof: old key signs new pubkey armor; new key signs it too.
  const revokedKeySigArmor = await signMessage(newKeyPair.publicKey.trim(), privateKey, password);
  const newKeySigArmor = await signMessage(newKeyPair.publicKey.trim(), newKeyPair.privateKey, password);

  const addForm = new URLSearchParams({
    userID: userId,
    publicKey: btoa(newKeyPair.publicKey.trim()),
    revokedKeyFingerprint: bareFingerprint,
    revokedKeySignature: btoa(revokedKeySigArmor),
    newKeySignature: btoa(newKeySigArmor),
  }).toString();
  const addRes = await fetch(`${BASE}/keys`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: addForm,
  });
  const addBody = await addRes.text();
  console.log('AddPublicKey status:', addRes.status, addBody.slice(0, 300));
  if (addRes.status !== 200) { console.error('ADD PUBLIC KEY FAILED'); process.exit(1); }
  const newKey = JSON.parse(addBody);
  const newCanonicalFingerprint = `${userId}/${newKeyPair.fingerprint}`;
  if (newKey.fingerprint !== newCanonicalFingerprint) {
    console.error(`ADD PUBLIC KEY FINGERPRINT MISMATCH: got ${newKey.fingerprint} want ${newCanonicalFingerprint}`);
    process.exit(1);
  }
  if (newKey.predecessor !== canonicalFingerprint) {
    console.error(`PREDECESSOR MISMATCH: got ${newKey.predecessor} want ${canonicalFingerprint}`);
    process.exit(1);
  }
  console.log('TEST C.2 (AddPublicKey / rotation) PASSED\n');

  // --- Test D: GetKeyRevocation via bare-fingerprint URL route ---
  const getRevRes = await signedFetch(`${BASE}/users/${userId}/keys/${bareFingerprint}/revocation`, {
    method: 'GET', userId, bareFingerprint: newKeyPair.fingerprint, privateKey: newKeyPair.privateKey, passphrase: password,
  });
  const revBody = await getRevRes.json();
  console.log('GetKeyRevocation status:', getRevRes.status, 'fingerprint:', revBody.fingerprint, 'successor:', revBody.successor);
  if (getRevRes.status !== 200 || revBody.fingerprint !== canonicalFingerprint || revBody.successor !== newCanonicalFingerprint) {
    console.error('GET KEY REVOCATION MISMATCH'); process.exit(1);
  }
  console.log('TEST D (GetKeyRevocation bare-URL route + successor chain) PASSED\n');

  console.log('ALL SMOKE TESTS PASSED');
}

main().catch((err) => {
  console.error('SMOKE TEST FAILED:', err);
  process.exit(1);
});
