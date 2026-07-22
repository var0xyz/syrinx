/**
 * Drive unfinished recovery progress units through complete.
 * Order: own identity → peers → reeds → follows → complete.
 */

import {
  COMPLETE_KEY,
  FOLLOWS_KEY,
  getRecoveryProgress,
  isEntityEntry,
  isFollowsEntry,
  markEntityFinished,
  markEntitySkipped,
  markEntityStarted,
  markFollowPageFinished,
  markFollowPageStarted,
  OWN_IDENTITY_KEY,
  peerEntityKey,
  reedEntityKey,
  type RecoveryFollowPage,
} from './recoveryProgress';
import { claimOwnIdentity } from './ownIdentityClaim';
import { reportPeerIdentity } from './peerIdentityReport';
import {
  completeRecoveryImport,
  reportRecoveryFollowing,
  reportRecoveryReed,
} from './recoveryHoldings';
import { completeRecoveryRun } from './recoveryRun';

export type RecoveryRunnerResult =
  | { ok: true }
  | { ok: false; error: string };

export type RecoveryRunnerOptions = {
  /** Called after each unit of work updates the ledger (for live percent UI). */
  onProgress?: () => void;
};

function errorMessage(err: unknown, fallback: string): string {
  return err instanceof Error && err.message ? err.message : fallback;
}

function ownIdentityDone(): boolean {
  const ledger = getRecoveryProgress();
  const entry = ledger?.entities[OWN_IDENTITY_KEY];
  return isEntityEntry(entry) && entry.endTime != null;
}

function completeDone(): boolean {
  const ledger = getRecoveryProgress();
  const entry = ledger?.entities[COMPLETE_KEY];
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

/** Pending reed IDs from the progress ledger. */
function pendingReedIds(): string[] {
  const ledger = getRecoveryProgress();
  if (!ledger) return [];
  const ids: string[] = [];
  for (const [key, entry] of Object.entries(ledger.entities)) {
    if (!key.startsWith('reed:')) continue;
    if (!isEntityEntry(entry)) continue;
    if (entry.endTime != null || entry.skipped) continue;
    ids.push(key.slice('reed:'.length));
  }
  return ids;
}

/** Follow pages that still need a POST. */
function pendingFollowPages(): RecoveryFollowPage[] {
  const ledger = getRecoveryProgress();
  const follows = ledger?.entities[FOLLOWS_KEY];
  if (!isFollowsEntry(follows)) return [];
  return follows.pages.filter((page) => page.endTime == null);
}

/**
 * Run all pending recovery units through complete.
 * Idempotent when units are already finished. Peer network failures do not
 * abort the rest of the peer list; unfinished peers stay retryable.
 * Reed / follow failures stop the run so Retry resumes from the ledger.
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
      return { ok: false, error: errorMessage(err, 'Identity claim failed.') };
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
      const message = errorMessage(err, 'Peer identity report failed.');
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

  for (const reedId of pendingReedIds()) {
    const key = reedEntityKey(reedId);
    markEntityStarted(key);
    try {
      await reportRecoveryReed(reedId);
      markEntityFinished(key);
      notify();
    } catch (err) {
      return {
        ok: false,
        error: errorMessage(err, 'Reed report failed.'),
      };
    }
  }

  for (const page of pendingFollowPages()) {
    markFollowPageStarted(page.index);
    try {
      await reportRecoveryFollowing(page.userIds);
      markFollowPageFinished(page.index);
      notify();
    } catch (err) {
      return {
        ok: false,
        error: errorMessage(err, 'Follow report failed.'),
      };
    }
  }

  if (!completeDone()) {
    markEntityStarted(COMPLETE_KEY);
    try {
      await completeRecoveryImport();
      markEntityFinished(COMPLETE_KEY);
      completeRecoveryRun();
      notify();
    } catch (err) {
      return {
        ok: false,
        error: errorMessage(err, 'Recovery complete failed.'),
      };
    }
  }

  return { ok: true };
}
