import { writable } from 'svelte/store';
import { apiService } from './api';
import type * as api from '$lib/types/api';
import { chainOrder, ledgerRecord, ledgerRepository, type LedgerRecord } from '$lib/repositories/ledger';

const TAMPERED_KEY = 'ledgerTampered';

function readTampered(): boolean {
  try {
    return localStorage.getItem(TAMPERED_KEY) === '1';
  } catch {
    return false;
  }
}

/** Bumped whenever an entry is stored. */
export const ledgerChanged = writable(0);

/** Set once the server's ledger disagreed with the copy held here. */
export const ledgerTampered = writable(readTampered());

function markTampered(reason: string, id: number): void {
  console.error('[ledger] the server rewrote its ledger:', reason, id);
  try {
    localStorage.setItem(TAMPERED_KEY, '1');
  } catch {
    // The banner still shows for this session.
  }
  ledgerTampered.set(true);
}

function sameEntry(a: LedgerRecord, b: LedgerRecord): boolean {
  return (
    a.prevID === b.prevID &&
    a.userID === b.userID &&
    a.message === b.message &&
    a.content === b.content &&
    a.targets.join('\n') === b.targets.join('\n') &&
    a.serverSignature.signedAt === b.serverSignature.signedAt &&
    a.serverSignature.armor === b.serverSignature.armor
  );
}

/** The held entry nothing follows, null while nothing is held. */
async function lastHeld(): Promise<LedgerRecord | null> {
  const chain = chainOrder(await ledgerRepository.getAll());
  return chain.at(-1) ?? null;
}

/** Stores entry when it chains after the last one held, and compares it
 * with the copy held otherwise. False when it doesn't fit either way. */
async function accept(entry: api.LedgerEntry): Promise<boolean> {
  const record = ledgerRecord(entry);
  const held = await ledgerRepository.get(record.id);
  if (held) {
    if (!sameEntry(held, record)) markTampered('entry differs', record.id);
    return true;
  }
  const last = await lastHeld();
  if (record.prevID !== last?.id) return false;
  try {
    await ledgerRepository.put(record);
  } catch (err) {
    console.error('[ledger] entry failed verification', record.id, err);
    return false;
  }
  ledgerChanged.update((n) => n + 1);
  return true;
}

let syncing: Promise<void> | null = null;

/** Walks the server's ledger from the head: stores what's new, and checks
 * every entry already held against the server's copy. */
export function syncLedger(): Promise<void> {
  if (!syncing) {
    syncing = walk().finally(() => {
      syncing = null;
    });
  }
  return syncing;
}

async function walk(): Promise<void> {
  const seen = new Set<number>();
  try {
    let page = await apiService.getLedger();
    if (!page.head) return;
    let prev: api.LedgerEntry = page.head;
    if (prev.prevId !== undefined || !(await accept(prev))) return;
    seen.add(Number(prev.id));
    while (page.tail.length > 0) {
      let link = prev.id;
      for (const entry of page.tail) {
        if (entry.prevId !== link) {
          console.error('[ledger] refusing a page that doesn’t chain', entry.id);
          return;
        }
        link = entry.id;
      }
      for (const entry of page.tail) {
        if (!(await accept(entry))) return;
        seen.add(Number(entry.id));
      }
      prev = page.tail[page.tail.length - 1];
      page = await apiService.getLedger(Number(prev.id));
    }
  } catch (err) {
    const status = (err as { status?: number })?.status;
    if (status !== 401 && status !== 403) console.error('[ledger] sync failed', err);
    return;
  }
  for (const record of await ledgerRepository.getAll()) {
    if (!seen.has(record.id)) markTampered('entry missing from the server', record.id);
  }
}

/** A LEDGER_ENTRY pushed over the WebSocket. One that doesn't follow the
 * last entry held means some were missed, so the client catches up. */
export async function receiveLedgerEntry(entry: api.LedgerEntry): Promise<void> {
  if (!(await accept(entry))) await syncLedger();
}
