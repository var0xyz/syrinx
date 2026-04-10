<script>
  import { onMount } from 'svelte';
  import "$lib/styles.css";
  import Notifications from '$lib/components/Notifications.svelte';
  import OfflineIndicator from '$lib/components/OfflineIndicator.svelte';
  import InstallButton from '$lib/components/InstallButton.svelte';
  import { initializePWA, isOnline } from '$lib/services/pwa';
  import { authService } from '$lib/services/auth';
  import { serverConnection } from '$lib/services/serverConnection';
  import { reedsService } from '$lib/repositories/reeds';

  let user = null;
  $: headerLink = user ? '/reeds' : '/';

  let wasOnline = false;
  $: if ($isOnline && !wasOnline) {
    wasOnline = true;
    authService.getCurrentUser().then(currentUser => {
      if (currentUser) reedsService.processUnsignedReeds();
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
      serverConnection.connect().catch(err => console.error('ServerConnection failed:', err));
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
