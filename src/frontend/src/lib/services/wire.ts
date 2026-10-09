// Converts protobuf messages to and from the plain shapes in lib/types/api.ts:
// keys are each field's json_name, and every int64 (a unix-seconds
// timestamp) is an ISO string.

import {
  create,
  fromBinary,
  toBinary,
  ScalarType,
  type DescField,
  type DescMessage,
  type MessageShape,
} from '@bufbuild/protobuf';

export const PROTOBUF_CONTENT_TYPE = 'application/x-protobuf';

/** The `type` discriminator certificates carried on the JSON wire. */
const TYPE_TAGS: Record<string, string> = {
  'syrinx.ReedRemovalCert': 'reed',
  'syrinx.AccountRemovalCert': 'account',
  'syrinx.BlockCert': 'block',
  'syrinx.ThreadRemovalCert': 'thread_removal',
  'syrinx.ThreadRecord': 'thread',
  'syrinx.ServerKeyRevocation': 'server-key-revocation',
};

function isTimestamp(scalar: ScalarType | undefined): boolean {
  return scalar === ScalarType.INT64;
}

function secondsToISO(seconds: bigint): string | null {
  if (seconds === 0n) return null;
  return new Date(Number(seconds) * 1000).toISOString().replace(/\.\d{3}Z$/, 'Z');
}

function isoToSeconds(value: unknown): bigint {
  if (value == null || value === '') return 0n;
  const ms = typeof value === 'number' ? value : Date.parse(String(value));
  return Number.isNaN(ms) ? 0n : BigInt(Math.floor(ms / 1000));
}

function fieldValue(msg: Record<string, unknown>, field: DescField): unknown {
  if (field.oneof) {
    const slot = msg[field.oneof.localName] as { case?: string; value?: unknown } | undefined;
    return slot?.case === field.localName ? slot.value : undefined;
  }
  return msg[field.localName];
}

function scalarToShape(scalar: ScalarType, value: unknown): unknown {
  return isTimestamp(scalar) ? secondsToISO(value as bigint) : value;
}

/** A decoded message as the plain object the rest of the SPA expects.
 * Unset optional and message fields read as null, lists as []. */
export function toShape(schema: DescMessage, msg: object): any {
  const source = msg as Record<string, unknown>;
  const out: Record<string, unknown> = {};
  const tag = TYPE_TAGS[schema.typeName];
  if (tag) out.type = tag;
  for (const field of schema.fields) {
    const value = fieldValue(source, field);
    switch (field.fieldKind) {
      case 'scalar':
        if (field.oneof && value === undefined) continue;
        out[field.jsonName] = value === undefined ? null : scalarToShape(field.scalar, value);
        break;
      case 'message':
        if (field.oneof && value === undefined) continue;
        out[field.jsonName] = value === undefined ? null : toShape(field.message, value as object);
        break;
      case 'list':
        out[field.jsonName] = ((value as unknown[]) ?? []).map((item) =>
          field.listKind === 'message'
            ? toShape(field.message, item as object)
            : field.listKind === 'scalar'
              ? scalarToShape(field.scalar, item)
              : item,
        );
        break;
      default:
        out[field.jsonName] = value ?? null;
    }
  }
  return out;
}

/** The inverse of toShape: a message built from a plain object. */
export function fromShape<Desc extends DescMessage>(schema: Desc, shape: object): MessageShape<Desc> {
  const source = shape as Record<string, unknown>;
  const init: Record<string, unknown> = {};
  for (const field of schema.fields) {
    const value = source[field.jsonName];
    if (value == null) continue;
    let converted: unknown;
    switch (field.fieldKind) {
      case 'scalar':
        converted = isTimestamp(field.scalar) ? isoToSeconds(value) : value;
        break;
      case 'message':
        converted = fromShape(field.message, value as object);
        break;
      case 'list':
        converted = (value as unknown[]).map((item) =>
          field.listKind === 'message'
            ? fromShape(field.message, item as object)
            : field.listKind === 'scalar' && isTimestamp(field.scalar)
              ? isoToSeconds(item)
              : item,
        );
        break;
      default:
        converted = value;
    }
    if (field.oneof) {
      init[field.oneof.localName] = { case: field.localName, value: converted };
    } else {
      init[field.localName] = converted;
    }
  }
  return create(schema, init as never) as MessageShape<Desc>;
}

/** Encodes a plain object as schema's protobuf bytes. */
export function encodeShape(schema: DescMessage, shape: object): Uint8Array<ArrayBuffer> {
  return toBinary(schema, fromShape(schema, shape)) as Uint8Array<ArrayBuffer>;
}

/** Decodes protobuf bytes as schema, into its plain-object shape. */
export function decodeShape(schema: DescMessage, bytes: Uint8Array): any {
  return toShape(schema, fromBinary(schema, bytes));
}
