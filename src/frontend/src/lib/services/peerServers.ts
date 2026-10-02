import { federatedServersRepository } from '$lib/repositories/federatedServers';
import { parseCanonicalId } from '$lib/utils/identityRef';

/** The server a user lives on, or null when it is this server. */
export function foreignServerOf(userID: string): string | null {
  const parsed = parseCanonicalId(userID);
  const own = typeof localStorage !== 'undefined' ? localStorage.getItem('serverId') : null;
  if (!parsed || !own || parsed[1] === own) return null;
  return parsed[1];
}

/** What to tell the user when a step that needed another server failed:
 * nothing is queued, so they retry by reloading. */
export async function unreachableServerMessage(userID: string): Promise<string> {
  const serverID = foreignServerOf(userID) ?? '';
  let name = serverID;
  try {
    name = (await federatedServersRepository.get(serverID))?.name || serverID;
  } catch {
    // The id is still a usable name.
  }
  return `Couldn't reach ${name || 'their server'}. Reload the page and try again.`;
}
