import { redirect, error } from '@sveltejs/kit';
import { reedsService } from '$lib/repositories/reeds';
import { normalizePipeTag } from '$lib/utils/pipeTag';

/** @type {import('./$types').PageLoad} */
export async function load({ parent, params }) {
  const { user } = await parent();
  if (!user) {
    throw redirect(307, '/');
  }

  const tag = normalizePipeTag(params.tag ?? '');
  if (!tag) {
    throw error(404, 'Pipe not found');
  }

  const { reeds, authors } = await reedsService.getReedsByTag(tag);

  return {
    user,
    tag,
    reeds,
    authors,
  };
}
