import { parseReedRef } from '$lib/utils/reedRef';
import type { ReedType } from '$lib/types/reed';

/** Echo with no commentary — feeds should show the original instead. */
export function isBlankEcho(reed: { content?: string; echoing?: string } | null | undefined): boolean {
  return !!(reed?.echoing && !(reed.content || '').trim());
}

type GetReed = (authorId: string, reedId: string) => Promise<ReedType | null>;

const MAX_BLANK_UNWRAP = 8;

/**
 * Walk blank-echo links until a reed with content (or no further echo) is found.
 */
export async function resolveBlankEchoChain(
  reed: ReedType,
  getReed: GetReed
): Promise<ReedType> {
  let current = reed;
  const seen = new Set<string>();
  for (let i = 0; i < MAX_BLANK_UNWRAP; i++) {
    if (!isBlankEcho(current)) return current;
    if (seen.has(current.id)) return current;
    seen.add(current.id);
    const ref = parseReedRef(current.echoing);
    if (!ref) return current;
    const next = await getReed(ref.authorId, ref.reedId);
    if (!next) return current;
    current = next;
  }
  return current;
}

/**
 * Sync unwrap using an already-loaded echoing-ref → reed map
 * (keys are full `userID@serverID/reedID` strings).
 */
export function resolveBlankEchoFromMap(
  reed: ReedType,
  echoByRef: Map<string, ReedType>
): ReedType {
  let current = reed;
  const seen = new Set<string>();
  for (let i = 0; i < MAX_BLANK_UNWRAP; i++) {
    if (!isBlankEcho(current)) return current;
    if (seen.has(current.id)) return current;
    seen.add(current.id);
    const key = current.echoing;
    if (!key) return current;
    const next = echoByRef.get(key);
    if (!next) return current;
    current = next;
  }
  return current;
}
