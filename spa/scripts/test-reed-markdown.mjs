#!/usr/bin/env node
/**
 * Unit checks for spa/src/lib/utils/reedMarkdown.ts
 * Run: npm run test:reed-markdown
 */

import assert from 'node:assert/strict';
import {
  formatPipeHref,
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

// bold / italic: multi-word content still matches...
{
  const bold = firstInlines('*this is bold*');
  assert.equal(bold[0].type, 'strong');
  assert.equal(bold[0].children[0].value, 'this is bold');

  const italic = firstInlines('_this is italic_');
  assert.equal(italic[0].type, 'em');
  assert.equal(italic[0].children[0].value, 'this is italic');
}

// ...but a space touching either delimiter disqualifies it (not emphasis;
// stays literal text) — "* Tomorrow *" must not render as bold.
{
  const cases = ['* Tomorrow *', '*bold *', '* bold*', '_ hi there _', '_italic _', '_ italic_'];
  for (const input of cases) {
    const nodes = firstInlines(input);
    assert.equal(nodes.length, 1, input);
    assert.equal(nodes[0].type, 'text', input);
    assert.equal(nodes[0].value, input, input);
  }
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

// hashtag → link to pipe href
{
  const nodes = firstInlines('hello #world more');
  assert.equal(nodes[0].type, 'text');
  assert.equal(nodes[0].value, 'hello ');
  assert.equal(nodes[1].type, 'link');
  assert.equal(nodes[1].href, formatPipeHref('world'));
  assert.equal(nodes[1].children[0].value, '#world');
  assert.equal(internalPath(nodes[1].href), '/pipe/world');
}

// hashtag at start
{
  const nodes = firstInlines('#alone');
  assert.equal(nodes[0].type, 'link');
  assert.equal(nodes[0].href, formatPipeHref('alone'));
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
  assert.equal(b.href, formatPipeHref('[hashtag](https://var0.xyz)'));
  assert.equal(b.children[0].value, '#[hashtag](https://var0.xyz)');
  assert.equal(
    internalPath(b.href),
    `/pipe/${encodeURIComponent('[hashtag](https://var0.xyz)')}`
  );

  // #hashtag → normal pipe link
  const c = onlyLink('#hashtag');
  assert.equal(c.href, formatPipeHref('hashtag'));
  assert.equal(c.children[0].value, '#hashtag');
  assert.equal(internalPath(c.href), '/pipe/hashtag');
}

// ---------------------------------------------------------------------------
// Mentions: ~userID@serverID. IDs are alphanumeric runs of ANY length ≥ 1
// (not fixed-width — e.g. the root user's id is "1"; foreign servers may
// mint IDs of any length). Boundary = the alphanumeric run itself, same
// idea as #hashtag's \S+ run. Strikethrough (~text~) takes precedence
// whenever a closing '~' exists.
// ---------------------------------------------------------------------------

{
  // well-formed mention, mid-sentence
  const nodes = firstInlines('hi ~a1B2c3D4@srv1xyz1 there');
  assert.equal(nodes[0].type, 'text');
  assert.equal(nodes[0].value, 'hi ');
  assert.equal(nodes[1].type, 'mention');
  assert.equal(nodes[1].userID, 'a1B2c3D4');
  assert.equal(nodes[1].serverID, 'srv1xyz1');
  assert.equal(nodes[2].type, 'text');
  assert.equal(nodes[2].value, ' there');
}

{
  // mention at start of content, end of content
  const nodes = firstInlines('~a1B2c3D4@srv1xyz1');
  assert.equal(nodes.length, 1);
  assert.equal(nodes[0].type, 'mention');
  assert.equal(nodes[0].userID, 'a1B2c3D4');
  assert.equal(nodes[0].serverID, 'srv1xyz1');
}

{
  // root user id is "1" — a single-char, non-alphabet-in-the-typical-sense
  // but still alphanumeric ID. Must resolve as a mention.
  const nodes = firstInlines('ping ~1@CcODhAr7 please');
  assert.equal(nodes[1].type, 'mention');
  assert.equal(nodes[1].userID, '1');
  assert.equal(nodes[1].serverID, 'CcODhAr7');
}

{
  // closing '~' present → strikethrough wins, never a mention, even though
  // the inner text is exactly userID@serverID shaped.
  const nodes = firstInlines('~a1B2c3D4@srv1xyz1~');
  assert.equal(nodes.length, 1);
  assert.equal(nodes[0].type, 'del');
  assert.equal(nodes[0].children[0].value, 'a1B2c3D4@srv1xyz1');
}

{
  // missing '@' separator → not a mention (whole alphanumeric run is
  // consumed as a candidate userID, then no '@' follows)
  const nodes = firstInlines('~a1B2c3D4srv1xyz1 rest');
  assert.equal(nodes[0].type, 'text');
  assert.ok(nodes[0].value.startsWith('~a1B2c3D4srv1xyz1'));
}

{
  // '@' with nothing alphanumeric after it → no serverID, not a mention
  const nodes = firstInlines('~a1B2c3D4@ rest');
  assert.equal(nodes[0].type, 'text');
  assert.ok(nodes[0].value.startsWith('~a1B2c3D4@'));
}

{
  // punctuation is never part of an ID — a server id with a hyphen (as
  // federation might otherwise produce) does not form a mention; only the
  // alphanumeric prefix before the hyphen would be attempted, and fails
  // without a proper trailing boundary since '-' just isn't consumed at all
  // (the run simply stops there, which is a valid, complete match).
  const nodes = firstInlines('~a1B2c3D4@some-id rest');
  assert.equal(nodes[0].type, 'mention');
  assert.equal(nodes[0].userID, 'a1B2c3D4');
  assert.equal(nodes[0].serverID, 'some');
  assert.equal(nodes[1].type, 'text');
  assert.equal(nodes[1].value, '-id rest');
}

{
  // chained '@' right after a serverID run → serverID stops at the second
  // '@' (not part of the alphanumeric alphabet); the mention is still
  // well-formed up to that point, followed by literal text.
  const nodes = firstInlines('~a1B2c3D4@srv1xyz1@more');
  assert.equal(nodes[0].type, 'mention');
  assert.equal(nodes[0].serverID, 'srv1xyz1');
  assert.equal(nodes[1].type, 'text');
  assert.equal(nodes[1].value, '@more');
}

{
  // two mentions in one paragraph — a second mention's '~' must never be
  // mistaken for a strikethrough closer of the first.
  const nodes = firstInlines('~a1B2c3D4@srv1xyz1 and ~e5F6g7H8@srv2abc2');
  const mentions = nodes.filter((n) => n.type === 'mention');
  assert.equal(mentions.length, 2);
  assert.equal(mentions[0].userID, 'a1B2c3D4');
  assert.equal(mentions[1].userID, 'e5F6g7H8');
}

{
  // strikethrough is single-word only — a space before any closing '~'
  // disqualifies it. ~hey you~ must NOT strikethrough, and since "hey" has
  // no '@' immediately after it, it's not a mention either.
  const nodes = firstInlines('~hey you~');
  assert.equal(nodes[0].type, 'text');
  assert.ok(nodes[0].value.startsWith('~hey you'), nodes[0].value);
}

{
  // "~hey you ~foo": first '~' has no closer without crossing a space (not
  // strikethrough) and "hey" has no '@' right after it (not a mention);
  // second '~' has no closer and "foo" has no '@' either. All literal text.
  const nodes = firstInlines('~hey you ~foo');
  assert.equal(nodes.length, 1);
  assert.equal(nodes[0].type, 'text');
  assert.equal(nodes[0].value, '~hey you ~foo');
}

{
  // single-word strikethrough still works with the space restriction.
  const nodes = firstInlines('a ~word~ b');
  assert.equal(nodes[1].type, 'del');
  assert.equal(nodes[1].children[0].value, 'word');
}

console.log('reedMarkdown: all checks passed');
