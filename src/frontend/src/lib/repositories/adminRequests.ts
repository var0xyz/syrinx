import { dbService } from '$lib/services/db';
import type * as api from '$lib/types/api';
import { verifyAdminRequest } from '$lib/verifiers';

/** Admin requests in any state, verified before they're stored. A closed
 * request never changes, so a stored closed copy is final. */
export const adminRequestsRepository = {
  /** Stores req unless a closed copy is held. True when it stored it. */
  async put(req: api.AdminRequest): Promise<boolean> {
    const held = await dbService.get<api.AdminRequest>('adminRequests', req.id);
    if (held && (held.state !== 'pending' || req.state === 'pending')) return false;
    await dbService.put('adminRequests', req, verifyAdminRequest);
    return true;
  },

  async get(id: string): Promise<api.AdminRequest | null> {
    return dbService.get<api.AdminRequest>('adminRequests', id);
  },

  /** Newest first, in the server's order: created time, then ID. */
  async getAll(): Promise<api.AdminRequest[]> {
    const stored = await dbService.getAll<api.AdminRequest>('adminRequests');
    return stored.sort((a, b) => b.createdAt - a.createdAt || (a.id < b.id ? 1 : a.id > b.id ? -1 : 0));
  },

  async clear(): Promise<void> {
    await dbService.clear('adminRequests');
  },
};
