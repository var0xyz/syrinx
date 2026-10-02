<script lang="ts">
  import { federatedServersRepository } from '$lib/repositories/federatedServers';
  import { foreignServerOf } from '$lib/services/peerServers';

  /** Renders " (Server name)" after a user from another server, nothing for ours. */
  export let userID: string;

  let name = '';

  $: serverID = foreignServerOf(userID);
  $: resolve(serverID);

  async function resolve(id: string | null) {
    name = id ?? '';
    if (!id) return;
    try {
      const server = await federatedServersRepository.get(id);
      if (server?.name && serverID === id) name = server.name;
    } catch {
      // The id is still a usable name.
    }
  }
</script>

{#if name}<span class="server">({name})</span>{/if}

<style>
  .server {
    margin-left: 0.3em;
    color: var(--muted);
    white-space: nowrap;
  }
</style>
