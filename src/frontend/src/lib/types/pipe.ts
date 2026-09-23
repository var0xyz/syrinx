import type * as api from '$lib/types/api';

/** A local, unsigned pinned hashtag pipe. Never sent to the server. */
export interface PipeType extends api.Base {
  /** Normalized (lowercase) — primary key, used for matching. */
  tagName: string;
  /** As the author originally wrote it, for display. */
  displayName: string;
  createdAt: number;
}
