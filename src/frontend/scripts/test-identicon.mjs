/**
 * Identicon unit tests.
 */
import assert from 'node:assert/strict';
import { webcrypto } from 'node:crypto';
import {
  identiconFingerprint,
  identiconForUser,
  identiconFromDigest,
  identiconIdentity,
  paletteFromRoot,
  sha256Utf8,
} from '../src/lib/utils/identicon.ts';

if (!globalThis.crypto) {
  globalThis.crypto = webcrypto;
}

const SERVER = 'srv01abc';

assert.equal(identiconIdentity('alice', SERVER), `alice@${SERVER}`);

const a = await identiconForUser('03gkv33nkdd524ker8sgo0c11', SERVER);
const a2 = await identiconForUser('03gkv33nkdd524ker8sgo0c11', SERVER);
assert.equal(identiconFingerprint(a), identiconFingerprint(a2), 'stable for same identity');

const b = await identiconForUser('different-user-id-xxxxx', SERVER);
assert.notEqual(identiconFingerprint(a), identiconFingerprint(b), 'different user ids differ');

const otherServer = await identiconForUser('03gkv33nkdd524ker8sgo0c11', 'otherSrv');
assert.notEqual(identiconFingerprint(a), identiconFingerprint(otherServer), 'different servers differ');

assert.equal(a.size, 11);
assert.equal(a.cells.length, 11);
assert.equal(a.cells[0].length, 11);

const mid = Math.floor(a.size / 2);
for (let r = 0; r < a.size; r++) {
  for (let c = 0; c <= mid; c++) {
    assert.equal(a.cells[r][c], a.cells[r][a.size - 1 - c], 'mirrored');
  }
}

assert.ok(a.background.startsWith('hsl('), 'hash-derived background');

// Scale: fixed intervals relative to root (tonic), not absolute colors.
const scale = [
  { h: 0, s: 0, l: 0 },
  { h: 180, s: -10, l: 5 },
];
const swatches = paletteFromRoot(40, 70, 50, scale);
assert.equal(swatches[0], 'hsl(40 70% 50%)');
assert.equal(swatches[1], 'hsl(220 60% 55%)');
const shifted = paletteFromRoot(100, 70, 50, scale);
assert.equal(shifted[0], 'hsl(100 70% 50%)');
assert.equal(shifted[1], 'hsl(280 60% 55%)');

const digest = await sha256Utf8(identiconIdentity('x', SERVER));
const fromDigest = identiconFromDigest(digest);
assert.equal(
  identiconFingerprint(fromDigest),
  identiconFingerprint(await identiconForUser('x', SERVER))
);

// Spot-check: many ids should not all collapse to one fingerprint.
const seen = new Set();
for (let i = 0; i < 40; i++) {
  seen.add(identiconFingerprint(await identiconForUser(`user-${i}-abcdefghijkl`, SERVER)));
}
assert.ok(seen.size >= 35, `expected high uniqueness, got ${seen.size}`);

// Structural variety: occupancy masks should differ across users (not just hues).
function occupancy(model) {
  return model.cells.map((row) => row.map((c) => (c ? '1' : '0')).join('')).join(';');
}
const masks = new Set();
for (let i = 0; i < 40; i++) {
  masks.add(occupancy(await identiconForUser(`shape-${i}-abcdefghijkl`, SERVER)));
}
assert.ok(masks.size >= 30, `expected distinct silhouettes, got ${masks.size}`);

console.log('identicon tests ok');
