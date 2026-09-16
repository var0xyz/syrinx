import { redirect } from '@sveltejs/kit';
import { getFollowReeds } from '$lib/repositories/reeds';

/** @type {import('./$types').PageLoad} */
export async function load({ parent }) {
  const { user } = await parent();
  if (!user) {
    throw redirect(307, '/');
  }

  const followReeds = await getFollowReeds();

  return {
    user,
    followReeds,
  };
}
