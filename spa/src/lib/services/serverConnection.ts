import { requestSigner } from './request-signer';
import { authService } from './auth';
import { ensureDeviceId } from './deviceId';
import {
  computeReedRequestId,
  reedRequestsRepository,
  type ReedRequestRecord,
} from '$lib/repositories/reedRequests';
import { reedsService } from '$lib/repositories/reeds';
import { startReedRequestDrainer } from './reedRequestDrainer';
import { refForReed } from '$lib/utils/identityRef';

export type ServerEventHandler = (data: any) => void;

type PendingRequest = { resolve: (data: any) => void; reject: (err: any) => void };

export enum ServerEvent {
  AccountRemoved       = 'ACCOUNT_REMOVED',
  BroadcastReed        = 'BROADCAST_REED',
  DataResponse         = 'DATA_RESPONSE',
  FollowReed           = 'FOLLOW_REED',
  InvalidRequestIdError = 'INVALID_REQUEST_ID_ERROR',
  PipeReed             = 'PIPE_REED',
  PublishReadyAck      = 'PUBLISH_READY_ACK',
  ReedCoverage         = 'REED_COVERAGE',
  ReedEchoes           = 'REED_ECHOES',
  ReedLikes            = 'REED_LIKES',
  ReedNotFound         = 'REED_NOT_FOUND',
  ReedNotHeld          = 'REED_NOT_HELD',
  ReedNotification     = 'reed_notification',
  ReedRemoved          = 'REED_REMOVED',
  ReedReplies          = 'REED_REPLIES',
  ReedReply            = 'REED_REPLY',
  ReedStats            = 'REED_STATS',
  RelayRequest         = 'RELAY_REQUEST',
  RequestAck           = 'REQUEST_ACK',
  RipplePosted         = 'RIPPLE_POSTED',
  RippleUpdated        = 'RIPPLE_UPDATED',
  Sigterm              = 'SIGTERM',
}

class ServerConnection {
  private ws: WebSocket | null = null;
  private connectingPromise: Promise<void> | null = null;
  private eventHandlers: Map<string, ServerEventHandler[]> = new Map();
  private pendingRequests: Map<string, PendingRequest> = new Map();
  private pendingReedPromises: Map<string, Promise<any>> = new Map();
  private dispatchedReedRequests = new Set<string>();
  // Subscriptions live server-side per-connection: a fresh socket after a
  // network drop starts with none of them, so we replay whatever was
  // active once the new connection opens. Reed/profile/pipe are mutually
  // exclusive in practice (each is scoped to its own route, and callers
  // always unsubscribe on unmount before subscribing elsewhere), so one
  // slot covers all three. Broadcast is independent and can coexist.
  private activeSubscription:
    | { kind: 'reed'; authorId: string; reedId: string }
    | { kind: 'profile'; userId: string }
    | { kind: 'pipe'; tag: string }
    | null = null;
  private broadcastSubscribed = false;
  /** Set while retrying after a server-initiated SIGTERM shutdown notice,
   * so a concurrent connect() elsewhere doesn't leave a duplicate timer
   * running. Cleared once a retry succeeds (or something else reconnects). */
  private sigtermRetryTimer: ReturnType<typeof setTimeout> | null = null;

  private cancelSigtermRetry(): void {
    if (this.sigtermRetryTimer != null) {
      clearTimeout(this.sigtermRetryTimer);
      this.sigtermRetryTimer = null;
    }
  }

  /** SIGTERM: the server told us it's shutting down before the socket goes
   * dead. Drop the connection now and retry every 3s until a new process
   * is up, rather than waiting on navigator.onLine (which a same-network
   * server restart never flips). */
  private handleSigterm(): void {
    console.log('ServerConnection: server sent SIGTERM, reconnecting…');
    this.cancelSigtermRetry();
    if (this.ws) {
      const prev = this.ws;
      this.ws = null;
      prev.onclose = null;
      prev.close();
    }
    const retry = () => {
      this.connect()
        .then(() => {
          if (this.isConnected()) {
            this.cancelSigtermRetry();
          } else {
            this.sigtermRetryTimer = setTimeout(retry, 3000);
          }
        })
        .catch(() => {
          this.sigtermRetryTimer = setTimeout(retry, 3000);
        });
    };
    this.sigtermRetryTimer = setTimeout(retry, 3000);
  }

  /**
   * Force a fresh connection even if the current socket still reports
   * OPEN. Browsers don't reliably fire `close`/`error` on a real network
   * drop — the OS may not notice a dead route for minutes — so after an
   * offline→online transition we can't trust readyState and must not
   * short-circuit like `connect()` does.
   */
  async reconnect(): Promise<void> {
    if (this.ws && this.ws.readyState !== WebSocket.CLOSED) {
      const prev = this.ws;
      this.ws = null;
      prev.onclose = null;
      prev.close();
    }
    this.connectingPromise = null;
    return this.connect();
  }

  async connect(): Promise<void> {
    if (this.ws?.readyState === WebSocket.OPEN) return;
    if (this.connectingPromise) return this.connectingPromise;
    // Another caller may have created the socket but not finished open yet.
    if (this.ws?.readyState === WebSocket.CONNECTING) {
      this.connectingPromise = new Promise<void>((resolve, reject) => {
        const ws = this.ws!;
        const onOpen = () => { cleanup(); resolve(); };
        const onError = () => { cleanup(); reject(new Error('connection failed')); };
        const cleanup = () => {
          ws.removeEventListener('open', onOpen);
          ws.removeEventListener('error', onError);
        };
        ws.addEventListener('open', onOpen);
        ws.addEventListener('error', onError);
      }).finally(() => {
        this.connectingPromise = null;
      });
      return this.connectingPromise;
    }

    this.connectingPromise = this.doConnect().finally(() => {
      this.connectingPromise = null;
    });
    return this.connectingPromise;
  }

  private async doConnect(): Promise<void> {
    try {
      const user = await authService.getCurrentUser();
      if (!user) {
        console.log('ServerConnection: no authenticated user, skipping connection');
        return;
      }

      if (!requestSigner.isInitialized()) {
        const fingerprint = authService.getActiveKeyFingerprint();
        const passphrase = authService.getPassphrase();

        if (!fingerprint || !passphrase) {
          console.log('ServerConnection: request signer not ready, skipping connection');
          return;
        }

        try {
          await requestSigner.initializeWorker(fingerprint, passphrase);
        } catch (error) {
          console.error('ServerConnection: failed to initialize request signer:', error);
          return;
        }
      }

      // Drop any half-open / stale socket before opening a new one so the
      // server does not accumulate zombie connections for this user.
      if (this.ws && this.ws.readyState !== WebSocket.CLOSED) {
        const prev = this.ws;
        this.ws = null;
        prev.onclose = null;
        prev.close();
      }

      const timestamp = Math.floor(Date.now() / 1000).toString();
      const signature = await requestSigner.sign(timestamp);
      const canonicalFingerprint = authService.getActiveKeyFingerprint()!;

      const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
      const url = new URL(`${protocol}//${window.location.host}/ws/`);
      url.searchParams.set('publicKeyId', canonicalFingerprint);
      url.searchParams.set('timestamp', timestamp);
      url.searchParams.set('signature', signature);
      url.searchParams.set('deviceId', ensureDeviceId());

      console.log('ServerConnection: connecting...');
      this.ws = new WebSocket(url.toString());

      this.ws.onmessage = (event) => {
        try {
          const message = JSON.parse(event.data);
          console.log('ServerConnection: message received:', message.type);

          if (message.type === ServerEvent.Sigterm) {
            this.handleSigterm();
            return;
          }

          if (message.type === ServerEvent.RequestAck) {
            void reedRequestsRepository.get(message.data.request_id).then((record) => {
              if (!record) {
                console.warn(
                  `ServerConnection: ${ServerEvent.RequestAck} for unknown request, discarding:`,
                  message.data.request_id
                );
              }
            });
          } else if (message.type === 'DATA_RESPONSE') {
            const requestId = message.data.request_id;
            this.dispatchedReedRequests.delete(requestId);
            const pending = this.pendingRequests.get(requestId);
            if (pending) {
              pending.resolve(message.data.data);
              this.pendingRequests.delete(requestId);
            }
          } else if (message.type === ServerEvent.ReedNotFound || message.type === ServerEvent.ReedNotHeld) {
            const requestId = message.data.request_id;
            this.dispatchedReedRequests.delete(requestId);
            void reedRequestsRepository.delete(requestId);
            const pending = this.pendingRequests.get(requestId);
            if (pending) {
              pending.reject(new Error(message.type === ServerEvent.ReedNotHeld ? 'reed_not_held' : 'reed_not_found'));
              this.pendingRequests.delete(requestId);
            }
          } else if (message.type === ServerEvent.InvalidRequestIdError) {
            // The server rejected a request_id we minted (malformed, or
            // its identity doesn't match this connection) — the server
            // never created any pending state for it, so just discard our
            // own local record rather than retry it.
            const requestId = message.data.request_id;
            console.warn('ServerConnection: request_id rejected by server, discarding:', requestId);
            this.dispatchedReedRequests.delete(requestId);
            void reedRequestsRepository.delete(requestId);
            const pending = this.pendingRequests.get(requestId);
            if (pending) {
              pending.reject(new Error('invalid_request_id'));
              this.pendingRequests.delete(requestId);
            }
          }

          this.emit(message.type, message.data ?? message);
        } catch {
          console.warn('ServerConnection: received non-JSON message, ignoring');
        }
      };

      this.ws.onclose = (event) => {
        console.log('ServerConnection: connection closed', event.code, event.reason);
        if (this.ws === event.target) {
          this.ws = null;
          this.dispatchedReedRequests.clear();
          sessionStorage.removeItem('syncRequestId');
        }
      };

      await new Promise<void>((resolve, reject) => {
        const timeout = setTimeout(() => reject(new Error('connection timeout')), 10000);

        this.ws!.onopen = () => {
          clearTimeout(timeout);
          console.log('ServerConnection: connected');
          this.resubscribeAll();
          resolve();
        };

        this.ws!.onerror = () => {
          clearTimeout(timeout);
          reject(new Error('connection failed'));
        };
      });
    } catch (error) {
      console.error('ServerConnection: connect failed:', error);
      if (this.ws) {
        const failed = this.ws;
        this.ws = null;
        failed.onclose = null;
        failed.close();
      }
    }
  }

  disconnect(): void {
    this.cancelSigtermRetry();
    if (this.ws) {
      this.ws.close();
      this.ws = null;
    }
    this.dispatchedReedRequests.clear();
    this.activeSubscription = null;
    this.broadcastSubscribed = false;
    console.log('ServerConnection: disconnected');
  }

  isConnected(): boolean {
    return this.ws?.readyState === WebSocket.OPEN;
  }

  isReedRequestDispatched(requestId: string): boolean {
    return this.dispatchedReedRequests.has(requestId);
  }

  clearDispatchedReedRequests(): void {
    this.dispatchedReedRequests.clear();
  }

  dispatchReedRequest(record: ReedRequestRecord): void {
    if (this.dispatchedReedRequests.has(record.requestId)) return;
    this.dispatchedReedRequests.add(record.requestId);
    this.send({
      type: 'REQUEST_REED',
      data: {
        request_id: record.requestId,
        reed_id: refForReed(record.authorId, record.reedId),
      },
    });
  }

  on(event: ServerEvent, handler: ServerEventHandler): void {
    const key = event as unknown as string;
    if (!this.eventHandlers.has(key)) {
      this.eventHandlers.set(key, []);
    }
    this.eventHandlers.get(key)!.push(handler);
  }

  off(event: ServerEvent, handler: ServerEventHandler): void {
    const key = event as unknown as string;
    const handlers = this.eventHandlers.get(key);
    if (handlers) {
      const index = handlers.indexOf(handler);
      if (index > -1) handlers.splice(index, 1);
    }
  }

  async requestReedContent(reedId: string, authorId: string, serverId: string): Promise<any> {
    const requesterId = localStorage.getItem('userId') ?? '';
    const requestId = computeReedRequestId(requesterId, serverId, authorId, reedId);
    const held = await reedsService.getReed(refForReed(authorId, reedId));
    if (held) return held;

    await reedRequestsRepository.enqueue({ requestId, serverId, authorId, reedId });

    let promise = this.pendingReedPromises.get(requestId);
    if (!promise) {
      promise = new Promise<any>((resolve, reject) => {
        this.pendingRequests.set(requestId, { resolve, reject });
      }).finally(() => {
        this.pendingReedPromises.delete(requestId);
        this.pendingRequests.delete(requestId);
      });
      this.pendingReedPromises.set(requestId, promise);
    }

    startReedRequestDrainer();
    return promise;
  }

  sendRelayResponse(eventId: string, data: any): void {
    this.send({ type: 'RELAY_RESPONSE', data: { event_id: eventId, data } });
  }

  sendRelayMiss(eventId: string): void {
    this.send({ type: 'RELAY_MISS', data: { event_id: eventId } });
  }

  sendDataAck(eventId: string): void {
    this.send({ type: 'DATA_ACK', data: { event_id: eventId } });
  }

  sendDataInvalid(eventId: string): void {
    this.send({ type: 'DATA_INVALID', data: { event_id: eventId } });
  }

  /** Reports a failed key fetch needed to verify content received over this
   * (already-authenticated) connection — an anomaly, not a routine cache miss. */
  sendKeyFetchError(userId: string, fingerprint: string): void {
    this.send({ type: 'KEY_FETCH_ERROR', data: { user_id: userId, fingerprint } });
  }

  /** Reports content whose timestamp is at or after its signing key's revocation. */
  sendRevokedKeyUsed(userId: string, fingerprint: string): void {
    this.send({ type: 'REVOKED_KEY_USED', data: { user_id: userId, fingerprint } });
  }

  async publishReady(reedId: string, options?: { broadcast?: boolean }): Promise<void> {
    await this.connect();
    this.send({
      type: 'PUBLISH_READY',
      data: {
        reed_id: reedId,
        broadcast: options?.broadcast !== false,
      },
    });
  }

  syncRequest(): void {
    const requesterId = localStorage.getItem('userId') ?? '';
    const requestId = `${requesterId}/${crypto.randomUUID()}`;
    sessionStorage.setItem('syncRequestId', requestId);
    this.send({ type: 'SYNC_REQUEST', data: { request_id: requestId } });
    startReedRequestDrainer();
  }

  async subscribeProfile(userId: string): Promise<void> {
    await this.connect();
    this.activeSubscription = { kind: 'profile', userId };
    this.send({ type: 'SUBSCRIBE_PROFILE', data: { user_id: userId } });
  }

  unsubscribeProfile(userId: string): void {
    if (this.activeSubscription?.kind === 'profile' && this.activeSubscription.userId === userId) {
      this.activeSubscription = null;
    }
    this.send({ type: 'UNSUBSCRIBE_PROFILE', data: { user_id: userId } });
  }

  async subscribeReed(authorId: string, reedId: string): Promise<boolean> {
    await this.connect();
    if (!this.isConnected()) {
      return false;
    }
    this.activeSubscription = { kind: 'reed', authorId, reedId };
    this.send({ type: 'SUBSCRIBE_REED', userID: authorId, reedID: reedId });
    return true;
  }

  unsubscribeReed(authorId: string, reedId: string): void {
    if (
      this.activeSubscription?.kind === 'reed' &&
      this.activeSubscription.authorId === authorId &&
      this.activeSubscription.reedId === reedId
    ) {
      this.activeSubscription = null;
    }
    this.send({ type: 'UNSUBSCRIBE_REED', userID: authorId, reedID: reedId });
  }

  subscribeToBroadcast(): void {
    this.broadcastSubscribed = true;
    this.send({ type: 'SUBSCRIBE_BROADCAST' });
  }

  unsubscribeFromBroadcast(): void {
    this.broadcastSubscribed = false;
    this.send({ type: 'UNSUBSCRIBE_BROADCAST' });
  }

  async subscribePipe(tag: string): Promise<void> {
    const normalized = tag.trim().replace(/^#/, '').toLowerCase();
    if (!normalized) return;
    await this.connect();
    this.activeSubscription = { kind: 'pipe', tag: normalized };
    this.send({ type: 'SUBSCRIBE_PIPE', data: { tag: normalized } });
  }

  unsubscribePipe(tag: string): void {
    const normalized = tag.trim().replace(/^#/, '').toLowerCase();
    if (!normalized) return;
    if (this.activeSubscription?.kind === 'pipe' && this.activeSubscription.tag === normalized) {
      this.activeSubscription = null;
    }
    this.send({ type: 'UNSUBSCRIBE_PIPE', data: { tag: normalized } });
  }

  /** Replays whatever subscription was active — server-side state doesn't survive a reconnect. */
  private resubscribeAll(): void {
    switch (this.activeSubscription?.kind) {
      case 'reed':
        this.send({
          type: 'SUBSCRIBE_REED',
          userID: this.activeSubscription.authorId,
          reedID: this.activeSubscription.reedId,
        });
        break;
      case 'profile':
        this.send({ type: 'SUBSCRIBE_PROFILE', data: { user_id: this.activeSubscription.userId } });
        break;
      case 'pipe':
        this.send({ type: 'SUBSCRIBE_PIPE', data: { tag: this.activeSubscription.tag } });
        break;
    }
    if (this.broadcastSubscribed) {
      this.send({ type: 'SUBSCRIBE_BROADCAST' });
    }
  }

  private send(message: { type: string; data?: any; userID?: string; reedID?: string }): void {
    if (!this.isConnected()) {
      console.warn('ServerConnection: cannot send, not connected');
      return;
    }
    this.ws!.send(JSON.stringify(message));
  }

  private emit(eventType: string, data: any): void {
    const handlers = this.eventHandlers.get(eventType);
    if (handlers) {
      handlers.forEach(handler => handler(data));
    } else {
      console.log('ServerConnection: no handler for event:', eventType);
    }
  }
}

export const serverConnection = new ServerConnection();
