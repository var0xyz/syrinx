/**
 * Bootstrap tip override for account-recovery publish (Approach B).
 */

import { reedsService } from '$lib/repositories/reeds';

const PUBLISH_TIP_KEY = 'publishTipReedID';

export function hasPublishTipOverride(): boolean {
  return typeof localStorage !== 'undefined' && localStorage.getItem(PUBLISH_TIP_KEY) !== null;
}

export function clearPublishTipOverride(): void {
  if (typeof localStorage === 'undefined') return;
  localStorage.removeItem(PUBLISH_TIP_KEY);
}

async function newestLocalOwnReedId(): Promise<string | undefined> {
  const userId = localStorage.getItem('userId');
  if (!userId) return undefined;
  const reeds = await reedsService.getReedsByAuthor(userId);
  return reeds[0]?.id;
}

/** Tip id to send as previousID on the next create, if any. */
export async function previousIDForPublish(): Promise<string | undefined> {
  if (typeof localStorage === 'undefined') return undefined;

  const override = localStorage.getItem(PUBLISH_TIP_KEY);
  if (override !== null) return override;

  return newestLocalOwnReedId();
}
