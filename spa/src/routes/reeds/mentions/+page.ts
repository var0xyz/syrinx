import { redirect } from '@sveltejs/kit';
import { getMentionItems } from '$lib/repositories/mentions';

/** @type {import('./$types').PageLoad} */
export async function load({ parent }) {
  const { user } = await parent();
  if (!user) {
    throw redirect(307, '/');
  }

  const items = await getMentionItems();

  return { user, items };
}
