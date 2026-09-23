import { dbService } from '$lib/services/db';
import { allowUnsigned } from '$lib/verifiers';
import { normalizePipeTag } from '$lib/utils/pipeTag';
import type { PipeType } from '$lib/types/pipe';

const PIPES_STORE = 'pipes';

export const pipesRepository = {
  async getAll(): Promise<PipeType[]> {
    const all = await dbService.getAll<PipeType>(PIPES_STORE);
    return all.sort((a, b) => a.tagName.localeCompare(b.tagName, undefined, { sensitivity: 'base' }));
  },

  async isPinned(tag: string): Promise<boolean> {
    const normalized = normalizePipeTag(tag);
    if (!normalized) return false;
    return (await dbService.get<PipeType>(PIPES_STORE, normalized)) !== null;
  },

  async pin(tag: string): Promise<void> {
    const normalized = normalizePipeTag(tag);
    if (!normalized) return;
    const displayName = tag.trim().replace(/^#/, '') || normalized;
    const pipe: PipeType = { tagName: normalized, displayName, createdAt: Date.now() };
    await dbService.put<PipeType>(PIPES_STORE, pipe, allowUnsigned);
  },

  async unpin(tag: string): Promise<void> {
    const normalized = normalizePipeTag(tag);
    if (!normalized) return;
    await dbService.delete(PIPES_STORE, normalized);
  },
};
