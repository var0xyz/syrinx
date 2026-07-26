import * as openpgp from 'openpgp/lightweight';

export interface KeyPair {
  fingerprint: string;
  privateKey: string;
  publicKey: string;
}

export interface KeyGenerationOptions {
  name: string;
  email?: string;
  password: string;
}

/**
 * OpenPGP.js rejects signatures when `signature.created > verifyDate`.
 * Mobile clocks often lag the server by a few seconds (or more), so a
 * freshly minted server countersignature looks "in the future". Allow a
 * small skew window; optionally pin to a server-provided reference time.
 */
export const VERIFY_CLOCK_SKEW_MS = 5 * 60 * 1000;

export function verificationDate(reference?: Date | string | number): Date {
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

async function getFingerprint(publicKey: string) {
  const entity = await openpgp.readKey({ armoredKey: publicKey });
  return String(entity.getFingerprint());
}

export class CryptoService {
  /**
   * Generate a new OpenPGP key pair
   */
  async generateKeyPair(options: KeyGenerationOptions): Promise<KeyPair> {
    const { name: userId, email, password } = options;

    try {
      // Create user ID - use email if provided, otherwise just username
      const identity = email ? { name: userId, email } : { name: userId };

      // Generate key pair
      const { privateKey, publicKey } = await openpgp.generateKey({
        type: 'ecc', // Use ECC (Elliptic Curve Cryptography)
        userIDs: [identity],
        passphrase: password,
        format: 'armored' // Return keys in ASCII-armored format
      });

      const fingerprint = await getFingerprint(publicKey);

      console.log({
        fingerprint: fingerprint,
        privateKey: privateKey,
        publicKey: publicKey
      });

      return {
        fingerprint: fingerprint,
        privateKey: privateKey,
        publicKey: publicKey
      };
    } catch (error) {
      console.error('Error generating key pair:', error);
      throw new Error('Failed to generate encryption keys');
    }
  }

  /**
   * Read and decrypt a private key with the given passphrase
   */
  async decryptPrivateKey(privateKeyArmored: string, passphrase: string) {
    try {
      const privateKey = await openpgp.readPrivateKey({
        armoredKey: privateKeyArmored
      });
      const decryptedPrivateKey = await openpgp.decryptKey({
        privateKey,
        passphrase
      });
      return decryptedPrivateKey;
    } catch (error) {
      console.error('Error decrypting private key:', error);
      throw new Error('Failed to decrypt private key');
    }
  }

  /**
   * Sign a message with a private key
   */
  async signMessage(message: string, privateKeyArmored: string, passphrase: string): Promise<string> {
    try {
      const privateKey = await this.decryptPrivateKey(privateKeyArmored, passphrase);
      const messageObj = await openpgp.createMessage({ text: message });

      const signedMessage = await openpgp.sign({
        message: messageObj,
        signingKeys: privateKey,
        detached: true
      });

      return signedMessage.trim();
    } catch (error) {
      console.error('Error signing message:', error);
      throw new Error('Failed to sign message');
    }
  }

  /**
   * Verify a detached signature with a public key.
   *
   * Server countersignatures use Go `DetachSign` (SigTypeBinary). Prefer binary
   * message bytes; fall back to text for engine quirks. OpenPGP.js exposes
   * `verified` as a Promise that rejects on failure — always await it.
   *
   * `at` is typically the server countersignature timestamp; verification
   * uses max(now, at) + skew so lagged device clocks do not reject fresh
   * signatures as "creation time is in the future".
   */
  async verifySignature(
    message: string,
    signature: string,
    publicKeyArmored: string,
    at?: Date | string
  ): Promise<boolean> {
    const modes: Array<'binary' | 'text'> = ['binary', 'text'];
    const date = verificationDate(at);
    for (const mode of modes) {
      try {
        const publicKey = await openpgp.readKey({ armoredKey: publicKeyArmored });
        const messageObj =
          mode === 'binary'
            ? await openpgp.createMessage({
                binary: new TextEncoder().encode(message)
              })
            : await openpgp.createMessage({ text: message });
        const signatureObj = await openpgp.readSignature({ armoredSignature: signature });

        const verificationResult = await openpgp.verify({
          message: messageObj,
          signature: signatureObj,
          verificationKeys: publicKey,
          date
        });

        const verified = verificationResult.signatures[0]?.verified;
        if (!verified) continue;
        await verified;
        return true;
      } catch (error) {
        console.error(`Error verifying signature (${mode}):`, error);
      }
    }
    return false;
  }

  /**
   * Encrypt binary data with a password using OpenPGP symmetric encryption
   */
  async encryptBackup(data: Uint8Array, password: string): Promise<Uint8Array> {
    const message = await openpgp.createMessage({ binary: data });
    const encrypted = await openpgp.encrypt({
      message,
      passwords: [password],
      format: 'binary'
    });
    return encrypted as Uint8Array;
  }

  /**
   * Decrypt binary data encrypted with encryptBackup
   */
  async decryptBackup(data: Uint8Array, password: string): Promise<Uint8Array> {
    const message = await openpgp.readMessage({ binaryMessage: data });
    const { data: decrypted } = await openpgp.decrypt({
      message,
      passwords: [password],
      format: 'binary'
    });
    return decrypted as Uint8Array;
  }

  /**
   * Re-derive fingerprint from armored public key material.
   */
  async fingerprintFromArmor(publicKey: string): Promise<string> {
    return getFingerprint(publicKey);
  }

  /**
   * Extract identity from a public key
   */
  async getKeyIdentity(publicKey: string): Promise<string> {
    try {
      const entity = await openpgp.readKey({ armoredKey: publicKey });
      const userIds = entity.getUserIDs();
      if (userIds.length > 0) {
        return userIds[0];
      }
      return 'Unknown';
    } catch (error) {
      console.error('Error extracting key identity:', error);
      throw new Error('Failed to extract key identity');
    }
  }
}

export const cryptoService = new CryptoService();
