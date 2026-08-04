/** Normalize a path/param tag the same way as publish extractTags. */
export function normalizePipeTag(raw: string): string {
  let t = raw.trim();
  try {
    t = decodeURIComponent(t);
  } catch {
    // keep raw
  }
  t = t.trim();
  if (t.startsWith('#')) t = t.slice(1);
  return t.toLowerCase();
}
