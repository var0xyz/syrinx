<script>
  import { onMount } from 'svelte';
  import "$lib/styles.css";
  import Notifications from '$lib/components/Notifications.svelte';
  import OfflineIndicator from '$lib/components/OfflineIndicator.svelte';
  import InstallButton from '$lib/components/InstallButton.svelte';
  import { initializePWA, isOnline } from '$lib/services/pwa';
  import { websocketService } from '$lib/services/websocket';
  import { authService } from '$lib/services/auth';
  import { requestSigner } from '$lib/services/request-signer';
  import { reedsService } from '$lib/repositories/reeds';

  let user = null;
  $: headerLink = user ? '/reeds' : '/';

  async function initializeWebSocket() {
    try {
      const currentUser = await authService.getCurrentUser();
      user = currentUser;

      if (currentUser) {
        console.log('WebSocket init - User found:', currentUser.id);
        console.log('WebSocket init - Request signer initialized:', requestSigner.isInitialized());

        // Let websocketService.connect() handle request signer initialization if needed
        // It will check and initialize the request signer automatically
        await websocketService.connect();

        // Only subscribe if connection was successful
        if (websocketService.isConnected()) {
          // Subscribe to both user and broadcast notifications
          websocketService.subscribeToUser();
          websocketService.subscribeToBroadcast();

          // Set up event handlers
          websocketService.on('reed_notification', (data) => {
            console.log('New reed notification:', data);
            // TODO: Show notification or update UI
          });

          websocketService.on('user_update', (data) => {
            console.log('User update notification:', data);
            // TODO: Update user data in UI
          });
        } else {
          console.log('WebSocket connection not established, skipping subscriptions');
        }
      } else {
        console.log('No user found, skipping WebSocket connection');
      }
    } catch (error) {
      console.error('Failed to initialize WebSocket connection:', error);
    }
  }

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

    if (user) reedsService.processUnsignedReeds();

    // Wait a bit for Auth component to initialize first
    setTimeout(async () => {
      await initializeWebSocket();
    }, 100);

  });
</script>

<header>
  <h1><a href={headerLink}>💫 Syrinx</a></h1>
</header>

<OfflineIndicator />
<slot />

<Notifications />
<InstallButton />
