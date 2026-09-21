import type * as api from '$lib/types/api';

/** A local, unsigned pinned hashtag pipe. Never sent to the server. */
export interface PipeType extends api.Base {
  tagName: string;
  createdAt: number;
}
