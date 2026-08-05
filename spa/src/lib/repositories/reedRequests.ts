import { dbService } from '$lib/services/db';
import { allowUnsigned } from '$lib/verifiers';

declare const md5: (str: string) => string;

export interface ReedRequestRecord {
  requestId: string;
  serverId: string;
  authorId: string;
  reedId: string;
  requestedAt: number;
}

export function computeReedRequestId(
  serverId: string,
  authorId: string,
  reedId: string
): string {
  return md5(`REQUEST_REED:${serverId}/${authorId}/${reedId}`);
}

export const reedRequestsRepository = {
  async enqueue(
    record: Omit<ReedRequestRecord, 'requestedAt'> & { requestedAt?: number }
  ): Promise<void> {
    const existing = await dbService.get<ReedRequestRecord>('reedRequests', record.requestId);
    if (existing) return;
    await dbService.put(
      'reedRequests',
      {
        requestId: record.requestId,
        serverId: record.serverId,
        authorId: record.authorId,
        reedId: record.reedId,
        requestedAt: record.requestedAt ?? Date.now(),
      },
      allowUnsigned
    );
  },

  async get(requestId: string): Promise<ReedRequestRecord | null> {
    return dbService.get<ReedRequestRecord>('reedRequests', requestId);
  },

  async delete(requestId: string): Promise<void> {
    await dbService.delete('reedRequests', requestId);
  },

  async getAllPending(): Promise<ReedRequestRecord[]> {
    const all = await dbService.getAll<ReedRequestRecord>('reedRequests');
    all.sort((a, b) => a.requestedAt - b.requestedAt);
    return all;
  },

  async seedReedIDs(
    serverId: string,
    authorId: string,
    reedIDs: string[],
    skipReedIds: ReadonlySet<string>
  ): Promise<void> {
    let requestedAt = Date.now();
    for (const reedId of reedIDs) {
      if (skipReedIds.has(reedId)) continue;
      await this.enqueue({
        requestId: computeReedRequestId(serverId, authorId, reedId),
        serverId,
        authorId,
        reedId,
        requestedAt: requestedAt++,
      });
    }
  },
};
