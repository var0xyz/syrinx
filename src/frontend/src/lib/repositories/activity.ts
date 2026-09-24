/**
 * Activity list: latest reed per followed author, for the activity sidebar.
 * localStorage-backed (not IndexedDB) — this is a lightweight, ephemeral
 * view cache, not data that needs to persist or be backed up.
 */
import { localStorageService } from '$lib/services/localstorage';
import { followingRepository } from '$lib/repositories/following';
import { isBlankEcho } from '$lib/utils/emptyEcho';
import type { ReedType } from '$lib/types/reed';

const ACTIVITY_KEY = 'followActivity';
const ACTIVITY_LIMIT = 10;

function readActivity(): ReedType[] {
  return localStorageService.get<ReedType[]>(ACTIVITY_KEY) ?? [];
}

/**
 * Latest reed per followed author, newest first. Re-checks follows on read
 * so unfollows and entries stored before the write-side check drop out.
 */
export async function getActivity(): Promise<ReedType[]> {
  const stored = readActivity();
  const kept: ReedType[] = [];
  for (const reed of stored) {
    try {
      if (await followingRepository.isFollowing(reed.userID)) kept.push(reed);
    } catch {
      kept.push(reed);
    }
  }
  if (kept.length !== stored.length) localStorageService.set(ACTIVITY_KEY, kept);
  return kept;
}

/**
 * Record a newly-arrived reed from a followed author: drop any existing
 * entry for that author, then insert this one at the front and cap the
 * list at ACTIVITY_LIMIT. Blank echoes are skipped entirely.
 *
 * Replies, mentions, pipe and relayed reeds all arrive from authors we
 * may not follow, so the follow check belongs here rather than at each
 * delivery site.
 */
export async function recordActivity(reed: ReedType): Promise<void> {
  if (isBlankEcho(reed)) return;
  if (!reed.userID) return;

  // Never let a cache-only lookup failure bubble into a delivery handler,
  // where it would be misread as a bad signature and reject the reed.
  try {
    if (!(await followingRepository.isFollowing(reed.userID))) return;
  } catch (error) {
    console.warn('Activity: follow check failed, skipping:', reed.id, error);
    return;
  }

  const existing = readActivity().filter((r) => r.userID !== reed.userID);
  const updated = [reed, ...existing].slice(0, ACTIVITY_LIMIT);
  localStorageService.set(ACTIVITY_KEY, updated);
}
