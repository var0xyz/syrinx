/**
 * The server's public key, pasted in by the user out-of-band on first run
 * (see routes/+page.svelte's gate) instead of ever being fetched from the
 * server itself. Its fingerprint proves possession on every request to a
 * route that isn't otherwise authenticated (see serverKeyProofHeader).
 */

import { cryptoService } from './crypto';

const ARMOR_KEY = 'serverKeyArmor';
const FINGERPRINT_KEY = 'serverKeyFingerprint';

export const SERVER_KEY_PROOF_HEADER = 'X-Syrinx-Server-Key-Fingerprint';

export function getTrustedServerKey(): { armor: string; fingerprint: string } | null {
  if (typeof localStorage === 'undefined') return null;
  const armor = localStorage.getItem(ARMOR_KEY);
  const fingerprint = localStorage.getItem(FINGERPRINT_KEY);
  if (!armor || !fingerprint) return null;
  return { armor, fingerprint };
}

export function hasTrustedServerKey(): boolean {
  return getTrustedServerKey() !== null;
}

/** Validates armor is a parseable public key and stores it with its
 * derived fingerprint. Throws if armor isn't valid key material. */
export async function setTrustedServerKey(armor: string): Promise<string> {
  const fingerprint = await cryptoService.fingerprintFromArmor(armor);
  localStorage.setItem(ARMOR_KEY, armor);
  localStorage.setItem(FINGERPRINT_KEY, fingerprint);
  return fingerprint;
}

export function clearTrustedServerKey(): void {
  if (typeof localStorage === 'undefined') return;
  localStorage.removeItem(ARMOR_KEY);
  localStorage.removeItem(FINGERPRINT_KEY);
}

export function serverKeyProofHeader(): Record<string, string> {
  const trusted = getTrustedServerKey();
  return trusted ? { [SERVER_KEY_PROOF_HEADER]: trusted.fingerprint } : {};
}
