import { writable } from 'svelte/store';

/** Which nav section the (single, root-layout) BottomToolbar highlights.
 * Pages set this instead of each rendering their own toolbar instance, so
 * the toolbar's DOM (and its scroll position) survives navigation. */
export const currentToolbarPage = writable('');

/** Count of currently-mounted <BottomToolbar currentPage="..."> usages.
 * Not every route renders one (logged-out, error page pre-auth-check, ...),
 * and during client-side navigation the incoming page's instance can mount
 * before the outgoing one unmounts — a plain boolean set on destroy would
 * risk a flicker if the order were ever reversed, so this counts instead. */
export const toolbarUsers = writable(0);
