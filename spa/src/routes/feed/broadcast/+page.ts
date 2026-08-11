import { redirect } from '@sveltejs/kit';
import { isBlankEcho } from '$lib/utils/emptyEcho';

const BROADCAST_KEY = 'broadcastReeds';

function loadBroadcastReeds() {
  const defaultValue = { reeds: [], authors: {} };
  if (typeof sessionStorage === 'undefined') return defaultValue;
  if (!sessionStorage.getItem(BROADCAST_KEY)) return defaultValue;
  try {
    const parsed = JSON.parse(sessionStorage.getItem(BROADCAST_KEY));
    parsed.reeds = (parsed.reeds ?? []).filter((r) => !isBlankEcho(r));
    return parsed;
  } catch {
    sessionStorage.removeItem(BROADCAST_KEY);
  }
  return defaultValue;
}

/** @type {import('./$types').PageLoad} */
export async function load({ parent }) {
  const { user } = await parent();
  if (!user) {
    throw redirect(307, '/');
  }

  const broadcastReeds = loadBroadcastReeds();

  return {
    user,
    broadcastReeds,
  };
}
