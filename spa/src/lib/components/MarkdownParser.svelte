<script lang="ts">
  export let text: string = '';
  export let className: string = '';

  // Simple markdown parser that safely converts markdown to HTML
  function parseMarkdown(input: string): string {
    if (!input) return '';

    let html = input;

    // Parse links [text](url) - URL-encode the href
    html = html.replace(/\[([^\]]+)\]\(([^)]+)\)/g, (_, text, url) => `<a href="${url}" target="_blank" rel="noopener noreferrer">${text}</a>`);

    // Parse bold **text** or __text__
    html = html.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
    html = html.replace(/__([^_]+)__/g, '<strong>$1</strong>');

    // Parse italic *text* or _text_
    html = html.replace(/\*([^*]+)\*/g, '<em>$1</em>');
    html = html.replace(/_([^_]+)_/g, '<em>$1</em>');

    // Parse code `text`
    html = html.replace(/`([^`]+)`/g, '<code>$1</code>');

    // Parse line breaks (double newline = paragraph, single newline = br)
    html = html.replace(/\n\n/g, '</p><p>');
    html = html.replace(/\n/g, '<br>');

    return html;
  }

  $: parsedHtml = parseMarkdown(text);
</script>

<div class="markdown-content {className}">
  {@html parsedHtml}
</div>

<style>
  .markdown-content {
    line-height: 1.5;
  }

  .markdown-content a {
    color: var(--primary, #007bff);
    text-decoration: underline;
    word-break: break-all;
  }

  .markdown-content a:hover {
    text-decoration: none;
  }

  .markdown-content strong {
    font-weight: 600;
  }

  .markdown-content em {
    font-style: italic;
  }

  .markdown-content code {
    background: var(--input-bg, #f5f5f5);
    padding: 0.125rem 0.25rem;
    border-radius: 3px;
    font-family: 'Courier New', Courier, monospace;
    font-size: 0.9em;
  }

  .markdown-content {
    overflow-wrap: break-word;
  }
</style>
