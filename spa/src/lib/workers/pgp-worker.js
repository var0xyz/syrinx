/**
 * Dedicated Web Worker for PGP signing operations
 * This worker holds the decrypted private key in memory for signing requests
 */

import * as openpgp from 'openpgp';

let privateKey = null;
let keyReady = false;

// Message handler for communication with service worker
self.onmessage = async (event) => {
  const { type, data, id } = event.data;

  try {
    if (type === 'INIT_KEY') {
      const { armoredKey, passphrase } = data;

      // Read and decrypt the private key
      const key = await openpgp.readPrivateKey({ armoredKey });
      privateKey = await openpgp.decryptKey({
        privateKey: key,
        passphrase
      });

      keyReady = true;
      self.postMessage({ type: 'READY', id });
    }

    if (type === 'SIGN' && keyReady) {
      const { text } = data;

      // Create message and sign it
      const message = await openpgp.createMessage({ text });
      const signature = await openpgp.sign({
        message,
        signingKeys: privateKey,
        detached: true,
        format: 'armored'
      });

      console.log('Signature:', signature.trim());

      self.postMessage({
        type: 'SIGNED',
        id: data.id,
        signature: signature.trim()
      });
    }

    if (type === 'CLEAR') {
      privateKey = null;
      keyReady = false;
      self.postMessage({ type: 'CLEARED', id });
    }

  } catch (error) {
    console.error('PGP Worker error:', error);
    self.postMessage({
      type: 'ERROR',
      id,
      error: error.message
    });
  }
};
