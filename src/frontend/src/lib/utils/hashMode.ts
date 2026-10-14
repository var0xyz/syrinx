import { goto } from '$app/navigation';

/** Mirrors a page's pill choice into the URL hash, so a reload keeps it. */
export function keepInHash(url: URL, hash: string): void {
  if (url.hash === hash) return;
  void goto(`${url.pathname}${hash}`, { replaceState: true, noScroll: true, keepFocus: true });
}
