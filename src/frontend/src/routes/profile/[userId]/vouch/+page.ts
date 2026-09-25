import { redirect } from '@sveltejs/kit';

/** @type {import('./$types').PageLoad} */
export async function load({ params, parent }) {
  const { user: currentUser } = await parent();
  if (!currentUser) {
    throw redirect(307, '/');
  }
  // The key id arrives in the fragment, which never reaches the server and
  // is not visible to the loader — the page reads it on mount.
  return {
    currentUser,
    subjectUserID: params.userId,
    isSelf: currentUser.id === params.userId,
  };
}
