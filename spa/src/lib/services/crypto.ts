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
   * Verify a signature with a public key
   */
  async verifySignature(message: string, signature: string, publicKeyArmored: string): Promise<boolean> {
    try {
      const publicKey = await openpgp.readKey({ armoredKey: publicKeyArmored });
      const messageObj = await openpgp.createMessage({ text: message });
      const signatureObj = await openpgp.readSignature({ armoredSignature: signature });

      const verificationResult = await openpgp.verify({
        message: messageObj,
        signature: signatureObj,
        verificationKeys: publicKey
      });

      return verificationResult.signatures[0]?.verified || false;
    } catch (error) {
      console.error('Error verifying signature:', error);
      return false;
    }
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
