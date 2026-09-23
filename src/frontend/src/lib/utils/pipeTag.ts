/** Normalize a path/param tag the same way as publish extractTags. Rejects
 * (returns '') a tag that still has whitespace after trimming — a tag is a
 * #\S+ run, so anything with an internal space isn't a valid tag at all. */
export function normalizePipeTag(raw: string): string {
  let t = raw.trim();
  try {
    t = decodeURIComponent(t);
  } catch {
    // keep raw
  }
  t = t.trim();
  if (t.startsWith('#')) t = t.slice(1);
  if (/\s/.test(t)) return '';
  return t.toLowerCase();
}
