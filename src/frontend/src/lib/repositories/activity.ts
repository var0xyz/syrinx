/**
 * Activity list: latest reed per followed author, for the activity sidebar.
 * localStorage-backed (not IndexedDB) — this is a lightweight, ephemeral
 * view cache, not data that needs to persist or be backed up.
 */
import { localStorageService } from '$lib/services/localstorage';
import { isBlankEcho } from '$lib/utils/emptyEcho';
import type { ReedType } from '$lib/types/reed';

const ACTIVITY_KEY = 'followActivity';
const ACTIVITY_LIMIT = 10;

function readActivity(): ReedType[] {
  return localStorageService.get<ReedType[]>(ACTIVITY_KEY) ?? [];
}

/** Latest reed per followed author, newest first. */
export function getActivity(): ReedType[] {
  return readActivity();
}

/**
 * Record a newly-arrived reed from a followed author: drop any existing
 * entry for that author, then insert this one at the front and cap the
 * list at ACTIVITY_LIMIT. Blank echoes are skipped entirely.
 */
export function recordActivity(reed: ReedType): void {
  if (isBlankEcho(reed)) return;

  const existing = readActivity().filter((r) => r.userID !== reed.userID);
  const updated = [reed, ...existing].slice(0, ACTIVITY_LIMIT);
  localStorageService.set(ACTIVITY_KEY, updated);
}
