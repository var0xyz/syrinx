import { redirect, error } from '@sveltejs/kit';
import { getListReeds } from '$lib/repositories/reeds';

/** @type {import('./$types').PageLoad} */
export async function load({ parent, params }) {
  const { user } = await parent();
  if (!user) {
    throw redirect(307, '/');
  }

  const { reeds, authors, list } = await getListReeds(params.listId);
  if (!list) {
    throw error(404, 'List not found');
  }

  return { user, list, reeds, authors };
}
