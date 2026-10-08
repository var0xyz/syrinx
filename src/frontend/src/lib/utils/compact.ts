function isPlainObject(value: unknown): value is Record<string, unknown> {
  if (value === null || typeof value !== 'object') return false;
  const proto = Object.getPrototypeOf(value);
  return proto === Object.prototype || proto === null;
}

function isEmpty(value: unknown): boolean {
  if (value === undefined || value === null || value === '') return true;
  if (Array.isArray(value)) return value.length === 0;
  return isPlainObject(value) && Object.keys(value).length === 0;
}

/** Deep copy without undefined, null, '', [] or {} fields; 0 and false stay.
 * Array elements are compacted in place, never dropped, so indexes hold. */
export function compact<T>(value: T): T {
  if (Array.isArray(value)) return value.map((item) => compact(item)) as T;
  if (!isPlainObject(value)) return value;
  const out: Record<string, unknown> = {};
  for (const [key, field] of Object.entries(value)) {
    const compacted = compact(field);
    if (!isEmpty(compacted)) out[key] = compacted;
  }
  return out as T;
}
