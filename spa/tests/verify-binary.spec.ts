import { test, expect } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');

test.describe('Binary detached signature verify', () => {
  test('crypto.ts verifies with binary then text, awaits verified, and skews date', () => {
    const src = readFileSync(join(root, 'src/lib/services/crypto.ts'), 'utf8');
    expect(src).toContain("import * as openpgp from 'openpgp/lightweight'");
    expect(src).toContain("modes: Array<'binary' | 'text'> = ['binary', 'text']");
    expect(src).toContain('binary: new TextEncoder().encode(message)');
    expect(src).toContain('await verified');
    expect(src).toContain('verificationDate');
    expect(src).toContain('VERIFY_CLOCK_SKEW_MS');
    expect(src).toContain('date');
    expect(src).not.toContain('signatures[0]?.verified || false');
    expect(src).not.toContain('verifySignatureDetailed');
  });

  test('verify.ts passes signedAt into verifySignature for clock skew', () => {
    const src = readFileSync(join(root, 'src/lib/services/verify.ts'), 'utf8');
    expect(src).toContain('signedAtHeader(serverSignature.timestamp)');
    expect(src).toContain('atob(serverSignature.armor)');
    expect(src).toContain('signedAt');
    expect(src).not.toContain('verifySignatureDetailed');
    expect(src).not.toContain('payloadBytes');
  });
});
