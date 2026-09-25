import { dbService } from '$lib/services/db';
import type * as api from '$lib/types/api';
import { allowUnsigned } from '$lib/verifiers';

/**
 * A person you verified in person. Roots are interpretation, not evidence:
 * the vouch itself is public, but whose judgement you weight is yours, so
 * this store never leaves the device.
 */
export interface TrustRoot extends api.Base {
  userID: string;
  /** The key you actually compared, for showing what was verified. */
  keyID: string;
  addedAt: string;
  /** Demoted roots stop seeding trust while their public vouch stands. */
  demoted?: boolean;
}

export const trustRootsRepository = {
  async add(userID: string, keyID: string): Promise<void> {
    const record: TrustRoot = { userID, keyID, addedAt: new Date().toISOString() };
    await dbService.put<TrustRoot>('trustRoots', record, allowUnsigned);
  },

  async get(userID: string): Promise<TrustRoot | null> {
    return dbService.get<TrustRoot>('trustRoots', userID);
  },

  async all(): Promise<TrustRoot[]> {
    return dbService.getAll<TrustRoot>('trustRoots');
  },

  /** Ids of roots that currently seed trust, demoted ones excluded. */
  async activeIDs(): Promise<Set<string>> {
    const roots = await trustRootsRepository.all();
    return new Set(roots.filter((r) => !r.demoted).map((r) => r.userID));
  },

  /** Keeps the record so the user can see what they once verified. */
  async setDemoted(userID: string, demoted: boolean): Promise<void> {
    const existing = await trustRootsRepository.get(userID);
    if (!existing) return;
    await dbService.put<TrustRoot>('trustRoots', { ...existing, demoted }, allowUnsigned);
  },

  async remove(userID: string): Promise<void> {
    await dbService.delete('trustRoots', userID);
  },
};
