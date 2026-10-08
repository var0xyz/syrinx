// A reed's signed payload nests `replying` and `thread`, and a thread part
// never replies or echoes.
import assert from 'node:assert/strict';
import { reedShapeProblem } from '../src/lib/utils/reedHeaders.ts';
import { buildReedUserPayload } from '../src/lib/services/signing.ts';

const root = 'bob@home/r0';
const head = 'alice@home/t0';

assert.equal(
  buildReedUserPayload({ id: 'alice@home/r1', userID: 'alice@home', replying: { to: 'bob@home/r5', root }, content: 'hi' }),
  '{"content":"hi","id":"alice@home/r1","replying":{"root":"bob@home/r0","to":"bob@home/r5"},"userID":"alice@home"}',
  'reply'
);
assert.equal(
  buildReedUserPayload({ id: head, userID: 'alice@home', thread: { head, index: 0 }, content: 'one' }),
  '{"content":"one","id":"alice@home/t0","thread":{"head":"alice@home/t0","index":0},"userID":"alice@home"}',
  'thread head'
);
assert.equal(
  buildReedUserPayload({ id: 'alice@home/t1', userID: 'alice@home', thread: { head, index: 1 }, content: 'two' }),
  '{"content":"two","id":"alice@home/t1","thread":{"head":"alice@home/t0","index":1},"userID":"alice@home"}',
  'later part'
);
assert.equal(
  buildReedUserPayload({ id: 'alice@home/e1', userID: 'alice@home', echoing: root, content: '' }),
  '{"echoing":"bob@home/r0","id":"alice@home/e1","userID":"alice@home"}',
  'blank echo drops its empty content'
);
assert.equal(
  buildReedUserPayload({ id: 'alice@home/r2', userID: 'alice@home', replying: null, thread: undefined, content: 'x' }),
  buildReedUserPayload({ id: 'alice@home/r2', userID: 'alice@home', content: 'x' }),
  'null and absent headers sign the same'
);

const part = { id: 'alice@home/t1', userID: 'alice@home', thread: { head, index: 1 } };
assert.equal(reedShapeProblem(part), null, 'a plain part');
assert.equal(reedShapeProblem({ id: 'x', userID: 'alice@home', replying: { to: root, root } }), null, 'a plain reply');
assert.ok(reedShapeProblem({ ...part, replying: { to: root, root } }), 'part that replies');
assert.ok(reedShapeProblem({ ...part, echoing: root }), 'part that echoes');
assert.ok(reedShapeProblem({ id: 'x', userID: 'alice@home', replying: { to: root } }), 'reply without root');
assert.ok(reedShapeProblem({ ...part, thread: { head, index: -1 } }), 'negative index');

console.log('ok   reed headers');
