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
  /** False for text that isn't a published reed (bios, composer preview). */
  export let fullRender: boolean = true;

  const MK = '                       _..gggggppppp.._\n                  _.gd$$$$$$$$$$$$$$$$$$bp._\n               .g$$$$$$P^^""j$$b""""^^T$$$$$$p.\n            .g$$$P^T$$b    d$P T;       ""^^T$$$p.\n          .d$$P^"  :$; `  :$;                "^T$$b.\n        .d$$P\'      T$b.   T$b                  `T$$b.\n       d$$P\'      .gg$$$$bpd$$$p.d$bpp.           `T$$b\n      d$$P      .d$$$$$$$$$$$$$$$$$$$$bp.           T$$b\n     d$$P      d$$$$$$$$$$$$$$$$$$$$$$$$$b.          T$$b\n    d$$P      d$$$$$$$$$$$$$$$$$$P^^T$$$$P            T$$b\n   d$$P    \'-\'T$$$$$$$$$$$$$$$$$$bggpd$$$$b.           T$$b\n  :$$$      .d$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$p._.g.     $$$;\n  $$$;     d$$$$$$$$$$$$$$$$$$$$$$$P^"^T$$$$P^^T$$$;    :$$$\n :$$$     :$$$$$$$$$$$$$$:$$$$$$$$$_    "^T$bpd$$$$,     $$$;\n $$$;     :$$$$$$$$$$$$$$bT$$$$$P^^T$p.    `T$$$$$$;     :$$$\n:$$$      :$$$$$$$$$$$$$$P `^^^\'    "^T$p.    lb`TP       $$$;\n:$$$      $$$$$$$$$$$$$$$              `T$$p._;$b         $$$;\n$$$;      $$$$$$$$$$$$$$;                `T$$$$:Tb        :$$$\n$$$;      $$$$$$$$$$$$$$$                        Tb    _  :$$$\n:$$$     d$$$$$$$$$$$$$$$.                        $b.__Tb $$$;\n:$$$  .g$$$$$$$$$$$$$$$$$$$p...______...gp._      :$`^^^\' $$$;\n $$$;  `^^\'T$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$p.    Tb._, :$$$\n :$$$       T$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$b.   "^"  $$$;\n  $$$;       `$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$b      :$$$\n  :$$$        $$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$;     $$$;\n   T$$b    _  :$$`$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$;   d$$P\n    T$$b   T$g$$; :$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$  d$$P\n     T$$b   `^^\'  :$$ "^T$$$$$$$$$$$$$$$$$$$$$$$$$$$ d$$P\n      T$$b        $P     T$$$$$$$$$$$$$$$$$$$$$$$$$;d$$P\n       T$$b.      \'       $$$$$$$$$$$$$$$$$$$$$$$$$$$$P\n        `T$$$p.  syrinx  d$$$$$$$$$$$$$$$$$$$$$$$$$$P\'\n          `T$$$$p..__..g$$$$$$$$$$$$$$$$$$$$$$$$$$P\'\n            "^$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$$^"\n               "^T$$$$$$$$$$$$$$$$$$$$$$$$$$P^"\n                   """^^^T$$$$$$$$$$P^^^"""';

  let pendingUrl = '';
  let modalOpen = false;

  function openExternal(url: string) {
    pendingUrl = url;
    modalOpen = true;
  }

  $: easterEgg = fullRender && text === 'abacabb' ? MK : undefined;
  $: doc = parseReedMarkdown(preview ? (text ?? '').trim() : (text ?? ''));
</script>

<div class="markdown-content {className}" role="presentation">
  {#if easterEgg}
    <pre class="easter-egg">{easterEgg}</pre>
  {:else}
    {#each doc.blocks as block}
      {#if block.type === 'pre'}
        <pre>{block.value}</pre>
      {:else if block.type === 'paragraph'}
        <p>
          <MarkdownInline nodes={block.children} {preview} {usernameHints} onExternal={openExternal} />
        </p>
      {/if}
    {/each}
  {/if}
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

  .markdown-content pre.easter-egg {
    white-space: pre;
    line-height: 1.1;
    font-size: 0.7em;
  }

  .markdown-content :global(p) {
    margin: 0 0 0.5em 0;
    white-space: pre-wrap;
  }

  .markdown-content :global(p:last-child) {
    margin-bottom: 0;
  }
</style>
