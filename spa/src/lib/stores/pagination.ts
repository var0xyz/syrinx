import { writable, type Readable } from 'svelte/store';

/**
 * Shared pagination state. Pages already arrive trimmed from the server
 * (limit+1/hasMore trimming is a backend concern — see pagination.go), so
 * this store's job is state shape and load/loadMore orchestration only,
 * not any client-side trimming.
 */
export interface PaginationState<T> {
  rows: T[];
  loading: boolean;
  loadingMore: boolean;
  hasMore: boolean;
  error: string;
}

export interface PaginationOptions<T, Page> {
  /** 'append' (tail, the common case) or 'prepend' (head). */
  direction?: 'append' | 'prepend';
  /** Fetches one page. cursor is undefined for the first page. */
  fetchPage: (cursor: string | undefined) => Promise<Page>;
  /** Pulls the raw item array out of the page — may resolve extra data
   * (e.g. usernames) per row, hence async. */
  getItems: (page: Page) => T[] | Promise<T[]>;
  getHasMore: (page: Page) => boolean;
  /** Derives the cursor for the next page from the page just loaded and
   * the rows accumulated so far (covers both "last item's own timestamp
   * field" and "server-issued nextCursor" conventions). */
  getNextCursor: (page: Page, rowsSoFar: T[]) => string | undefined;
}

export interface PaginationStore<T> extends Readable<PaginationState<T>> {
  /** Resets state and loads the first page. */
  load(): Promise<void>;
  /** Loads the next page and appends/prepends per `direction`. No-op if
   * there's no more data or a load is already in flight. */
  loadMore(): Promise<void>;
  /** Resets state and re-fetches page 1 — for websocket-triggered refresh. */
  reload(): Promise<void>;
  /** Escape hatch for live websocket-driven inserts/removes/patches. The
   * store never imports serverConnection or knows what a "live item" is;
   * callers splice into `rows` directly through this. */
  update(updater: (state: PaginationState<T>) => PaginationState<T>): void;
}

// loading starts true: both adopting components render eagerly (before
// load() resolves its first await) and expect a "Loading…" first frame,
// matching their original hand-rolled `let loading = true`.
const initialState = <T,>(): PaginationState<T> => ({
  rows: [],
  loading: true,
  loadingMore: false,
  hasMore: false,
  error: '',
});

export function createPaginationStore<T, Page>(
  opts: PaginationOptions<T, Page>,
): PaginationStore<T> {
  const direction = opts.direction ?? 'append';
  const { subscribe, update, set } = writable<PaginationState<T>>(initialState());

  let cursor: string | undefined;
  let rows: T[] = [];
  subscribe((state) => {
    rows = state.rows;
  });

  async function fetchAndApply(isFirstPage: boolean) {
    const page = await opts.fetchPage(isFirstPage ? undefined : cursor);
    const items = await opts.getItems(page);
    const combined = isFirstPage
      ? items
      : direction === 'prepend'
        ? [...items, ...rows]
        : [...rows, ...items];

    cursor = opts.getNextCursor(page, combined);
    const hasMore = opts.getHasMore(page);
    update((state) => ({ ...state, rows: combined, hasMore, error: '' }));
  }

  async function load() {
    cursor = undefined;
    set(initialState());
    try {
      await fetchAndApply(true);
    } catch (err) {
      console.error('Failed to load list:', err);
      update((state) => ({ ...state, error: 'Unable to load this list right now.' }));
    } finally {
      update((state) => ({ ...state, loading: false }));
    }
  }

  async function loadMore() {
    let shouldLoad = false;
    update((state) => {
      shouldLoad = state.hasMore && !state.loadingMore;
      return shouldLoad ? { ...state, loadingMore: true } : state;
    });
    if (!shouldLoad) return;

    try {
      await fetchAndApply(false);
    } catch (err) {
      console.error('Failed to load more:', err);
    } finally {
      update((state) => ({ ...state, loadingMore: false }));
    }
  }

  async function reload() {
    await load();
  }

  return { subscribe, load, loadMore, reload, update };
}
