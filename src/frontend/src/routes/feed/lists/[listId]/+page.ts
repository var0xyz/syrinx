import { redirect, error } from '@sveltejs/kit';
import { getUserListReeds } from '$lib/repositories/reeds';

/** @type {import('./$types').PageLoad} */
export async function load({ parent, params }) {
  const { user } = await parent();
  if (!user) {
    throw redirect(307, '/');
  }

  const { reeds, authors, userList } = await getUserListReeds(params.listId);
  if (!userList) {
    throw error(404, 'List not found');
  }

  return { user, userList, reeds, authors };
}
