import { dbService } from '$lib/services/db';
import type * as api from '$lib/types/api';
import { allowUnsigned, verifyInvite } from '$lib/verifiers';

export const invitesRepository = {
  async put(invite: api.Invite): Promise<void> {
    await dbService.put('invites', invite, verifyInvite);
  },

  /** Merge unsigned status fields without re-checking crypto. */
  async putStatus(invite: api.Invite): Promise<void> {
    await dbService.put('invites', invite, allowUnsigned);
  },

  async get(id: string): Promise<api.Invite | null> {
    return dbService.get<api.Invite>('invites', id);
  },

  async getAll(): Promise<api.Invite[]> {
    const all = await dbService.getAll<api.Invite>('invites');
    return all.sort(
      (a, b) => new Date(b.createdAt).getTime() - new Date(a.createdAt).getTime()
    );
  },

  async delete(id: string): Promise<void> {
    await dbService.delete('invites', id);
  },
};
