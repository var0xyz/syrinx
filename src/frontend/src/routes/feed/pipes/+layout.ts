import { redirect } from '@sveltejs/kit';
import { pipesRepository } from '$lib/repositories/pipes';

/** @type {import('./$types').LayoutLoad} */
export async function load({ parent }) {
  const { user } = await parent();
  if (!user) {
    throw redirect(307, '/');
  }

  const pipes = await pipesRepository.getAll();

  return { user, pipes };
}
