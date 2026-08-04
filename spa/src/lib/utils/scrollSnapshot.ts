import { tick } from 'svelte';

/**
 * Apply window scroll after the next paint (DOM height must be ready).
 * Returns a promise that resolves after scrollTo is called.
 */
export async function restoreWindowScroll(y: number | null | undefined): Promise<void> {
  if (typeof y !== 'number' || y < 0 || typeof window === 'undefined') return;
  await tick();
  await new Promise<void>((resolve) => {
    requestAnimationFrame(() => {
      window.scrollTo(0, y);
      resolve();
    });
  });
}

/** Capture current window scroll for a SvelteKit page snapshot. */
export function captureWindowScroll(): number {
  return typeof window !== 'undefined' ? window.scrollY : 0;
}
