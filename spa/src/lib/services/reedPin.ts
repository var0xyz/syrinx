import { apiService } from '$lib/services/api';
import { userInfoRepository } from '$lib/repositories/userInfo';
import { notificationStore } from '$lib/stores/notifications';

/** Unsigned, same-server-only — no signing/pending-queue like likes.
 * Rolls back and notifies on failure; returns the new array either way. */
async function togglePin(
  ownerId: string,
  reedId: string,
  current: string[],
  pin: boolean
): Promise<string[]> {
  const after = pin
    ? [reedId, ...current.filter((id) => id !== reedId)]
    : current.filter((id) => id !== reedId);

  try {
    if (pin) {
      await apiService.pinReed(reedId);
    } else {
      await apiService.unpinReed(reedId);
    }
    const cached = await userInfoRepository.get(ownerId);
    if (cached) {
      await userInfoRepository.put({ ...cached, pinnedReedIDs: after });
    }
    return after;
  } catch (error) {
    console.error(`Failed to ${pin ? 'pin' : 'unpin'} reed:`, error);
    notificationStore.error(pin ? 'Failed to pin reed.' : 'Failed to unpin reed.');
    return current;
  }
}

export function pinReed(ownerId: string, reedId: string, current: string[]): Promise<string[]> {
  return togglePin(ownerId, reedId, current, true);
}

export function unpinReed(ownerId: string, reedId: string, current: string[]): Promise<string[]> {
  return togglePin(ownerId, reedId, current, false);
}
