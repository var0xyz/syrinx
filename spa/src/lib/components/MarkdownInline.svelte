<script lang="ts">
  import { goto } from '$app/navigation';
  import { internalPath, type Inline } from '$lib/utils/reedMarkdown';

  export let nodes: Inline[] = [];
  export let preview: boolean = false;
  /** Open an external (non–web+syrinx) href in ExternalLinkModal. */
  export let onExternal: (url: string) => void = () => {};

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
    <strong><svelte:self nodes={node.children} {preview} {onExternal} /></strong>
  {:else if node.type === 'em'}
    <em><svelte:self nodes={node.children} {preview} {onExternal} /></em>
  {:else if node.type === 'del'}
    <del><svelte:self nodes={node.children} {preview} {onExternal} /></del>
  {:else if node.type === 'link'}
    {#if preview}
      <span class="inline-link"><svelte:self nodes={node.children} {preview} {onExternal} /></span>
    {:else}
      <a
        href={internalPath(node.href) ?? '#'}
        class="inline-link"
        on:click|preventDefault={() => activateLink(node.href)}
      ><svelte:self nodes={node.children} {preview} {onExternal} /></a>
    {/if}
  {/if}
{/each}
