<script>
  import { goto } from '$app/navigation';
  import { userRepository } from '$lib/repositories/user';
  import { isRoot } from '$lib/utils/identityRef';

  /** `userID@serverID` id — callers already have this form, so this
   * component takes it as a single value rather than composing it. */
  /** @type {string} */
  export let userID;
  /** Display name. Omit to resolve it from local cache or the server —
   * shows the bare id until that resolves. */
  /** @type {string | undefined} */
  export let username = undefined;
  /** Set when nesting inside another clickable element (e.g. a reed row/quote)
   * that navigates elsewhere on click. */
  export let stopPropagation = false;
  /** Render a leading "@" (resolved username) or "~" (raw unresolved id)
   * as part of the link/text itself, instead of the caller prefixing a
   * plain "@" outside it. */
  export let at = false;
  /** Render as a profile link. Set false when an ancestor element already
   * handles navigation (e.g. a whole Quote block linking to the reed) so this
   * doesn't create a nested/competing click target. */
  export let linked = true;
  /** Show the root fire / admin sheriff badge. */
  export let fire = true;
  /** Text color override (e.g. "var(--muted)" to match surrounding dimmed text).
   * Ignored for the admin (userID '1') case, which always renders red. */
  export let color = '';
  /** Extra classes merged onto the rendered element (e.g. caller-specific link styling). */
  let extraClass = '';
  export { extraClass as class };

  let resolvedUsername = username;
  let role = '';
  $: resolvedUsername = username;
  $: role = '', userID;
  $: if (userID && (!username || (fire && !isAdmin))) resolveUser(userID);

  // Role comes from the locally stored signed profile; only a missing
  // username falls back to fetching it.
  async function resolveUser(id) {
    const user = username ? await userRepository.get(id) : await userRepository.getByUserId(id);
    if (id !== userID) return;
    if (!username && user?.username) resolvedUsername = user.username;
    role = user?.role ?? '';
  }

  $: displayName = resolvedUsername ?? userID;
  $: atPrefix = at ? (displayName !== userID ? '@' : '~') : '';
  $: isAdmin = isRoot(userID);
  $: isSheriff = !isAdmin && role === 'admin';
  // Inline, not class-based: callers embed this in contexts with their own
  // `.inline-link`/etc color rules (e.g. MarkdownParser's link styling) that
  // would otherwise win on specificity/source order over a scoped class.
  $: resolvedColor = isAdmin ? 'var(--error)' : color;

  function activate(event) {
    if (stopPropagation) event.stopPropagation();
    goto(`/profile/${userID}`);
  }
</script>

<span class="username-root {extraClass}">
  {#if isAdmin && fire}
    <img class="username-fire" src="/icons/fire.gif" alt="" width="16" height="16" />
  {:else if isSheriff && fire}
    <span class="username-sheriff" style={resolvedColor ? `color: ${resolvedColor}` : ''}></span>
  {/if}
  {#if linked}
    <a
      href="/profile/{userID}"
      class="username {extraClass}"
      class:admin={isAdmin}
      style={resolvedColor ? `color: ${resolvedColor}` : ''}
      on:click|preventDefault={activate}
      >{atPrefix}{displayName}</a
    >
  {:else}
    <span
      class="username {extraClass}"
      class:admin={isAdmin}
      style={resolvedColor ? `color: ${resolvedColor}` : ''}
      >{atPrefix}{displayName}</span
    >
  {/if}
</span>

<style>
  .username-root {
    display: inline-flex;
    align-items: baseline;
  }

  .username-fire {
    display: inline-block;
    width: 1em;
    height: 1em;
    margin-right: 0.2em;
    vertical-align: -0.15em;
    flex-shrink: 0;
  }

  .username-sheriff {
    display: inline-block;
    width: 1em;
    height: 1em;
    margin-right: 0.2em;
    align-self: center;
    flex-shrink: 0;
    color: var(--fg);
    background-color: currentColor;
    -webkit-mask: url('/icons/sheriff-16.svg') center / contain no-repeat;
    mask: url('/icons/sheriff-16.svg') center / contain no-repeat;
  }

  .username {
    color: var(--fg);
    text-decoration: none;
  }

  a.username:hover {
    text-decoration: underline;
  }

  .username.admin {
    color: var(--error);
  }
</style>
