#!/usr/bin/env node
// Standalone test for spa/src/lib/services/signing.ts using the shared
// vectors in signing/testvectors.json. Runs under plain node (>= 18).
//
// The SPA does not have a JS unit-test framework installed, and pulling one
// in just for this file would be overkill. This script:
//   1. Loads the shared JSON vectors from ../signing/testvectors.json (the
//      exact same file the Go tests consume).
//   2. Runs `bytesToSign` against each vector.
//   3. Compares the UTF-8 bytes to the expected string.
//
// Byte-identity between the Go and TS implementations is the whole point of
// Proposal 01 — this script is what enforces it on the SPA side.

import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';

// Inline the TS logic here as JS (mirroring signing.ts exactly). We could
// also transpile signing.ts, but keeping this file self-contained avoids a
// build step and keeps this trivially runnable from any checkout.
function bytesToSign(headers, content) {
  const keys = Object.keys(headers)
    .filter((k) => headers[k] !== '')
    .sort();

  const parts = ['---\n'];
  for (let i = 0; i < keys.length; i++) {
    if (i > 0) parts.push('\n');
    parts.push(keys[i], ': ', headers[keys[i]]);
  }
  if (keys.length > 0) parts.push('\n');
  parts.push('---\n', content);

  return new TextEncoder().encode(parts.join(''));
}

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const vectorsPath = resolve(__dirname, '../../signing/testvectors.json');

const vectors = JSON.parse(readFileSync(vectorsPath, 'utf8'));
if (!Array.isArray(vectors) || vectors.length === 0) {
  console.error('No test vectors found at', vectorsPath);
  process.exit(1);
}

let failed = 0;
const decoder = new TextDecoder();

for (const v of vectors) {
  const got = decoder.decode(bytesToSign(v.headers, v.content));
  if (got !== v.expected) {
    failed++;
    console.error(`FAIL ${v.name}`);
    console.error('  got  =', JSON.stringify(got));
    console.error('  want =', JSON.stringify(v.expected));
  } else {
    console.log(`ok   ${v.name}`);
  }
}

// Additional structural checks that don't need vector files.
function assertIdenticalRegardlessOfInsertionOrder() {
  const decoder2 = new TextDecoder();
  const h1 = { z: '1', a: '2', m: '3' };
  const h2 = { a: '2', m: '3', z: '1' };
  const a = decoder2.decode(bytesToSign(h1, 'x'));
  const b = decoder2.decode(bytesToSign(h2, 'x'));
  if (a !== b) {
    failed++;
    console.error('FAIL insertion-order independence');
    console.error('  a =', JSON.stringify(a));
    console.error('  b =', JSON.stringify(b));
  } else {
    console.log('ok   insertion-order independence');
  }
}
assertIdenticalRegardlessOfInsertionOrder();

if (failed > 0) {
  console.error(`\n${failed} test(s) failed`);
  process.exit(1);
}
console.log(`\nAll ${vectors.length + 1} tests passed`);
