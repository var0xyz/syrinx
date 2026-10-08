import { redirect } from '@sveltejs/kit';
import { userListsRepository } from '$lib/repositories/userLists';

/** @type {import('./$types').LayoutLoad} */
export async function load({ parent }) {
  const { user } = await parent();
  if (!user) {
    throw redirect(307, '/');
  }

  const userLists = await userListsRepository.getAll();

  return { user, userLists };
}
