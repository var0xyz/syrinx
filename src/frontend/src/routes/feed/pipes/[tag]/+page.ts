import { redirect, error } from '@sveltejs/kit';
import { normalizePipeTag, pipeTagLabel } from '$lib/utils/pipeTag';

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

  return {
    user,
    tag,
    displayName: pipeTagLabel(params.tag ?? ''),
  };
}
