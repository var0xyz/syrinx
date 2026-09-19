/**
 * Visible (markdown-stripped) and raw content caps for reeds.
 * Raw limit blocks markdown padding abuse; visible limit is the UX budget.
 */
export const MAX_REED_VISIBLE_CHARS = 140;
export const MAX_REED_RAW_CHARS = 1400;

/**
 * Mentions (~userID@serverID) render as "@username" — an unknown length
 * until resolved — so the visible-character budget counts only the userID
 * segment (matches the picker's/backend's ID alphabet: alphanumeric, any
 * length ≥ 1). Must run before the ~text~ strikethrough strip below, since
 * that pattern would otherwise consume the whole token as one group.
 */
function stripMentionsToUserID(text: string): string {
  return text.replace(/~([a-zA-Z0-9]+)@[a-zA-Z0-9]+/g, '$1');
}

/**
 * Count characters in markdown text, stripping formatting syntax.
 * Supports: bold (*text*), italic (_text_), strikethrough (~text~),
 * inline code (`text`), links [text](url), code fences, hashtag #,
 * mentions (~userID@serverID, counted as userID length only).
 */
export function countMarkdownCharacters(text: string): number {
  if (!text) return 0;

  let result = text;

  result = stripMentionsToUserID(result);
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
 * Strip markdown formatting from text (display helper). Mentions fall back
 * to "@userID" here (no username resolution available outside a rendering
 * context) — imperfect but readable, unlike the bare ~userID@serverID token.
 */
export function stripMarkdown(text: string): string {
  if (!text) return '';

  let result = text;
  result = result.replace(/~([a-zA-Z0-9]+)@[a-zA-Z0-9]+/g, '@$1');
  result = result.replace(/\[([^\]]+)\]\([^)]+\)/g, '$1');
  result = result.replace(/`([^`]+)`/g, '$1');
  result = result.replace(/~([^~]+)~/g, '$1');
  result = result.replace(/_([^_]+)_/g, '$1');
  result = result.replace(/\*([^*]+)\*/g, '$1');
  return result;
}
