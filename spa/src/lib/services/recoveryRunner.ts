/**
 * Drive unfinished recovery progress units. Own identity then peer identities;
 * reeds / follows / complete follow later.
 */

import {
  getRecoveryProgress,
  isEntityEntry,
  markEntityFinished,
  markEntitySkipped,
  markEntityStarted,
  OWN_IDENTITY_KEY,
  peerEntityKey,
} from './recoveryProgress';
import { claimOwnIdentity } from './ownIdentityClaim';
import { reportPeerIdentity } from './peerIdentityReport';

export type RecoveryRunnerResult =
  | { ok: true }
  | { ok: false; error: string };

export type RecoveryRunnerOptions = {
  /** Called after each unit of work updates the ledger (for live percent UI). */
  onProgress?: () => void;
};

function ownIdentityDone(): boolean {
  const ledger = getRecoveryProgress();
  const entry = ledger?.entities[OWN_IDENTITY_KEY];
  return isEntityEntry(entry) && entry.endTime != null;
}

/** Pending peer user IDs (not finished and not already skipped). */
function pendingPeerUserIds(): string[] {
  const ledger = getRecoveryProgress();
  if (!ledger) return [];
  const ids: string[] = [];
  for (const [key, entry] of Object.entries(ledger.entities)) {
    if (!key.startsWith('peer:')) continue;
    if (!isEntityEntry(entry)) continue;
    if (entry.endTime != null || entry.skipped) continue;
    ids.push(key.slice('peer:'.length));
  }
  return ids;
}

/**
 * Run pending own-identity claim, then each pending peer identity.
 * Idempotent when units are already finished. Peer network failures do not
 * abort the rest of the peer list; unfinished peers stay retryable.
 */
export async function runRecoveryWork(
  options?: RecoveryRunnerOptions
): Promise<RecoveryRunnerResult> {
  const notify = () => options?.onProgress?.();

  if (!ownIdentityDone()) {
    markEntityStarted(OWN_IDENTITY_KEY);
    try {
      await claimOwnIdentity();
      markEntityFinished(OWN_IDENTITY_KEY);
      notify();
    } catch (err) {
      const message =
        err instanceof Error && err.message
          ? err.message
          : 'Identity claim failed.';
      return { ok: false, error: message };
    }
  }

  const peerErrors: string[] = [];
  for (const peerUserId of pendingPeerUserIds()) {
    const key = peerEntityKey(peerUserId);
    markEntityStarted(key);
    try {
      const result = await reportPeerIdentity(peerUserId);
      if (result.status === 'skipped') {
        markEntitySkipped(key, result.reason);
      } else {
        markEntityFinished(key);
      }
      notify();
    } catch (err) {
      const message =
        err instanceof Error && err.message
          ? err.message
          : 'Peer identity report failed.';
      console.error(`recovery peer ${peerUserId}:`, message);
      peerErrors.push(peerUserId);
      // Leave unfinished so Retry can re-run this peer.
    }
  }

  if (peerErrors.length > 0) {
    const n = peerErrors.length;
    return {
      ok: false,
      error:
        n === 1
          ? 'Failed to report one peer identity. You can retry.'
          : `Failed to report ${n} peer identities. You can retry.`,
    };
  }

  return { ok: true };
}
