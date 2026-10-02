import { dbService } from '$lib/services/db';
import type * as api from '$lib/types/api';
import type { FederatedServer } from '$lib/types/server';
import { allowUnsigned } from '$lib/verifiers';

type FederatedServerRecord = FederatedServer & api.Base;

/**
 * The peers this server is federated with, as last reported by
 * /server/info. Read locally so nothing that needs them waits on the network.
 */
export const federatedServersRepository = {
  async all(): Promise<FederatedServer[]> {
    const servers = await dbService.getAll<FederatedServerRecord>('federatedServers');
    return servers.sort((a, b) => a.name.localeCompare(b.name));
  },

  async get(id: string): Promise<FederatedServer | null> {
    return dbService.get<FederatedServerRecord>('federatedServers', id);
  },

  /** Replaces the stored list, so a peer the server stopped listing goes too. */
  async replaceAll(servers: FederatedServer[]): Promise<void> {
    await dbService.clear('federatedServers');
    for (const server of servers) {
      await dbService.put<FederatedServerRecord>('federatedServers', server, allowUnsigned);
    }
  },
};
