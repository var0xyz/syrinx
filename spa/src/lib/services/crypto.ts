import * as openpgp from 'openpgp';

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

export type VerifySignatureResult =
  | { ok: true; mode: 'binary' | 'text' }
  | { ok: false; error: string };

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
   */
  async verifySignature(
    message: string,
    signature: string,
    publicKeyArmored: string
  ): Promise<boolean> {
    return (await this.verifySignatureDetailed(message, signature, publicKeyArmored)).ok;
  }

  /**
   * Like verifySignature, but keeps the OpenPGP rejection reasons so callers
   * (signup diagnose) can surface them on mobile.
   */
  async verifySignatureDetailed(
    message: string,
    signature: string,
    publicKeyArmored: string
  ): Promise<VerifySignatureResult> {
    const modes: Array<'binary' | 'text'> = ['binary', 'text'];
    const errors: string[] = [];
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
          verificationKeys: publicKey
        });

        const verified = verificationResult.signatures[0]?.verified;
        if (!verified) {
          errors.push(`${mode}: no signature slot`);
          continue;
        }
        await verified;
        return { ok: true, mode };
      } catch (error) {
        const msg = error instanceof Error ? error.message : String(error);
        console.error(`Error verifying signature (${mode}):`, error);
        errors.push(`${mode}: ${msg}`);
      }
    }
    return { ok: false, error: errors.join(' | ') || 'verification failed' };
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
