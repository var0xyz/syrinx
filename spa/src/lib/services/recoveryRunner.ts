/**
 * Drive unfinished recovery progress units. Currently own_identity only;
 * peers / reeds / follows / complete follow later.
 */

import {
  getRecoveryProgress,
  isEntityEntry,
  markEntityFinished,
  markEntityStarted,
  OWN_IDENTITY_KEY,
} from './recoveryProgress';
import { claimOwnIdentity } from './ownIdentityClaim';

export type RecoveryRunnerResult =
  | { ok: true }
  | { ok: false; error: string };

function ownIdentityDone(): boolean {
  const ledger = getRecoveryProgress();
  const entry = ledger?.entities[OWN_IDENTITY_KEY];
  return isEntityEntry(entry) && entry.endTime != null;
}

/**
 * Run pending own-identity claim. Idempotent when already finished.
 * On failure leaves the ledger unfinished so Retry can re-run.
 */
export async function runRecoveryWork(): Promise<RecoveryRunnerResult> {
  if (ownIdentityDone()) {
    return { ok: true };
  }

  markEntityStarted(OWN_IDENTITY_KEY);
  try {
    await claimOwnIdentity();
    markEntityFinished(OWN_IDENTITY_KEY);
    return { ok: true };
  } catch (err) {
    const message =
      err instanceof Error && err.message
        ? err.message
        : 'Identity claim failed.';
    return { ok: false, error: message };
  }
}
