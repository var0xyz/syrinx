import { dbService } from '$lib/services/db';
import { authService } from '$lib/services/auth';
import { migratePrivateKeyFingerprintsV10 } from '$lib/repositories/privateKey';
import { migratePendingRevocationFingerprintsV10 } from '$lib/repositories/pendingRevocation';

/** @type {import('./$types').LayoutLoad} */
export async function load() {
  await dbService.init();

  // v10 key-fingerprint canonicalization (see db.ts) — idempotent, safe to
  // run on every load; each becomes a no-op once its rows are migrated.
  const userId = authService.isLoggedIn() ? localStorage.getItem('userId') : null;
  if (userId) {
    await migratePrivateKeyFingerprintsV10(userId).catch((err) =>
      console.error('privateKeys v10 migration failed:', err)
    );
  }
  await migratePendingRevocationFingerprintsV10().catch((err) =>
    console.error('pendingRevocation v10 migration failed:', err)
  );

  if (!authService.isLoggedIn()) {
    return { user: null };
  }

  const user = await authService.getCurrentUser().catch(() => null);
  return { user };
}
