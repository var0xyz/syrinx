// The decode layer must hand consumers the shapes the JSON wire did:
// json_name keys, ISO timestamps, null for unset optional and message
// fields, and the certificates' `type` tags.
import assert from 'node:assert/strict';
import { create, toBinary } from '@bufbuild/protobuf';
import { decodeShape, encodeShape, fromShape, toShape } from '../src/lib/services/wire.ts';
import {
  ErrorSchema,
  KeyRevocationCertSchema,
  ReedRemovalCertSchema,
  RippleSchema,
  ServerSignatureSchema,
  ThreadRemovalSchema,
} from '../src/lib/proto/common_pb.ts';

const ok = (name) => console.log(`ok   ${name}`);

{
  const sig = create(ServerSignatureSchema, { id: 'k@s', armor: 'a', signedAt: 1767225600n });
  assert.deepEqual(toShape(ServerSignatureSchema, sig), { id: 'k@s', armor: 'a', timestamp: '2026-01-01T00:00:00Z' });
  ok('int64 becomes a second-precision ISO string under its json_name');
}

{
  const back = fromShape(ServerSignatureSchema, { id: 'k@s', armor: 'a', timestamp: '2026-01-01T00:00:00Z' });
  assert.equal(back.signedAt, 1767225600n);
  ok('ISO string becomes unix seconds');
}

{
  const cert = create(ReedRemovalCertSchema, { serverId: 's', userId: 'u@s', reedId: 'u@s/r' });
  const shape = toShape(ReedRemovalCertSchema, cert);
  assert.equal(shape.type, 'reed');
  assert.equal(shape.serverID, 's');
  assert.equal(shape.reedID, 'u@s/r');
  assert.equal(shape.userSignature, null);
  ok('certificate gets its type tag; unset message reads null');
}

{
  const rev = toShape(KeyRevocationCertSchema, create(KeyRevocationCertSchema, { id: 'k', reason: '' }));
  assert.equal(rev.successor, null);
  assert.equal(rev.successorSignature, null);
  assert.equal(rev.reason, '');
  ok('unset optional string reads null, empty implicit string stays empty');
}

{
  const ripple = toShape(RippleSchema, create(RippleSchema, { hash: 'h', postedAt: 0n }));
  assert.equal(ripple.replyingTo, null);
  assert.equal(ripple.postedAt, null);
  ok('zero timestamp reads null');
}

{
  const removal = create(ThreadRemovalSchema, {
    cert: { threadId: 't' },
    record: { threadId: 't', reedIds: ['a', 'b'] },
  });
  const shape = toShape(ThreadRemovalSchema, removal);
  assert.equal(shape.cert.type, 'thread_removal');
  assert.equal(shape.record.type, 'thread');
  assert.deepEqual(shape.record.reedIDs, ['a', 'b']);
  ok('nested messages and lists keep their shapes');
}

{
  const error = create(ErrorSchema, {
    message: 'Account removed',
    detail: { case: 'accountRemoval', value: { userId: 'u@s', note: 'bye' } },
  });
  const shape = decodeShape(ErrorSchema, toBinary(ErrorSchema, error));
  assert.equal(shape.message, 'Account removed');
  assert.equal(shape.accountRemoval.type, 'account');
  assert.equal(shape.accountRemoval.note, 'bye');
  assert.equal('reedRemoval' in shape, false);
  ok('Error detail decodes to the one set oneof case');
}

{
  const shape = { id: 'k@s', armor: 'a', timestamp: '2026-01-01T00:00:00Z' };
  assert.deepEqual(decodeShape(ServerSignatureSchema, encodeShape(ServerSignatureSchema, shape)), shape);
  ok('encode then decode round-trips a shape');
}

console.log('\nAll wire tests passed');
