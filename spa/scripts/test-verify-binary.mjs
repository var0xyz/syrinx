#!/usr/bin/env node
/**
 * Regression: binary detached signature verify matches Go DetachSign.
 *
 * Mirrors spa/src/lib/services/crypto.ts verifySignature (binary
 * createMessage + await verified). Signs with binary message bytes the
 * same way the server does, then asserts verify succeeds / fails.
 *
 * Also checks that a public-key-shaped payload (bytesToSign + armor
 * content) round-trips under binary verify — the path that failed on
 * mobile after invite signup.
 */

import * as openpgp from 'openpgp';

function bytesToSign(headers, content) {
  const keys = Object.keys(headers)
    .filter((k) => headers[k] !== '')
    .sort();
  const parts = ['---\n'];
  for (let i = 0; i < keys.length; i++) {
    if (i > 0) parts.push('\n');
    parts.push(keys[i], ': ', headers[keys[i]]);
  }
  if (keys.length > 0) parts.push('\n');
  parts.push('---\n', content);
  return new TextDecoder().decode(new TextEncoder().encode(parts.join('')));
}

const VERIFY_CLOCK_SKEW_MS = 5 * 60 * 1000;

function verificationDate(reference) {
  let refMs = Date.now();
  if (reference !== undefined && reference !== null && reference !== '') {
    const parsed =
      typeof reference === 'number'
        ? reference
        : reference instanceof Date
          ? reference.getTime()
          : Date.parse(reference);
    if (!Number.isNaN(parsed)) {
      refMs = Math.max(refMs, parsed);
    }
  }
  return new Date(refMs + VERIFY_CLOCK_SKEW_MS);
}

/** Same verify path as CryptoService.verifySignature after the fix. */
async function verifySignature(message, signature, publicKeyArmored, at) {
  try {
    const publicKey = await openpgp.readKey({ armoredKey: publicKeyArmored });
    const messageObj = await openpgp.createMessage({
      binary: new TextEncoder().encode(message),
    });
    const signatureObj = await openpgp.readSignature({
      armoredSignature: signature,
    });
    const verificationResult = await openpgp.verify({
      message: messageObj,
      signature: signatureObj,
      verificationKeys: publicKey,
      date: verificationDate(at),
      config: { allowInsecureVerificationWithReformattedKeys: true },
    });
    const verified = verificationResult.signatures[0]?.verified;
    if (!verified) return false;
    await verified;
    return true;
  } catch {
    return false;
  }
}

/** Binary detached sign — mirrors golang.org/x/crypto DetachSign. */
async function signBinary(message, privateKey, passphrase) {
  const decrypted = await openpgp.decryptKey({
    privateKey: await openpgp.readPrivateKey({ armoredKey: privateKey }),
    passphrase,
  });
  const signed = await openpgp.sign({
    message: await openpgp.createMessage({
      binary: new TextEncoder().encode(message),
    }),
    signingKeys: decrypted,
    detached: true,
  });
  return signed.trim();
}

let failed = 0;

function assert(name, cond) {
  if (cond) {
    console.log(`ok   ${name}`);
  } else {
    failed++;
    console.error(`FAIL ${name}`);
  }
}

const passphrase = 'test-passphrase-32-chars-long!!';
const { privateKey, publicKey } = await openpgp.generateKey({
  type: 'ecc',
  userIDs: [{ name: 'verify-binary' }],
  passphrase,
  format: 'armored',
});
const pubEntity = await openpgp.readKey({ armoredKey: publicKey });
const fingerprint = pubEntity.getFingerprint();

const plain = 'hello\nworld\nwith\nnewlines';
const sig = await signBinary(plain, privateKey, passphrase);
assert('binary verify accepts matching payload', await verifySignature(plain, sig, publicKey));
assert(
  'binary verify rejects mutated payload',
  !(await verifySignature(plain + 'x', sig, publicKey))
);

const signedAt = '2026-07-26T22:15:30Z';
const payload = bytesToSign(
  {
    fingerprint,
    serverID: 'TestServer01',
    serverKeyFingerprint: fingerprint,
    signedAt,
    userID: 'userABC',
  },
  publicKey
);
const keySig = await signBinary(payload, privateKey, passphrase);
assert(
  'public-key payload binary verify OK',
  await verifySignature(payload, keySig, publicKey)
);
assert(
  'public-key payload rejects wrong signedAt',
  !(await verifySignature(
    bytesToSign(
      {
        fingerprint,
        serverID: 'TestServer01',
        serverKeyFingerprint: fingerprint,
        signedAt: '2026-07-26T20:15:30Z',
        userID: 'userABC',
      },
      publicKey
    ),
    keySig,
    publicKey
  ))
);

// Phone clock lag: signature created 2 minutes ahead of "now".
const ahead = new Date(Date.now() + 2 * 60 * 1000);
const laggedPlain = 'clock-skew-payload';
const laggedSig = (
  await openpgp.sign({
    message: await openpgp.createMessage({
      binary: new TextEncoder().encode(laggedPlain),
    }),
    signingKeys: await openpgp.decryptKey({
      privateKey: await openpgp.readPrivateKey({ armoredKey: privateKey }),
      passphrase,
    }),
    detached: true,
    date: ahead,
  })
).trim();
let noSkewOk = false;
try {
  const r = await openpgp.verify({
    message: await openpgp.createMessage({
      binary: new TextEncoder().encode(laggedPlain),
    }),
    signature: await openpgp.readSignature({ armoredSignature: laggedSig }),
    verificationKeys: await openpgp.readKey({ armoredKey: publicKey }),
    date: new Date(),
  });
  await r.signatures[0].verified;
  noSkewOk = true;
} catch {
  noSkewOk = false;
}
assert('lagged clock without skew rejects future sig', !noSkewOk);
assert(
  'lagged clock with skew accepts future sig',
  await verifySignature(laggedPlain, laggedSig, publicKey, ahead.toISOString())
);

// Text-mode verify against a binary signature should not be required to
// succeed; we only assert our binary path works (the mobile fix).
const textVerify = async (message, signature, publicKeyArmored) => {
  try {
    const result = await openpgp.verify({
      message: await openpgp.createMessage({ text: message }),
      signature: await openpgp.readSignature({ armoredSignature: signature }),
      verificationKeys: await openpgp.readKey({ armoredKey: publicKeyArmored }),
    });
    await result.signatures[0].verified;
    return true;
  } catch {
    return false;
  }
};
const textOk = await textVerify(payload, keySig, publicKey);
console.log(
  `info text-mode verify of binary sig: ${textOk ? 'ok (engine accepts)' : 'fail (why binary path matters)'}`
);

if (failed > 0) {
  console.error(`\n${failed} test(s) failed`);
  process.exit(1);
}
console.log('\nAll binary verify tests passed');
