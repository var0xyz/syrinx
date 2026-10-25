import type * as api from '$lib/types/api';

// Mirrors what the server writes into a tombstoned ripple.
const tombstones = new Set(['[DELETED]', '[ACCOUNT REMOVED]', '[USER KICKED]']);

/** A tombstoned ripple's text, saying why it's gone. Its content is
 * unsigned, so anything but a known tombstone reads as [DELETED]. */
export function rippleTombstone(ripple: api.Ripple): string {
  return tombstones.has(ripple.content) ? ripple.content : '[DELETED]';
}
