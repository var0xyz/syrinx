<script lang="ts">
  import ExternalLinkModal from './ExternalLinkModal.svelte';

  export let text: string = '';
  export let className: string = '';
  export let preview: boolean = false;

  let pendingUrl = '';
  let modalOpen = false;

  // TODO: This way of escaping strings is extremely vulnerable to XSS attacks.
  // TODO: For the time being we'll keep it because it's just a PoC for now,
  // TODO: but this will not fly in prod. We need to be more robust.
  function parseMarkdown(input: string, intercept: boolean): string {
    if (!input) return '';

    let html = input;

    // Escape HTML special characters to prevent XSS
    html = html
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;');

    const linkTag = (url: string, label: string) => intercept
      ? `<a href="#" data-external-href="${url}">${label}</a>`
      : `<span class="inline-link">${label}</span>`;

    // Parse markdown links [text](url)
    html = html.replace(/\[([^\]]+)\]\(([^)]+)\)/g, (_, linkText, url) =>
      linkTag(url, linkText)
    );

    // Auto-link bare URLs (skip those already inside HTML attributes)
    // html = html.replace(/(?<![=">'])(https?:\/\/[^\s<>"]+)/g, (url) =>
    //   linkTag(url, url)
    // );

    // Parse bold **text**
    html = html.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');

    // Parse italic *text*
    html = html.replace(/\*([^*]+)\*/g, '<em>$1</em>');

    // Parse inline code `text`
    html = html.replace(/```\s+([^`]+)\s+```/gm, '<pre>$1</pre>');
    html = html.replace(/`([^`^\n]+)`/, '<code>$1</code>');

    // Parse line breaks
    const extraSpace = preview ? '' : '&nbsp;';
    html = html.replace(/\n\n/g, extraSpace + '</p><p>');
    html = html.replace(/\n/g, '<br>');

    return `<p>${html}</p>`;
  }

  function handleClick(event: MouseEvent) {
    if (preview) return;
    const target = (event.target as HTMLElement).closest('[data-external-href]');
    if (!target) return;

    event.preventDefault();
    pendingUrl = (target as HTMLElement).dataset.externalHref || '';
    modalOpen = true;
  }

  $: parsedHtml = parseMarkdown(text, !preview);
</script>

<div class="markdown-content {className}" on:click={handleClick} role="presentation">
  {@html parsedHtml}
</div>

<ExternalLinkModal url={pendingUrl} open={modalOpen} on:close={() => { modalOpen = false; }} />

<style>
  .markdown-content {
    line-height: 1.5;
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

  .markdown-content :global(strong) {
    font-weight: 600;
  }

  .markdown-content :global(em) {
    font-style: italic;
  }

  .markdown-content :global(code) {
    background: var(--input-bg, #f5f5f5);
    padding: 0.125rem 0.25rem;
    border-radius: 3px;
    font-family: 'Courier New', Courier, monospace;
    font-size: 0.9em;
  }

  .markdown-content :global(p) {
    margin: 0 0 0.5em 0;
    white-space: pre-wrap;
  }

  .markdown-content :global(p:last-child) {
    margin-bottom: 0;
  }

  .markdown-content {
    overflow-wrap: break-word;
  }
</style>
