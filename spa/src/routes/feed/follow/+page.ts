import { redirect } from '@sveltejs/kit';
import { getFollowReeds, initFollowIds } from '$lib/repositories/reeds';

/** @type {import('./$types').PageLoad} */
export async function load({ parent }) {
  const { user } = await parent();
  if (!user) {
    throw redirect(307, '/');
  }

  await initFollowIds();
  const followReeds = await getFollowReeds();

  return {
    user,
    followReeds,
  };
}
