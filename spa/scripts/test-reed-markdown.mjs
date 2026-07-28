#!/usr/bin/env node
/**
 * Unit checks for spa/src/lib/utils/reedMarkdown.ts
 * Run: npm run test:reed-markdown
 */

import assert from 'node:assert/strict';
import {
  formatChannelHref,
  internalPath,
  parseReedMarkdown,
} from '../src/lib/utils/reedMarkdown.ts';


function firstInlines(input) {
  const doc = parseReedMarkdown(input);
  assert.equal(doc.blocks.length, 1);
  assert.equal(doc.blocks[0].type, 'paragraph');
  return doc.blocks[0].children;
}

function onlyLink(input) {
  const nodes = firstInlines(input);
  assert.equal(nodes.length, 1, `expected single node for ${JSON.stringify(input)}`);
  assert.equal(nodes[0].type, 'link', `expected link for ${JSON.stringify(input)}`);
  return nodes[0];
}

/** Attribute escape equivalent to setting a DOM attribute / Svelte binding serialization. */
function escapeAttr(value) {
  return String(value)
    .replace(/&/g, '&amp;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;');
}

function safeBoundHrefHtml(url, label) {
  return `<a href="${escapeAttr(url)}">${escapeAttr(label)}</a>`;
}

// empty
assert.deepEqual(parseReedMarkdown(''), { blocks: [] });
assert.deepEqual(parseReedMarkdown(null ?? ''), { blocks: [] });

// bold / italic / strike (budget dialect)
{
  const nodes = firstInlines('A *bold* and _italic_ and ~strike~');
  assert.equal(nodes[0].type, 'text');
  assert.equal(nodes[1].type, 'strong');
  assert.equal(nodes[1].children[0].value, 'bold');
  assert.equal(nodes[3].type, 'em');
  assert.equal(nodes[5].type, 'del');
}

// inline code — no hashtag / emphasis inside
{
  const nodes = firstInlines('use `#tag` and `*x*`');
  assert.equal(nodes[1].type, 'code');
  assert.equal(nodes[1].value, '#tag');
  assert.equal(nodes[3].type, 'code');
  assert.equal(nodes[3].value, '*x*');
}

// fenced code block
{
  const doc = parseReedMarkdown('before\n\n```\n#notachannel\n*bold*\n```\nafter');
  assert.equal(doc.blocks.length, 3);
  assert.equal(doc.blocks[0].type, 'paragraph');
  assert.equal(doc.blocks[1].type, 'pre');
  assert.equal(doc.blocks[1].value, '#notachannel\n*bold*');
  assert.equal(doc.blocks[2].type, 'paragraph');
  assert.equal(doc.blocks[2].children[0].value, 'after');
}

// http(s) link
{
  const nodes = firstInlines('[docs](https://example.com/a)');
  assert.equal(nodes[0].type, 'link');
  assert.equal(nodes[0].href, 'https://example.com/a');
  assert.equal(nodes[0].children[0].value, 'docs');
}

// bare host → https://
{
  const bare = onlyLink('[site](var0.xyz)');
  assert.equal(bare.href, 'https://var0.xyz');
}

// mailto allowed
{
  const mail = onlyLink('[me](mailto:a@b.test)');
  assert.equal(mail.href, 'mailto:a@b.test');
}

// empty url → label only
{
  const nodes = firstInlines('[x]()');
  assert.equal(nodes[0].type, 'text');
  assert.equal(nodes[0].value, 'x');
}

// unsupported scheme → label only
{
  const nodes = firstInlines('[x](javascript:alert)');
  assert.equal(nodes[0].type, 'text');
  assert.equal(nodes[0].value, 'x');
}

// web+syrinx user → ordinary link (in-app via internalPath)
{
  const nodes = firstInlines('[Ada](web+syrinx://users/srv/uid123)');
  assert.equal(nodes[0].type, 'link');
  assert.equal(nodes[0].href, 'web+syrinx://users/srv/uid123');
  assert.equal(nodes[0].children[0].value, 'Ada');
  assert.equal(internalPath(nodes[0].href), '/profile/uid123');
}

assert.equal(internalPath('web+syrinx://users/a/b'), '/profile/b');
assert.equal(internalPath('https://x'), null);

// hashtag → link to channel href
{
  const nodes = firstInlines('hello #world more');
  assert.equal(nodes[0].type, 'text');
  assert.equal(nodes[0].value, 'hello ');
  assert.equal(nodes[1].type, 'link');
  assert.equal(nodes[1].href, formatChannelHref('world'));
  assert.equal(nodes[1].children[0].value, '#world');
  assert.equal(internalPath(nodes[1].href), '/channel/world');
}

// hashtag at start
{
  const nodes = firstInlines('#alone');
  assert.equal(nodes[0].type, 'link');
  assert.equal(nodes[0].href, formatChannelHref('alone'));
}

// soft break
{
  const nodes = firstInlines('a\nb');
  assert.equal(nodes[0].value, 'a');
  assert.equal(nodes[1].type, 'break');
  assert.equal(nodes[2].value, 'b');
}

// ---------------------------------------------------------------------------
// Attack / XSS regressions
// Payloads that used to break HTML string builders must stay opaque href
// data in the AST. Bound/escaped serialization must remain a single attribute.
// ---------------------------------------------------------------------------

const attacks = [
  {
    name: 'double-quote attribute breakout (schemeless → https)',
    markdown: '[x](y" onfocus="alert&#41;" tabindex="0)',
    href: 'https://y" onfocus="alert&#41;" tabindex="0',
  },
  {
    name: 'single quotes inside https href',
    markdown: "[x](https://evil.test/a'b)",
    href: "https://evil.test/a'b",
  },
  {
    name: 'angle brackets in https href',
    markdown: '[x](https://evil.test/<script>)',
    href: 'https://evil.test/<script>',
  },
];

for (const { name, markdown, href } of attacks) {
  const link = onlyLink(markdown);
  assert.equal(link.href, href, name);
  assert.equal(
    link.children[0].value,
    markdown.slice(1, markdown.indexOf(']')),
    `${name}: label`
  );

  const safe = safeBoundHrefHtml(link.href, 'x');
  assert.match(
    safe,
    /^<a href="[^"]*">x<\/a>$/,
    `${name}: safe HTML must be a single href attribute with escaped value`
  );
  assert.equal(
    safe.slice('<a href="'.length, safe.indexOf('">')),
    escapeAttr(link.href),
    `${name}: href attr value is fully escaped`
  );
}

// javascript (and quote-breakout with that scheme) → label only
{
  const plain = firstInlines('[click](javascript:alert%281%29)');
  assert.equal(plain[0].type, 'text');
  assert.equal(plain[0].value, 'click');

  const breakout = firstInlines('[x](javascript:void" onfocus="evil" x=)');
  assert.equal(breakout[0].type, 'text');
  assert.equal(breakout[0].value, 'x');
}

// Label with quotes / angle brackets stays text data (not markup)
{
  const link = onlyLink('["onclick](https://example.com/)');
  assert.equal(link.children[0].value, '"onclick');
  const safe = safeBoundHrefHtml(link.href, link.children[0].value);
  assert.ok(!/\sonclick=/i.test(safe));
  assert.match(safe, /&quot;onclick/);
}

// Nested attempt: payload only in AST href, never split into extra nodes
{
  const nodes = firstInlines('[ok](https://a.test/"onclick="evil)');
  assert.equal(nodes.length, 1);
  assert.equal(nodes[0].type, 'link');
  assert.equal(nodes[0].href, 'https://a.test/"onclick="evil');
}

// User example: empty + javascript → label; bare host → https; https as-is
{
  const doc = parseReedMarkdown(
    '[Hola]()\n[Hola](javascript:alert)\n[Hola](var0.xyz)\n[Hola](https://var0.xyz)'
  );
  const nodes = doc.blocks[0].children.filter((n) => n.type !== 'break');
  assert.equal(nodes.length, 4);
  assert.equal(nodes[0].type, 'text');
  assert.equal(nodes[0].value, 'Hola');
  assert.equal(nodes[1].type, 'text');
  assert.equal(nodes[1].value, 'Hola');
  assert.equal(nodes[2].type, 'link');
  assert.equal(nodes[2].href, 'https://var0.xyz');
  assert.equal(nodes[3].type, 'link');
  assert.equal(nodes[3].href, 'https://var0.xyz');
}

// Hashtag vs markdown-link precedence
{
  // [#hashtag](url) → ordinary link; label is the text "#hashtag"
  const a = onlyLink('[#hashtag](https://var0.xyz)');
  assert.equal(a.href, 'https://var0.xyz');
  assert.equal(a.children[0].value, '#hashtag');
  assert.equal(internalPath(a.href), null);

  // #[hashtag](url) → hashtag whose name is the rest of the \S+ run
  const b = onlyLink('#[hashtag](https://var0.xyz)');
  assert.equal(b.href, formatChannelHref('[hashtag](https://var0.xyz)'));
  assert.equal(b.children[0].value, '#[hashtag](https://var0.xyz)');
  assert.equal(
    internalPath(b.href),
    `/channel/${encodeURIComponent('[hashtag](https://var0.xyz)')}`
  );

  // #hashtag → normal channel link
  const c = onlyLink('#hashtag');
  assert.equal(c.href, formatChannelHref('hashtag'));
  assert.equal(c.children[0].value, '#hashtag');
  assert.equal(internalPath(c.href), '/channel/hashtag');
}

console.log('reedMarkdown: all checks passed');
