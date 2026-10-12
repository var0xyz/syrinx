import { writable } from 'svelte/store';
import type * as api from '$lib/types/api';
import { verifyAdminAuthorization } from '$lib/verifiers';

/** The request that suspended the viewer, once a refusal carried it and it
 * verified. */
export const suspension = writable<api.AdminRequest | null>(null);

/** A refusal's suspension certificate. A suspended client can't fetch keys,
 * so admin signatures are checked only against keys already held. */
export async function receiveSuspension(cert: api.AdminAuthorization): Promise<void> {
  const viewerID = localStorage.getItem('userId');
  if (!viewerID || !cert.request?.targets.includes(viewerID)) return;
  if (await verifyAdminAuthorization(cert, 'suspend', { cachedKeysOnly: true })) {
    suspension.set(cert.request);
  }
}
