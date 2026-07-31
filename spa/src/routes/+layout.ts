import { dbService } from '$lib/services/db';
import { authService } from '$lib/services/auth';

/** @type {import('./$types').LayoutLoad} */
export async function load() {
  await dbService.init();

  if (!authService.isLoggedIn()) {
    return { user: null };
  }

  const user = await authService.getCurrentUser().catch(() => null);
  return { user };
}
