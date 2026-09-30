import { writable } from 'svelte/store';

/**
 * Bumped whenever the local vouch set changes, so open views re-read it.
 * The value is a counter, not the data: readers own their own query.
 */
export const vouchesChanged = writable(0);

export function notifyVouchesChanged(): void {
  vouchesChanged.update((n) => n + 1);
}
