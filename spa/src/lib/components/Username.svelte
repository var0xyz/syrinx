<script>
  import { goto } from '$app/navigation';

  /** `userID@serverID` id — callers already have this form, so this
   * component takes it as a single value rather than composing it. */
  /** @type {string} */
  export let userID;
  /** @type {string} */
  export let username;
  /** Set when nesting inside another clickable element (e.g. a reed row/quote)
   * that navigates elsewhere on click. */
  export let stopPropagation = false;
  /** Render a leading "@" as part of the link/text itself, instead of the
   * caller prefixing a plain "@" outside it. */
  export let at = false;
  /** Render as a profile link. Set false when an ancestor element already
   * handles navigation (e.g. a whole Quote block linking to the reed) so this
   * doesn't create a nested/competing click target. */
  export let linked = true;
  /** Show the admin fire badge. */
  export let fire = true;
  /** Text color override (e.g. "var(--muted)" to match surrounding dimmed text).
   * Ignored for the admin (userID '1') case, which always renders red. */
  export let color = '';
  /** Extra classes merged onto the rendered element (e.g. caller-specific link styling). */
  let extraClass = '';
  export { extraClass as class };

  // Split "userID@serverID" on the LAST '@' — matches the Go side's
  // identity.ParseIdentityID convention.
  $: atIndex = userID ? userID.lastIndexOf('@') : -1;
  $: bareUserID = atIndex > 0 ? userID.slice(0, atIndex) : userID;
  $: isAdmin = bareUserID === '1';
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
  {/if}
  {#if linked}
    <a
      href="/profile/{userID}"
      class="username {extraClass}"
      class:admin={isAdmin}
      style={resolvedColor ? `color: ${resolvedColor}` : ''}
      on:click|preventDefault={activate}
      >{at ? '@' : ''}{username}</a
    >
  {:else}
    <span
      class="username {extraClass}"
      class:admin={isAdmin}
      style={resolvedColor ? `color: ${resolvedColor}` : ''}
      >{at ? '@' : ''}{username}</span
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
