import type * as api from '$lib/types/api';

/** A local, unsigned grouping of followed users. Never sent to the server. */
export interface ListType extends api.Base {
  id: string;
  name: string;
  description: string;
  memberIds: string[];
  createdAt: number;
}
