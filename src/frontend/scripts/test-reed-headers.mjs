// Signed reed headers: `replying` and `thread` flatten to dotted keys,
// sorted with the rest, and a thread part never replies or echoes.
import assert from 'node:assert/strict';
import { reedShapeProblem, signedReedMarkdown } from '../src/lib/utils/reedHeaders.ts';

const root = 'bob@home/r0';
const head = 'alice@home/t0';

assert.equal(
  signedReedMarkdown({ id: 'alice@home/r1', userID: 'alice@home', replying: { to: 'bob@home/r5', root }, content: 'hi' }),
  '---\nid: alice@home/r1\nreplying.root: bob@home/r0\nreplying.to: bob@home/r5\nuserID: alice@home\n---\nhi',
  'reply'
);
assert.equal(
  signedReedMarkdown({ id: head, userID: 'alice@home', thread: { head, index: 0 }, content: 'one' }),
  '---\nid: alice@home/t0\nthread.head: alice@home/t0\nthread.index: 0\nuserID: alice@home\n---\none',
  'thread head'
);
assert.equal(
  signedReedMarkdown({ id: 'alice@home/t1', userID: 'alice@home', thread: { head, index: 1 }, content: 'two' }),
  '---\nid: alice@home/t1\nthread.head: alice@home/t0\nthread.index: 1\nuserID: alice@home\n---\ntwo',
  'later part'
);

const part = { id: 'alice@home/t1', userID: 'alice@home', thread: { head, index: 1 } };
assert.equal(reedShapeProblem(part), null, 'a plain part');
assert.equal(reedShapeProblem({ id: 'x', userID: 'alice@home', replying: { to: root, root } }), null, 'a plain reply');
assert.ok(reedShapeProblem({ ...part, replying: { to: root, root } }), 'part that replies');
assert.ok(reedShapeProblem({ ...part, echoing: root }), 'part that echoes');
assert.ok(reedShapeProblem({ id: 'x', userID: 'alice@home', replying: { to: root } }), 'reply without root');
assert.ok(reedShapeProblem({ ...part, thread: { head, index: -1 } }), 'negative index');

console.log('ok   reed headers');
