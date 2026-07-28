/**
 * Explicit reed-markdown grammar → AST.
 * Render with Svelte (MarkdownParser); do not build HTML strings.
 *
 * Constructs (scan precedence):
 *   blocks: fenced ```code```, then paragraphs (\n\n)
 *   inlines: [label](url) → link or label-only; `code`;
 *            ~strike~, _italic_, *bold*, #hashtag → link, \n
 *
 * Link href policy:
 *   - http / https / mailto / web+syrinx → as-is
 *   - no scheme → prepend https://
 *   - any other scheme / empty → label text only
 *
 * web+syrinx://… links are still `link` nodes; click routing uses internalPath().
 */

export type Inline =
  | { type: 'text'; value: string }
  | { type: 'break' }
  | { type: 'code'; value: string }
  | { type: 'strong' | 'em' | 'del'; children: Inline[] }
  | { type: 'link'; href: string; children: Inline[] };

export type Block =
  | { type: 'paragraph'; children: Inline[] }
  | { type: 'pre'; value: string };

export type Doc = { blocks: Block[] };

export function formatChannelHref(tag: string): string {
  return `web+syrinx://channel/${tag}`;
}

const ALLOWED_LINK_SCHEMES = new Set(['http', 'https', 'mailto', 'web+syrinx']);

/**
 * Resolve a markdown link destination.
 * Returns the href to store, or null to strip the tag and keep the label only.
 */
export function resolveLinkHref(raw: string): string | null {
  const url = raw.trim();
  if (!url) return null;

  const m = /^([a-zA-Z][a-zA-Z0-9+.-]*):/.exec(url);
  if (!m) {
    return `https://${url}`;
  }
  const scheme = m[1].toLowerCase();
  if (ALLOWED_LINK_SCHEMES.has(scheme)) {
    return url;
  }
  return null;
}

/**
 * Map a web+syrinx href to an in-app path, or null if it is not an internal link
 * (caller should treat it as external — e.g. open the disclosure modal).
 */
export function internalPath(href: string): string | null {
  const raw = href.trim();
  const user = /^web\+syrinx:\/\/users\/([^/]+)\/([^/]+)\/?$/i.exec(raw);
  if (user?.[1] && user[2]) {
    return `/profile/${user[2]}`;
  }
  const channel = /^web\+syrinx:\/\/channel\/(.+)$/i.exec(raw);
  if (channel?.[1]) {
    return `/channel/${encodeURIComponent(channel[1])}`;
  }
  return null;
}

export function parseReedMarkdown(input: string): Doc {
  if (!input) return { blocks: [] };

  const blocks: Block[] = [];
  let i = 0;
  const n = input.length;

  while (i < n) {
    if (isLineStart(input, i) && input.startsWith('```', i)) {
      const fence = readFence(input, i);
      if (fence) {
        blocks.push({ type: 'pre', value: fence.body });
        i = fence.end;
        continue;
      }
    }

    const nextFence = findNextFenceStart(input, i);
    const chunk = input.slice(i, nextFence);
    i = nextFence;

    if (chunk.length === 0) continue;

    for (const para of splitParagraphs(chunk)) {
      blocks.push({ type: 'paragraph', children: parseInlines(para) });
    }
  }

  return { blocks };
}

function isLineStart(s: string, i: number): boolean {
  return i === 0 || s[i - 1] === '\n';
}

function findNextFenceStart(s: string, from: number): number {
  let i = from;
  while (i < s.length) {
    if (isLineStart(s, i) && s.startsWith('```', i) && readFence(s, i)) {
      return i;
    }
    i++;
  }
  return s.length;
}

/** ```[info]\n body \n``` */
function readFence(s: string, start: number): { body: string; end: number } | null {
  if (!s.startsWith('```', start)) return null;
  let i = start + 3;
  while (i < s.length && s[i] !== '\n') i++;
  if (i >= s.length) return null;
  i++; // skip newline after info line
  const bodyStart = i;
  while (i < s.length) {
    if (isLineStart(s, i) && s.startsWith('```', i)) {
      let end = i + 3;
      while (end < s.length && s[end] !== '\n') end++;
      if (end < s.length && s[end] === '\n') end++;
      const body = s.slice(bodyStart, i);
      const trimmed = body.endsWith('\n') ? body.slice(0, -1) : body;
      return { body: trimmed, end };
    }
    i++;
  }
  return null;
}

function splitParagraphs(chunk: string): string[] {
  return chunk.split(/\n\n+/).filter((p) => p.length > 0);
}

function parseInlines(s: string): Inline[] {
  const out: Inline[] = [];
  let i = 0;
  let textStart = 0;

  const flush = (end: number) => {
    if (end > textStart) {
      out.push({ type: 'text', value: s.slice(textStart, end) });
    }
  };

  while (i < s.length) {
    if (s[i] === '\n') {
      flush(i);
      out.push({ type: 'break' });
      i += 1;
      textStart = i;
      continue;
    }

    if (s[i] === '[') {
      const link = readLink(s, i);
      if (link) {
        flush(i);
        out.push(link.node);
        i = link.end;
        textStart = i;
        continue;
      }
    }

    if (s[i] === '`') {
      const code = readInlineCode(s, i);
      if (code) {
        flush(i);
        out.push(code.node);
        i = code.end;
        textStart = i;
        continue;
      }
    }

    if (s[i] === '~') {
      const delim = readDelimited(s, i, '~', 'del');
      if (delim) {
        flush(i);
        out.push(delim.node);
        i = delim.end;
        textStart = i;
        continue;
      }
    }

    if (s[i] === '_') {
      const delim = readDelimited(s, i, '_', 'em');
      if (delim) {
        flush(i);
        out.push(delim.node);
        i = delim.end;
        textStart = i;
        continue;
      }
    }

    if (s[i] === '*') {
      const delim = readDelimited(s, i, '*', 'strong');
      if (delim) {
        flush(i);
        out.push(delim.node);
        i = delim.end;
        textStart = i;
        continue;
      }
    }

    if (
      s[i] === '#' &&
      (i === 0 || s[i - 1] === '\n' || /\s/.test(s[i - 1]))
    ) {
      const tag = readHashtag(s, i);
      if (tag) {
        flush(i);
        out.push({
          type: 'link',
          href: formatChannelHref(tag.tag),
          children: [{ type: 'text', value: `#${tag.tag}` }],
        });
        i = tag.end;
        textStart = i;
        continue;
      }
    }

    i += 1;
  }

  flush(s.length);
  return mergeAdjacentText(out);
}

function mergeAdjacentText(nodes: Inline[]): Inline[] {
  const out: Inline[] = [];
  for (const node of nodes) {
    const prev = out[out.length - 1];
    if (node.type === 'text' && prev?.type === 'text') {
      prev.value += node.value;
    } else {
      out.push(node);
    }
  }
  return out;
}

function readLink(
  s: string,
  start: number
): { node: Inline; end: number } | null {
  if (s[start] !== '[') return null;
  const labelEnd = s.indexOf(']', start + 1);
  if (labelEnd < 0) return null;
  if (s[labelEnd + 1] !== '(') return null;
  const urlEnd = s.indexOf(')', labelEnd + 2);
  if (urlEnd < 0) return null;

  const label = s.slice(start + 1, labelEnd);
  const url = s.slice(labelEnd + 2, urlEnd);
  const end = urlEnd + 1;
  const children: Inline[] = [{ type: 'text', value: label }];

  const href = resolveLinkHref(url);
  if (href === null) {
    return { node: { type: 'text', value: label }, end };
  }

  return { node: { type: 'link', href, children }, end };
}

function readInlineCode(
  s: string,
  start: number
): { node: Inline; end: number } | null {
  if (s[start] !== '`') return null;
  let i = start + 1;
  while (i < s.length && s[i] !== '`' && s[i] !== '\n') i++;
  if (i >= s.length || s[i] !== '`') return null;
  const value = s.slice(start + 1, i);
  if (value.length === 0) return null;
  return { node: { type: 'code', value }, end: i + 1 };
}

function readDelimited(
  s: string,
  start: number,
  delim: string,
  type: 'strong' | 'em' | 'del'
): { node: Inline; end: number } | null {
  if (s[start] !== delim) return null;
  let i = start + 1;
  while (i < s.length && s[i] !== delim && s[i] !== '\n') i++;
  if (i >= s.length || s[i] !== delim) return null;
  const inner = s.slice(start + 1, i);
  if (inner.length === 0) return null;
  return {
    node: { type, children: parseInlines(inner) },
    end: i + 1,
  };
}

function readHashtag(
  s: string,
  start: number
): { tag: string; end: number } | null {
  if (s[start] !== '#') return null;
  let i = start + 1;
  while (i < s.length && !/\s/.test(s[i])) i++;
  const tag = s.slice(start + 1, i);
  if (tag.length === 0) return null;
  return { tag, end: i };
}
