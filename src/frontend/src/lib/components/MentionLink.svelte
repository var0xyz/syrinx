<script>
  import { onMount } from 'svelte';
  import { userRepository } from '$lib/repositories/user';
  import Username from '$lib/components/Username.svelte';

  /** `userID@serverID` id — same convention as Username.svelte, which this
   * renders through. Callers already have this form (reedMarkdown.ts's
   * parser for signed mention content, MentionPicker.svelte's confirm() for
   * a fresh pick), so it's taken as a single value, never split props to
   * rejoin here. */
  /** @type {string} */
  export let userID;
  /** Inert rendering (composer preview) — no click navigation. */
  export let preview = false;
  /**
   * Optional immediate display value from an unsigned source (e.g. the
   * composer's search-result pick). Shown until the real verified fetch
   * below resolves and overwrites it — never a substitute for that fetch.
   */
  export let hintUsername = undefined;

  let username = hintUsername || null;
  // True once the verified fetch has actually been checked — distinct
  // from "unresolved" so the initial render (before onMount runs) never
  // flashes the literal-token fallback for what will turn out to resolve.
  let settled = !!hintUsername;
  let resolveFailed = false;

  // Render the literal wire token only once we know for certain the
  // mention can't resolve — the fetch failed or came back with no
  // username. Local and foreign mentions resolve the same way: a foreign
  // mention only ever gets into a reed via the composer's @ picker, whose
  // search results already came from a peer this server can reach (see
  // SearchUsers's federation fanout), so getByUserId's GET
  // /users/{id}/profile proxy has a real peer to resolve against. A
  // mention someone typed by hand pointing at an unknown/unreachable
  // server still falls through here via resolveFailed, same as a failed
  // local lookup.
  $: unresolved = settled && !username && resolveFailed;

  onMount(async () => {
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

</script>

{#if unresolved}
  {`~${userID}`}
{:else}
  <Username
    {userID}
    {username}
    class="inline-link mention-link"
    at
    fire={false}
    linked={!preview}
  />
{/if}
