<script lang="ts">
  import ExternalLinkModal from './ExternalLinkModal.svelte';
  import MarkdownInline from './MarkdownInline.svelte';
  import { parseReedMarkdown } from '$lib/utils/reedMarkdown';

  export let text: string = '';
  export let className: string = '';
  /** When true, links/mentions/pipes are inert (label only). */
  export let preview: boolean = false;
  /** Optional userID -> username display hints (composer preview only). */
  export let usernameHints: Map<string, string> | undefined = undefined;

  let pendingUrl = '';
  let modalOpen = false;

  function openExternal(url: string) {
    pendingUrl = url;
    modalOpen = true;
  }

  $: doc = parseReedMarkdown(preview ? (text ?? '').trim() : (text ?? ''));
</script>

<div class="markdown-content {className}" role="presentation">
  {#each doc.blocks as block}
    {#if block.type === 'pre'}
      <pre>{block.value}</pre>
    {:else if block.type === 'paragraph'}
      <p>
        <MarkdownInline nodes={block.children} {preview} {usernameHints} onExternal={openExternal} />
      </p>
    {/if}
  {/each}
</div>

<ExternalLinkModal url={pendingUrl} open={modalOpen} on:close={() => { modalOpen = false; }} />

<style>
  .markdown-content {
    line-height: 1.5;
    overflow-wrap: break-word;
  }

  .markdown-content :global(a),
  .markdown-content :global(.inline-link) {
    color: var(--primary, #007bff);
    text-decoration: underline;
    word-break: break-all;
    cursor: pointer;
  }

  .markdown-content :global(a:hover),
  .markdown-content :global(.inline-link:hover) {
    text-decoration: none;
  }

  .markdown-content :global(a.mention-link),
  .markdown-content :global(.inline-link.mention-link) {
    font-weight: 600;
    text-decoration: none;
  }

  .markdown-content :global(a.mention-link:hover),
  .markdown-content :global(.inline-link.mention-link:hover) {
    text-decoration: underline;
  }

  .markdown-content :global(strong) {
    font-weight: 600;
  }

  .markdown-content :global(em) {
    font-style: italic;
  }

  .markdown-content :global(del) {
    text-decoration: line-through;
  }

  .markdown-content :global(code) {
    background: var(--input-bg, #f5f5f5);
    padding: 0.125rem 0.25rem;
    border-radius: 3px;
    font-family: 'Courier New', Courier, monospace;
    font-size: 0.9em;
  }

  .markdown-content :global(pre) {
    background: var(--input-bg, #f5f5f5);
    padding: 0.5rem 0.75rem;
    border-radius: 6px;
    font-family: 'Courier New', Courier, monospace;
    font-size: 0.9em;
    overflow-x: auto;
    white-space: pre-wrap;
    margin: 0 0 0.5em 0;
  }

  .markdown-content :global(p) {
    margin: 0 0 0.5em 0;
    white-space: pre-wrap;
  }

  .markdown-content :global(p:last-child) {
    margin-bottom: 0;
  }
</style>
