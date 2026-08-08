<script>
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { userRepository } from '$lib/repositories/user';

  /** @type {string} */
  export let userID;
  /** @type {string} */
  export let serverID;
  /** Inert rendering (composer preview) — no click navigation. */
  export let preview = false;
  /**
   * Optional immediate display value from an unsigned source (e.g. the
   * composer's search-result pick). Shown until the real verified fetch
   * below resolves and overwrites it — never a substitute for that fetch.
   */
  export let hintUsername = undefined;

  let username = hintUsername || null;
  let localServerID = '';
  // True only once locality + (for local mentions) the verified fetch have
  // actually been checked — distinct from "unresolved" so the initial
  // render (before onMount runs, localServerID is still '') never flashes
  // the literal-token fallback for what will turn out to be a normal local
  // mention.
  let settled = !!hintUsername;
  let resolveFailed = false;

  $: isLocal = !!localServerID && serverID === localServerID;
  // Render the literal wire token only once we know for certain the
  // mention can't resolve: foreign-server mentions (nothing local to look
  // up) or a local lookup that failed/came back empty.
  $: unresolved = settled && !username && (!isLocal || resolveFailed);

  onMount(async () => {
    // Compute locality from a local const, not the `isLocal` reactive
    // statement — that only recomputes on Svelte's next flush, not
    // synchronously after this assignment, so checking it here would race
    // against a stale (pre-assignment) value.
    const resolvedServerID = localStorage.getItem('serverId') || '';
    localServerID = resolvedServerID;
    if (!resolvedServerID || serverID !== resolvedServerID) {
      settled = true;
      return;
    }
    // Always run the real verified fetch for local mentions, even with a
    // hint already in hand — the hint is an unsigned display shortcut, not
    // a substitute for verification, and this is also what keeps the
    // durable cache warm for every other (non-composer) render path.
    try {
      const user = await userRepository.getByUserId(userID);
      if (user?.username) {
        username = user.username;
      } else if (!hintUsername) {
        resolveFailed = true;
      }
    } catch {
      if (!hintUsername) resolveFailed = true;
    } finally {
      settled = true;
    }
  });

  function activate() {
    if (!isLocal) return;
    goto(`/profile/${userID}`);
  }
</script>

{#if unresolved}
  {`~${userID}@${serverID}`}
{:else if preview || !isLocal}
  <span class="inline-link mention-link"
    >{username}</span
  >
{:else}
  <a
    href="/profile/{userID}"
    class="inline-link mention-link"
    on:click|preventDefault={activate}
    >{username}</a
  >
{/if}
