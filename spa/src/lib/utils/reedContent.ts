/**
 * Visible (markdown-stripped) and raw content caps for reeds.
 * Raw limit blocks markdown padding abuse; visible limit is the UX budget.
 */
export const MAX_REED_VISIBLE_CHARS = 140;
export const MAX_REED_RAW_CHARS = 1400;

/**
 * Count characters in markdown text, stripping formatting syntax.
 * Supports: bold (*text*), italic (_text_), strikethrough (~text~),
 * inline code (`text`), links [text](url), code fences, hashtag #.
 */
export function countMarkdownCharacters(text: string): number {
  if (!text) return 0;

  let result = text;

  result = result.replace(/\[([^\]]+)\]\([^)]+\)/g, '$1');
  result = result.replace(/```[^\n]*\n?([\s\S]*?)\n```/g, '$1');
  result = result.replace(/`([^`]+)`/g, '$1');
  result = result.replace(/~([^~]+)~/g, '$1');
  result = result.replace(/_([^_]+)_/g, '$1');
  result = result.replace(/\*([^*]+)\*/g, '$1');
  result = result.replace(/(^|\s)#(?=\S)/g, '$1');

  return result.length;
}

/** True when content is within both the raw and markdown-visible caps. */
export function reedContentWithinLimits(content: string | null | undefined): boolean {
  const text = content ?? '';
  if (text.length > MAX_REED_RAW_CHARS) return false;
  if (countMarkdownCharacters(text) > MAX_REED_VISIBLE_CHARS) return false;
  return true;
}

/**
 * Strip markdown formatting from text (display helper).
 */
export function stripMarkdown(text: string): string {
  if (!text) return '';

  let result = text;
  result = result.replace(/\[([^\]]+)\]\([^)]+\)/g, '$1');
  result = result.replace(/`([^`]+)`/g, '$1');
  result = result.replace(/~([^~]+)~/g, '$1');
  result = result.replace(/_([^_]+)_/g, '$1');
  result = result.replace(/\*([^*]+)\*/g, '$1');
  return result;
}
