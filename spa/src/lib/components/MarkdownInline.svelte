<script lang="ts">
  import { goto } from '$app/navigation';
  import { internalPath, parseMentionHref, type Inline } from '$lib/utils/reedMarkdown';
  import MentionLink from './MentionLink.svelte';

  export let nodes: Inline[] = [];
  export let preview: boolean = false;
  /** Open an external (non–web+syrinx) href in ExternalLinkModal. */
  export let onExternal: (url: string) => void = () => {};
  /** Optional userID -> username display hints (composer preview only). */
  export let usernameHints: Map<string, string> | undefined = undefined;

  function activateLink(href: string) {
    const path = internalPath(href);
    if (path) {
      goto(path);
      return;
    }
    onExternal(href);
  }
</script>

{#each nodes as node}
  {#if node.type === 'text'}
    {node.value}
  {:else if node.type === 'break'}
    <br />
  {:else if node.type === 'code'}
    <code>{node.value}</code>
  {:else if node.type === 'strong'}
    <strong><svelte:self nodes={node.children} {preview} {onExternal} {usernameHints} /></strong>
  {:else if node.type === 'em'}
    <em><svelte:self nodes={node.children} {preview} {onExternal} {usernameHints} /></em>
  {:else if node.type === 'del'}
    <del><svelte:self nodes={node.children} {preview} {onExternal} {usernameHints} /></del>
  {:else if node.type === 'link' && parseMentionHref(node.href)}
    {@const target = parseMentionHref(node.href)}
    <MentionLink userID={target.userID} serverID={target.serverID} {preview} hintUsername={usernameHints?.get(target.userID)} />
  {:else if node.type === 'link'}
    {#if preview}
      <span class="inline-link"><svelte:self nodes={node.children} {preview} {onExternal} {usernameHints} /></span>
    {:else}
      <a
        href={internalPath(node.href) ?? '#'}
        class="inline-link"
        on:click|preventDefault={() => activateLink(node.href)}
      ><svelte:self nodes={node.children} {preview} {onExternal} {usernameHints} /></a>
    {/if}
  {:else if node.type === 'mention'}
    <MentionLink userID={node.userID} serverID={node.serverID} {preview} hintUsername={usernameHints?.get(node.userID)} />
  {/if}
{/each}
