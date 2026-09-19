import { redirect } from '@sveltejs/kit';
import { listsRepository } from '$lib/repositories/lists';

/** @type {import('./$types').LayoutLoad} */
export async function load({ parent }) {
  const { user } = await parent();
  if (!user) {
    throw redirect(307, '/');
  }

  const lists = await listsRepository.getAll();

  return { user, lists };
}
