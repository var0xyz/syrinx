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

export type ServerEventHandler = (data: any) => void;

type PendingRequest = { resolve: (data: any) => void; reject: (err: any) => void };

export enum ServerEvent {
  ReedNotification = 'reed_notification',
  RelayRequest     = 'RELAY_REQUEST',
  RequestAck       = 'REQUEST_ACK',
  DataResponse     = 'DATA_RESPONSE',
  BroadcastReed    = 'BROADCAST_REED',
  PipeReed         = 'PIPE_REED',
  FollowReed       = 'FOLLOW_REED',
  ReedNotFound     = 'REED_NOT_FOUND',
  ReedNotHeld      = 'REED_NOT_HELD',
  ReedRemoved      = 'REED_REMOVED',
  AccountRemoved   = 'ACCOUNT_REMOVED',
  PublishReadyAck  = 'PUBLISH_READY_ACK',
  ReedStats        = 'REED_STATS',
  ReedEchoes       = 'REED_ECHOES',
  ReedReplies      = 'REED_REPLIES',
  ReedCoverage     = 'REED_COVERAGE',
}

class ServerConnection {
  private ws: WebSocket | null = null;
  private connectingPromise: Promise<void> | null = null;
  private eventHandlers: Map<string, ServerEventHandler[]> = new Map();
  private pendingRequests: Map<string, PendingRequest> = new Map();
  private pendingReedPromises: Map<string, Promise<any>> = new Map();
  private dispatchedReedRequests = new Set<string>();

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
      const fingerprint = authService.getActiveKeyFingerprint()!;

      const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
      const url = new URL(`${protocol}//${window.location.host}/ws/`);
      url.searchParams.set('userID', user.id);
      url.searchParams.set('fingerprint', fingerprint);
      url.searchParams.set('timestamp', timestamp);
      url.searchParams.set('signature', signature);
      url.searchParams.set('deviceId', ensureDeviceId());

      console.log('ServerConnection: connecting...');
      this.ws = new WebSocket(url.toString());

      this.ws.onmessage = (event) => {
        try {
          const message = JSON.parse(event.data);
          console.log('ServerConnection: message received:', message.type);

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
    if (this.ws) {
      this.ws.close();
      this.ws = null;
    }
    this.dispatchedReedRequests.clear();
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
        reed_id: record.reedId,
        author_id: record.authorId,
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
    const requestId = computeReedRequestId(serverId, authorId, reedId);
    const held = await reedsService.getReed(authorId, reedId);
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
    const requestId = crypto.randomUUID();
    sessionStorage.setItem('syncRequestId', requestId);
    this.send({ type: 'SYNC_REQUEST', data: { request_id: requestId } });
    startReedRequestDrainer();
  }

  async subscribeProfile(userId: string): Promise<void> {
    await this.connect();
    this.send({ type: 'SUBSCRIBE_PROFILE', data: { user_id: userId } });
  }

  unsubscribeProfile(userId: string): void {
    this.send({ type: 'UNSUBSCRIBE_PROFILE', data: { user_id: userId } });
  }

  async subscribeReed(authorId: string, reedId: string): Promise<void> {
    await this.connect();
    this.send({ type: 'SUBSCRIBE_REED', userID: authorId, reedID: reedId });
  }

  unsubscribeReed(authorId: string, reedId: string): void {
    this.send({ type: 'UNSUBSCRIBE_REED', userID: authorId, reedID: reedId });
  }

  subscribeToBroadcast(): void {
    this.send({ type: 'SUBSCRIBE_BROADCAST' });
  }

  unsubscribeFromBroadcast(): void {
    this.send({ type: 'UNSUBSCRIBE_BROADCAST' });
  }

  async subscribePipe(tag: string): Promise<void> {
    const normalized = tag.trim().replace(/^#/, '').toLowerCase();
    if (!normalized) return;
    await this.connect();
    this.send({ type: 'SUBSCRIBE_PIPE', data: { tag: normalized } });
  }

  unsubscribePipe(tag: string): void {
    const normalized = tag.trim().replace(/^#/, '').toLowerCase();
    if (!normalized) return;
    this.send({ type: 'UNSUBSCRIBE_PIPE', data: { tag: normalized } });
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
