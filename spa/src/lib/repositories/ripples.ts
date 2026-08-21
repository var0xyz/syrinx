import { dbService, type DbService } from '../services/db';
import type * as api from '$lib/types/api';
import { apiService, canonicalKeyId } from '$lib/services/api';
import { verifyRipple } from '$lib/verifiers';
import { publicKeyRepository } from './publicKey';
import { userRepository } from './user';

/**
 * Local cache of verified ripple responses, keyed by `hash` (content-
 * addressed, globally unique — see specs/ripples/00_design.md). Mirrors
 * the reeds repository's shape: verify-then-cache, cache-hit shortcut
 * skips re-verification (important for tombstoned responses, whose
 * stored signatures no longer match their current "[DELETED]" content).
 */
export class RipplesRepository {
  private db: DbService;

  constructor(db: DbService = dbService) {
    this.db = db;
  }

  async getRipple(hash: string): Promise<api.Ripple | null> {
    return await this.db.get<api.Ripple>('ripples', hash);
  }

  async hasRipple(hash: string): Promise<boolean> {
    return !!(await this.getRipple(hash));
  }

  /**
   * Verify (unless already cached) and store a ripple. Also ensures the
   * author's public key and profile are cached, same as storeReed does
   * for a reed's author. reedAuthorID/reedID identify the parent reed
   * (needed to rebuild the signed payload — not part of the wire object).
   * Returns false (and does not store) if verification fails; callers
   * must treat that as "discard, do not render" per 00's Client-side
   * verification section, not as an error to surface to the user.
   */
  async storeRipple(ripple: api.Ripple, reedAuthorID: string, reedID: string): Promise<boolean> {
    if (await this.hasRipple(ripple.hash)) {
      return true;
    }

    if (ripple.userSignature?.fingerprint && ripple.userID) {
      const fp = ripple.userSignature.fingerprint;
      if (!(await publicKeyRepository.hasPublicKey(fp))) {
        try {
          const key = await apiService.getPublicKey(canonicalKeyId(ripple.userID, fp));
          await publicKeyRepository.put(key);
        } catch (error) {
          console.error('Failed to cache ripple author public key:', error);
        }
      }
    }

    try {
      await this.db.put('ripples', ripple, (r) => verifyRipple(r, reedAuthorID, reedID));
    } catch (error) {
      console.error('Ripple failed verification, discarding:', ripple.hash, error);
      return false;
    }

    if (ripple.userID) {
      try {
        await userRepository.getByUserId(ripple.userID);
      } catch (error) {
        console.error('Failed to cache ripple author profile:', error);
      }
    }

    return true;
  }
}

export const ripplesRepository = new RipplesRepository();
