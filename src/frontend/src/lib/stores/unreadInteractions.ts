import { writable } from 'svelte/store';

const STORAGE_KEY = 'unreadInteractions';

type UnreadState = { replies: boolean; mentions: boolean };

function load(): UnreadState {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return { replies: false, mentions: false };
    const parsed = JSON.parse(raw);
    return { replies: !!parsed.replies, mentions: !!parsed.mentions };
  } catch {
    return { replies: false, mentions: false };
  }
}

function save(state: UnreadState): void {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(state));
  } catch {
    // best-effort persistence only
  }
}

/** Whether a red dot should show for each Interactions tab — set on live
 * WS delivery (reed_reply / mention), cleared when the user visits the
 * corresponding tab. Persisted so the dot survives a reload. */
export const unreadInteractions = writable<UnreadState>(load());

export function markUnread(kind: keyof UnreadState): void {
  unreadInteractions.update((state) => {
    if (state[kind]) return state;
    const next = { ...state, [kind]: true };
    save(next);
    return next;
  });
}

export function clearUnread(kind: keyof UnreadState): void {
  unreadInteractions.update((state) => {
    if (!state[kind]) return state;
    const next = { ...state, [kind]: false };
    save(next);
    return next;
  });
}
