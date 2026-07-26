import { test, expect } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');

test.describe('Binary detached signature verify', () => {
  test('crypto.ts verifies with binary then text and awaits verified', () => {
    const src = readFileSync(join(root, 'src/lib/services/crypto.ts'), 'utf8');
    expect(src).toContain("modes: Array<'binary' | 'text'> = ['binary', 'text']");
    expect(src).toContain('binary: new TextEncoder().encode(message)');
    expect(src).toContain('await verified');
    expect(src).toContain('verificationDate');
    expect(src).toContain('VERIFY_CLOCK_SKEW_MS');
    expect(src).not.toContain('signatures[0]?.verified || false');
  });

  test('signedAtHeader prefers canonical wire Z timestamps', () => {
    const src = readFileSync(join(root, 'src/lib/services/verify.ts'), 'utf8');
    expect(src).toContain('Prefer the wire string when it is already canonical');
    expect(src).toContain('signedAtRaw=');
  });

  test('publicKey put surfaces diagnosePublicKey reason', () => {
    const repo = readFileSync(
      join(root, 'src/lib/repositories/publicKey.ts'),
      'utf8'
    );
    expect(repo).toContain('diagnosePublicKey');
    expect(repo).toContain('verification failed: ${diagnosis.reason}');

    const verifiers = readFileSync(
      join(root, 'src/lib/verifiers/index.ts'),
      'utf8'
    );
    expect(verifiers).toContain('export async function diagnosePublicKey');
  });

  test('binary detached verify works in browser WebCrypto', async ({ page }) => {
    await page.goto('/');
    await page.addScriptTag({
      url: 'https://unpkg.com/openpgp@6.2.2/dist/openpgp.min.js',
    });

    const result = await page.evaluate(async () => {
      // Loaded via CDN script tag; not the narrow window.openpgp typings.
      const openpgp = (window as any).openpgp;
      const passphrase = 'test-passphrase-32-chars-long!!';
      const { privateKey, publicKey } = await openpgp.generateKey({
        type: 'ecc',
        userIDs: [{ name: 'browser-verify' }],
        passphrase,
        format: 'armored',
      });

      const decrypted = await openpgp.decryptKey({
        privateKey: await openpgp.readPrivateKey({ armoredKey: privateKey }),
        passphrase,
      });

      const message = '---\nfingerprint: abc\n---\nkey-armor\n';
      const signature = (
        await openpgp.sign({
          message: await openpgp.createMessage({
            binary: new TextEncoder().encode(message),
          }),
          signingKeys: decrypted,
          detached: true,
        })
      ).trim();

      async function verifyBinary(msg: string, sig: string, pub: string) {
        try {
          const verificationResult = await openpgp.verify({
            message: await openpgp.createMessage({
              binary: new TextEncoder().encode(msg),
            }),
            signature: await openpgp.readSignature({ armoredSignature: sig }),
            verificationKeys: await openpgp.readKey({ armoredKey: pub }),
          });
          const verified = verificationResult.signatures[0]?.verified;
          if (!verified) return false;
          await verified;
          return true;
        } catch {
          return false;
        }
      }

      const ok = await verifyBinary(message, signature, publicKey);
      const bad = await verifyBinary(message + 'x', signature, publicKey);
      return { ok, bad };
    });

    expect(result.ok).toBe(true);
    expect(result.bad).toBe(false);
  });
});
