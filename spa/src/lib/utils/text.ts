/**
 * Mirror of `trimInvisibleChars` in utils.go. Signup (and anything else that
 * signs user-supplied text the server re-derives payload bytes from) MUST
 * apply this before signing — the server strips the same characters before
 * rebuilding the signed payload, so any client/server drift here breaks
 * signature verification for users whose input contains what they signed
 * but the server didn't (e.g. mobile-keyboard leading/trailing whitespace).
 *
 * Go's `unicode.IsPrint` excludes control (Cc, Cf, Cs, Co, Cn) and
 * separator (Zs, Zl, Zp) categories except the literal ASCII space, which
 * is allowed through explicitly — replicated here with the same two-pass
 * shape (outer trim, then per-character filter, then trim again).
 */
export function trimInvisibleChars(s: string): string {
  const trimmed = s.trim();
  const filtered = Array.from(trimmed)
    .filter((ch) => ch === ' ' || !/\p{C}|\p{Z}/u.test(ch))
    .join('');
  return filtered.trim();
}
