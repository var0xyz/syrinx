import { dbService } from '$lib/services/db';
import type * as api from '$lib/types/api';
import type { FederatedServer } from '$lib/types/server';
import { verifyPeerRecord } from '$lib/verifiers';

type FederatedServerRecord = FederatedServer & api.Base;

/**
 * The peers this server is federated with, as last reported by
 * /server/info, each with the record the server countersigned for it. Read
 * locally so nothing that needs them waits on the network, and reported
 * back if the server is ever rebuilt.
 */
export const federatedServersRepository = {
  async all(): Promise<FederatedServer[]> {
    const servers = await dbService.getAll<FederatedServerRecord>('federatedServers');
    return servers.sort((a, b) => a.name.localeCompare(b.name));
  },

  async get(id: string): Promise<FederatedServer | null> {
    return dbService.get<FederatedServerRecord>('federatedServers', id);
  },

  /** Keeps each verified record unless the device holds a newer one. Never
   * deletes: a rebuilt server lists nothing until devices report back. */
  async mergeNewer(servers: FederatedServer[]): Promise<void> {
    for (const server of servers) {
      const held = await dbService.get<FederatedServerRecord>('federatedServers', server.id);
      if ((held?.serverSignature?.signedAt ?? 0) > (server.serverSignature?.signedAt ?? 0)) continue;
      try {
        await dbService.put<FederatedServerRecord>('federatedServers', server, verifyPeerRecord);
      } catch (error) {
        console.error('federatedServers: dropping a record that does not verify', server.id, error);
      }
    }
  },
};
