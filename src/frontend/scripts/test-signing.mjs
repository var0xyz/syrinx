#!/usr/bin/env node
// canonicalJSON parity: runs signing.ts against the vectors the Go
// TestCanonicalJSONVectors asserts, so the two sides can't drift apart.
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';
import { canonicalJSON } from '../src/lib/services/signing.ts';

const here = dirname(fileURLToPath(import.meta.url));
const vectors = JSON.parse(
  readFileSync(resolve(here, '../../backend/testdata/canonical_json_vectors.json'), 'utf8')
);
assert.ok(vectors.length > 0, 'no vectors found');

for (const v of vectors) {
  assert.equal(canonicalJSON(v.fields), v.expected, v.name);
  console.log(`ok   ${v.name}`);
}

assert.equal(
  canonicalJSON({ z: '1', a: '2', m: '3' }),
  canonicalJSON({ a: '2', m: '3', z: '1' }),
  'insertion order must not matter'
);
console.log('ok   insertion-order independence');
