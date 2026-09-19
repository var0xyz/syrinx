// Manual smoke test (not part of the test suite): drives a real signup
// against a live local server, using the real signing.ts payload builder
// (byte-identical to identity.go) plus plain openpgp for key generation and
// signing (mirrors test-verify-binary.mjs's approach — crypto.ts imports
// 'openpgp/lightweight', a browser-bundler subpath Node can't resolve).
// Verifies canonical fingerprints work end-to-end over the real wire. Run
// with a local server on :8080.
import * as openpgp from 'openpgp';
import { buildNewUserIdentityPayload } from '../src/lib/services/signing.ts';

async function generateKeyPair({ name, email, comment, password }) {
  const userIDs = [{ name, email, comment }];
  const { privateKey, publicKey } = await openpgp.generateKey({
    type: 'ecc',
    curve: 'ed25519Legacy',
    userIDs,
    passphrase: password,
    format: 'armored',
  });
  const parsedPublic = await openpgp.readKey({ armoredKey: publicKey });
  const fingerprint = parsedPublic.getFingerprint();
  return { privateKey, publicKey, fingerprint };
}

async function signMessage(text, privateKeyArmored, passphrase) {
  const privateKey = await openpgp.decryptKey({
    privateKey: await openpgp.readPrivateKey({ armoredKey: privateKeyArmored }),
    passphrase,
  });
  const message = await openpgp.createMessage({ text });
  return openpgp.sign({ message, signingKeys: privateKey, detached: true, format: 'armored' });
}

const BASE = 'http://localhost:8080/api';

async function main() {
  const idRes = await fetch(`${BASE}/users/id`);
  const reserved = await idRes.json();
  console.log('reserved userID:', reserved.userID);

  const serverInfoRes = await fetch(`${BASE}/server/info`);
  const serverInfo = await serverInfoRes.json();
  const serverId = serverInfo.id;
  console.log('serverId:', serverId);

  const password = 'smoketest-passphrase-123';
  const username = 'smoketestuser' + Date.now().toString().slice(-6);
  const email = 'smoke@test.example';

  const keyPair = await generateKeyPair({
    name: `${reserved.userID}@${serverId}`,
    email,
    comment: serverInfo.name,
    password,
  });
  console.log('bare fingerprint:', keyPair.fingerprint);

  const canonicalUserId = `${reserved.userID}@${serverId}`;
  const canonicalFingerprint = `${canonicalUserId}/${keyPair.fingerprint}`;
  console.log('canonical fingerprint:', canonicalFingerprint);

  const signature = btoa(await signMessage(keyPair.publicKey, keyPair.privateKey, password));

  const identityPayload = buildNewUserIdentityPayload(username, canonicalFingerprint);
  const identitySigArmor = await signMessage(identityPayload, keyPair.privateKey, password);
  const userSignature = btoa(identitySigArmor);

  const signupBody = new URLSearchParams({
    username,
    publicKey: btoa(keyPair.publicKey),
    signature,
    userSignature,
    userID: reserved.userID,
    userIDSignature: reserved.signature,
    userIDFingerprint: reserved.fingerprint,
  });

  const signupRes = await fetch(`${BASE}/users/signup`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/x-www-form-urlencoded',
      'X-Syrinx-Device-Id': '550e8400-e29b-41d4-a716-446655440000',
    },
    body: signupBody.toString(),
  });

  const signupText = await signupRes.text();
  console.log('signup status:', signupRes.status);
  if (signupRes.status !== 201) {
    console.error('SIGNUP FAILED:', signupText);
    process.exit(1);
  }
  const user = JSON.parse(signupText);
  console.log('signup response user.id:', user.id);
  console.log('signup response userSignature.fingerprint:', user.userSignature.fingerprint);

  if (user.id !== canonicalUserId) {
    console.error(`MISMATCH: user.id=${user.id} want=${canonicalUserId}`);
    process.exit(1);
  }
  if (user.userSignature.fingerprint !== canonicalFingerprint) {
    console.error(`MISMATCH: user.userSignature.fingerprint=${user.userSignature.fingerprint} want=${canonicalFingerprint}`);
    process.exit(1);
  }

  console.log('\nSMOKE TEST 1 (signup) PASSED');
  console.log(JSON.stringify({ userId: user.id, bareFingerprint: keyPair.fingerprint, canonicalFingerprint, password, privateKey: keyPair.privateKey, publicKey: keyPair.publicKey }));
}

main().catch((err) => {
  console.error('SMOKE TEST FAILED:', err);
  process.exit(1);
});
