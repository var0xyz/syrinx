#!/usr/bin/env node
/**
 * Regression: binary detached verify + clock-skew tolerance.
 * Mirrors spa/src/lib/services/crypto.ts verifySignature.
 */

import * as openpgp from 'openpgp';

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

async function verifySignature(message, signature, publicKeyArmored, at) {
  const modes = ['binary', 'text'];
  const date = verificationDate(at);
  for (const mode of modes) {
    try {
      const publicKey = await openpgp.readKey({ armoredKey: publicKeyArmored });
      const messageObj =
        mode === 'binary'
          ? await openpgp.createMessage({
              binary: new TextEncoder().encode(message),
            })
          : await openpgp.createMessage({ text: message });
      const signatureObj = await openpgp.readSignature({
        armoredSignature: signature,
      });
      const verificationResult = await openpgp.verify({
        message: messageObj,
        signature: signatureObj,
        verificationKeys: publicKey,
        date,
      });
      const verified = verificationResult.signatures[0]?.verified;
      if (!verified) continue;
      await verified;
      return true;
    } catch {
      // try next mode
    }
  }
  return false;
}

async function signBinary(message, privateKey, passphrase, date) {
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
    ...(date ? { date } : {}),
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

const plain = 'hello\nworld\nwith\nnewlines';
const sig = await signBinary(plain, privateKey, passphrase);
assert('binary verify accepts matching payload', await verifySignature(plain, sig, publicKey));
assert(
  'binary verify rejects mutated payload',
  !(await verifySignature(plain + 'x', sig, publicKey))
);

// Phone clock lag: signature created 2 minutes ahead of "now".
const ahead = new Date(Date.now() + 2 * 60 * 1000);
const laggedPlain = 'clock-skew-payload';
const laggedSig = await signBinary(laggedPlain, privateKey, passphrase, ahead);
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

if (failed) {
  console.error(`\n${failed} test(s) failed`);
  process.exit(1);
}
console.log('\nAll binary verify tests passed');
