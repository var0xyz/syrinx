import { dbService } from '$lib/services/db';
import type * as api from '$lib/types/api';
import { allowUnsigned, verifyOwnBlock } from '$lib/verifiers';

/** A block signed on this device and not yet countersigned. */
export interface PendingBlockRecord {
  blockedUserID: string;
  keyId: string;
  signature: string;
}

/** A lift not yet sent. Unsigned, like unliking. */
export interface PendingUnblockRecord {
  blockedUserID: string;
}

/** The viewer's own verified blocks, keyed by the blocked user. */
export const blocksRepository = {
  async put(cert: api.BlockCert): Promise<void> {
    await dbService.put('blocks', cert, verifyOwnBlock);
  },

  async get(blockedUserID: string): Promise<api.BlockCert | null> {
    return dbService.get<api.BlockCert>('blocks', blockedUserID);
  },

  async getAll(): Promise<api.BlockCert[]> {
    return dbService.getAll<api.BlockCert>('blocks');
  },

  async delete(blockedUserID: string): Promise<void> {
    await dbService.delete('blocks', blockedUserID);
  },
};

export const pendingBlocksRepository = {
  async put(record: PendingBlockRecord): Promise<void> {
    await dbService.put('pendingBlocks', record, allowUnsigned);
  },

  async get(blockedUserID: string): Promise<PendingBlockRecord | null> {
    return dbService.get<PendingBlockRecord>('pendingBlocks', blockedUserID);
  },

  async getAll(): Promise<PendingBlockRecord[]> {
    return dbService.getAll<PendingBlockRecord>('pendingBlocks');
  },

  async delete(blockedUserID: string): Promise<void> {
    await dbService.delete('pendingBlocks', blockedUserID);
  },
};

export const pendingUnblocksRepository = {
  async put(record: PendingUnblockRecord): Promise<void> {
    await dbService.put('pendingUnblocks', record, allowUnsigned);
  },

  async get(blockedUserID: string): Promise<PendingUnblockRecord | null> {
    return dbService.get<PendingUnblockRecord>('pendingUnblocks', blockedUserID);
  },

  async getAll(): Promise<PendingUnblockRecord[]> {
    return dbService.getAll<PendingUnblockRecord>('pendingUnblocks');
  },

  async delete(blockedUserID: string): Promise<void> {
    await dbService.delete('pendingUnblocks', blockedUserID);
  },
};
