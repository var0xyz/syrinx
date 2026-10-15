/**
 * countCharacters parity: the SPA counts the same vectors as Go's
 * countCharacters (TestCountCharactersVectors).
 */
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { countCharacters } from '../src/lib/utils/text.ts';

const here = dirname(fileURLToPath(import.meta.url));
const vectors = JSON.parse(
  readFileSync(resolve(here, '../../backend/testdata/character_count_vectors.json'), 'utf8')
);

for (const v of vectors) {
  assert.equal(countCharacters(v.text), v.count, v.name);
  console.log(`ok   ${v.name}`);
}
