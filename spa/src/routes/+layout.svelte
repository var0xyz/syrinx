<script>
  import { onMount } from 'svelte';
  import "$lib/styles.css";
  import Notifications from '$lib/components/Notifications.svelte';
  import OfflineIndicator from '$lib/components/OfflineIndicator.svelte';
  import InstallButton from '$lib/components/InstallButton.svelte';
  import { initializePWA } from '$lib/services/pwa';

  onMount(() => {
    initializePWA();
  });
</script>

<OfflineIndicator />
<slot />

<Notifications />
<InstallButton />