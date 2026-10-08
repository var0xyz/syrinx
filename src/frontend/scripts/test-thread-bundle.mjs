// A relayed thread is accepted only when every part matches the record:
// listed in order, by its author, naming its own place.
import assert from 'node:assert/strict';
import { threadBundleMismatch } from '../src/lib/utils/threadBundle.ts';

const author = 'alice@home';
const ids = ['alice@home/r0', 'alice@home/r1', 'alice@home/r2'];
const record = { threadID: ids[0], userID: author, reedIDs: ids };
const parts = ids.map((id, index) => ({ id, userID: author, thread: { head: ids[0], index } }));

assert.equal(threadBundleMismatch(ids[0], record, parts), null, 'a matching bundle passes');

const fails = {
  'record for another thread': [ids[1], record, parts],
  'a part missing': [ids[0], record, parts.slice(0, 2)],
  'parts out of order': [ids[0], record, [parts[0], parts[2], parts[1]]],
  'a part by someone else': [ids[0], record, [parts[0], { ...parts[1], userID: 'eve@home' }, parts[2]]],
  'a part naming another index': [ids[0], record, [parts[0], { ...parts[1], thread: { head: ids[0], index: 2 } }, parts[2]]],
  'a part naming another head': [ids[0], record, [parts[0], { ...parts[1], thread: { head: 'x', index: 1 } }, parts[2]]],
  'a part that is also a reply': [ids[0], record, [parts[0], { ...parts[1], replying: 'bob@home/r9' }, parts[2]]],
};
for (const [name, args] of Object.entries(fails)) {
  assert.notEqual(threadBundleMismatch(...args), null, name);
}

console.log('ok   thread bundles');
