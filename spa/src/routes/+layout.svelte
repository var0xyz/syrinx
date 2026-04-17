<script>
  import { onMount } from 'svelte';
  import "$lib/styles.css";
  import Notifications from '$lib/components/Notifications.svelte';
  import OfflineIndicator from '$lib/components/OfflineIndicator.svelte';
  import InstallButton from '$lib/components/InstallButton.svelte';
  import { initializePWA, isOnline } from '$lib/services/pwa';
  import { authService } from '$lib/services/auth';
  import { serverConnection, ServerEvent } from '$lib/services/serverConnection';
  import { dbService } from '$lib/services/db';
  import { reedsService } from '$lib/repositories/reeds';
  import { followingRepository } from '$lib/repositories/following';

  let user = null;
  $: headerLink = user ? '/reeds' : '/';

  let wasOnline = false;
  $: if ($isOnline && !wasOnline) {
    wasOnline = true;
    authService.getCurrentUser().then(currentUser => {
      if (currentUser) {
        reedsService.processUnsignedReeds();
        followingRepository.syncPending();
      }
    });
  } else if (!$isOnline) {
    wasOnline = false;
  }

  onMount(async () => {
    initializePWA();

    // Check authentication status for header
    user = await authService.getCurrentUser();

    if (user) {
      reedsService.processUnsignedReeds();
      followingRepository.syncPending();
      serverConnection.connect()
        .then(() => {
          serverConnection.on(ServerEvent.RelayRequest, async ({ event_id, reed_id }) => {
            const reed = await dbService.get('reeds', reed_id);
            if (reed) {
              serverConnection.sendRelayResponse(event_id, reed);
            }
          });
        })
        .catch(err => console.error('ServerConnection failed:', err));
    }
  });
</script>

<header>
  <h1><a href={headerLink}>💫 Syrinx</a></h1>
</header>

<OfflineIndicator />
<slot />

<Notifications />
<InstallButton />
