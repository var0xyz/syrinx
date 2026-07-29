/** Shared alphabet/length for server, user, reed, and invite IDs (matches syrinx/ids). */
const ALPHABET = 'abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789';

export const ID_LENGTH = 8;

export function generateId(length = ID_LENGTH): string {
  const out = new Array<string>(length);
  const bytes = new Uint8Array(length);
  crypto.getRandomValues(bytes);
  for (let i = 0; i < length; i++) {
    out[i] = ALPHABET[bytes[i]! % ALPHABET.length]!;
  }
  return out.join('');
}
