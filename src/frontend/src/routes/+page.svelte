<script>
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { resolveRestoreTarget } from '$lib/services/restoreFlow';

  /** The splash stays up at least this long, so it never just flickers. */
  const MIN_SPLASH_MS = 1000;
  /** Then the logo fades out over this long before leaving. */
  const FADE_MS = 250;

  let leaving = false;

  // Only a splash: nothing here depends on the session, so no page that
  // does flashes before the user lands where they belong.
  onMount(async () => {
    const minimum = new Promise((resolve) => setTimeout(resolve, MIN_SPLASH_MS));
    const [target] = await Promise.all([resolveRestoreTarget(), minimum]);
    leaving = true;
    await new Promise((resolve) => setTimeout(resolve, FADE_MS));
    await goto(target ?? '/welcome', { replaceState: true });
  });
</script>

<main class="splash" class:leaving aria-label="Syrinx" style="--fade-ms: {FADE_MS}ms">
  <span class="logo" aria-hidden="true">💫</span>
  <span class="name">Syrinx</span>
</main>

<style>
  .splash {
    flex: 1;
    min-height: 100vh;
    min-height: 100dvh;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 0.75rem;
    color: var(--fg);
    background: var(--bg);
  }

  .logo,
  .name {
    transition: opacity var(--fade-ms) ease-out;
  }

  .leaving .logo,
  .leaving .name {
    opacity: 0;
  }

  .logo {
    font-size: 4.5rem;
    line-height: 1;
  }

  .name {
    font-size: 2.5rem;
    font-weight: 700;
    letter-spacing: -0.01em;
  }
</style>
