import { dbService } from '$lib/services/db';
import type * as api from '$lib/types/api';
import { verifyClosedAdminRequest } from '$lib/verifiers';

/** Closed admin requests, verified before they're stored. A closed request
 * never changes, so the stored copy is final. */
export const adminRequestsRepository = {
  async put(req: api.AdminRequest): Promise<void> {
    await dbService.put('adminRequests', req, verifyClosedAdminRequest);
  },

  async has(id: string): Promise<boolean> {
    return (await dbService.getMeta('adminRequests', id)) !== null;
  },

  /** Newest first, in the server's order: created time, then ID. */
  async getAll(): Promise<api.AdminRequest[]> {
    const stored = await dbService.getAll<api.AdminRequest>('adminRequests');
    return stored.sort((a, b) => b.createdAt - a.createdAt || (a.id < b.id ? 1 : a.id > b.id ? -1 : 0));
  },
};
