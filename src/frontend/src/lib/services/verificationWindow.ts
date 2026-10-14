import { apiService, type ApiError } from './api';

/** How often an open verification code pings; the server drops a window
 * after a minute without one. */
const PING_INTERVAL_MS = 10_000;

/**
 * Tells the server the viewer is showing their verification code, so a vouch
 * for them is accepted only meanwhile. Returns the function that closes it.
 * A window the server dropped (it expired while the app slept) is reopened.
 */
export function openVerificationWindow(onError: (message: string) => void): () => void {
  let id = crypto.randomUUID();
  let closed = false;

  const open = async (): Promise<void> => {
    try {
      await apiService.openVerificationWindow(id);
    } catch (err) {
      if (!closed) onError(err instanceof Error ? err.message : 'Could not open the verification window.');
    }
  };

  const ping = async (): Promise<void> => {
    try {
      await apiService.pingVerificationWindow(id);
    } catch (err) {
      if (closed || (err as ApiError).status !== 404) return;
      id = crypto.randomUUID();
      await open();
    }
  };

  void open();
  const timer = setInterval(() => void ping(), PING_INTERVAL_MS);

  return () => {
    if (closed) return;
    closed = true;
    clearInterval(timer);
    apiService.closeVerificationWindow(id).catch(() => {
      // The window expires on its own a minute after the last ping.
    });
  };
}
