import { reedRequestsRepository } from '$lib/repositories/reedRequests';
import { reedsService } from '$lib/repositories/reeds';
import { serverConnection } from './serverConnection';
import { refForReed } from '$lib/utils/reedRef';

const TICK_MS = 1000;

let timer: ReturnType<typeof setInterval> | null = null;
let ticking = false;

export function startReedRequestDrainer(): void {
  if (timer) return;
  void drainTick();
  timer = setInterval(() => void drainTick(), TICK_MS);
}

export function stopReedRequestDrainer(): void {
  if (timer) {
    clearInterval(timer);
    timer = null;
  }
}

export function clearReedRequestDispatched(): void {
  serverConnection.clearDispatchedReedRequests();
}

async function drainTick(): Promise<void> {
  if (ticking || !serverConnection.isConnected()) return;
  ticking = true;
  try {
    const pending = await reedRequestsRepository.getAllPending();
    for (const record of pending) {
      if (serverConnection.isReedRequestDispatched(record.requestId)) continue;
      const held = await reedsService.getReed(refForReed(record.authorId, record.reedId));
      if (held) {
        await reedRequestsRepository.delete(record.requestId);
        continue;
      }
      serverConnection.dispatchReedRequest(record);
      return;
    }
  } finally {
    ticking = false;
  }
}
