import { requestSigner } from './request-signer';
import { authService } from './auth';

declare const md5: (str: string) => string;

export type ServerEventHandler = (data: any) => void;

type PendingRequest = { resolve: (data: any) => void; reject: (err: any) => void };
type RequestRecord = { data: any; status: 'new' | 'waiting' };

export enum ServerEvent {
  ReedNotification = 'reed_notification',
  RelayRequest     = 'RELAY_REQUEST',
  RequestAck       = 'REQUEST_ACK',
  DataResponse     = 'DATA_RESPONSE',
  BroadcastReed    = 'BROADCAST_REED',
  ReedNotFound     = 'REED_NOT_FOUND',
  ReedRemoved      = 'REED_REMOVED',
  AccountRemoved   = 'ACCOUNT_REMOVED',
}

class ServerConnection {
  private ws: WebSocket | null = null;
  private connectingPromise: Promise<void> | null = null;
  private eventHandlers: Map<string, ServerEventHandler[]> = new Map();
  private pendingRequests: Map<string, PendingRequest> = new Map();

  async connect(): Promise<void> {
    if (this.ws?.readyState === WebSocket.OPEN) return;
    if (this.connectingPromise) return this.connectingPromise;

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

      const timestamp = Math.floor(Date.now() / 1000).toString();
      const signature = await requestSigner.sign(timestamp);
      const fingerprint = authService.getActiveKeyFingerprint()!;

      const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
      const url = new URL(`${protocol}//${window.location.host}/ws/`);
      url.searchParams.set('userID', user.id);
      url.searchParams.set('fingerprint', fingerprint);
      url.searchParams.set('timestamp', timestamp);
      url.searchParams.set('signature', signature);
      url.searchParams.set('algorithm', 'PGP+base64');

      console.log('ServerConnection: connecting...');
      this.ws = new WebSocket(url.toString());

      this.ws.onmessage = (event) => {
        try {
          const message = JSON.parse(event.data);
          console.log('ServerConnection: message received:', message.type);

          if (message.type === ServerEvent.RequestAck) {
            const record = this.getRequest(message.data.request_id);
            if (!record) {
              console.warn(`ServerConnection: ${ServerEvent.RequestAck} for unknown request, discarding:`, message.data.request_id);
              return;
            }
            this.setRequest(message.data.request_id, { data: message.data, status: 'waiting' });
          } else if (message.type === 'DATA_RESPONSE') {
            const pending = this.pendingRequests.get(message.data.request_id);
            if (pending) {
              pending.resolve(message.data.data);
              this.pendingRequests.delete(message.data.request_id);
              this.deleteRequest(message.data.request_id);
            }
          } else if (message.type === ServerEvent.ReedNotFound) {
            const pending = this.pendingRequests.get(message.data.request_id);
            if (pending) {
              pending.reject(new Error('reed_not_found'));
              this.pendingRequests.delete(message.data.request_id);
              this.deleteRequest(message.data.request_id);
            }
          }

          this.emit(message.type, message.data);
        } catch {
          console.warn('ServerConnection: received non-JSON message, ignoring');
        }
      };

      this.ws.onclose = (event) => {
        console.log('ServerConnection: connection closed', event.code, event.reason);
        this.ws = null;
        sessionStorage.removeItem('syncRequestId');
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
      this.ws = null;
    }
  }

  disconnect(): void {
    if (this.ws) {
      this.ws.close();
      this.ws = null;
    }
    console.log('ServerConnection: disconnected');
  }

  isConnected(): boolean {
    return this.ws?.readyState === WebSocket.OPEN;
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

  requestReedContent(reedId: string, userId: string, serverId: string): Promise<any> {
    const requestId = md5(`REQUEST_REED:${serverId}/${userId}/${reedId}`);
    const promise = new Promise<any>((resolve, reject) => {
      this.pendingRequests.set(requestId, { resolve, reject });
    });
    if (this.getRequest(requestId)) {
      console.warn('ServerConnection: duplicate request detected, skipping send:', requestId);
      return promise;
    }
    this.setRequest(requestId, { data: null, status: 'new' });
    this.send({ type: 'REQUEST_REED', data: { request_id: requestId, reed_id: reedId } });
    return promise;
  }

  private setRequest(requestId: string, record: RequestRecord): void {
    sessionStorage.setItem(`req:${requestId}`, JSON.stringify(record));
  }

  private getRequest(requestId: string): RequestRecord | null {
    const raw = sessionStorage.getItem(`req:${requestId}`);
    return raw ? JSON.parse(raw) : null;
  }

  private deleteRequest(requestId: string): void {
    sessionStorage.removeItem(`req:${requestId}`);
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

  // Store a relay request that couldn't be fulfilled immediately because the reed
  // wasn't in IndexedDB yet. Keyed by md5(RELAY_REQUEST:serverId/userId/reedId) so
  // storeReedInIndexedDB can find and dispatch it after the write completes.
  storePendingRelayRequest(reedId: string, eventId: string): void {
    const key = this.relayPendingKey(reedId);
    sessionStorage.setItem(key, JSON.stringify({ event_id: eventId }));
  }

  fulfillPendingRelayRequest(reedId: string, reed: any): void {
    const key = this.relayPendingKey(reedId);
    const raw = sessionStorage.getItem(key);
    if (!raw) {
      console.warn(`ServerConnection: fulfillPendingRelayRequest called for '${reedId}' but no pending relay found in sessionStorage`);
      return;
    }
    const { event_id } = JSON.parse(raw);
    console.log(`ServerConnection: sending relay response for reed '${reedId}', event '${event_id}'`);
    this.sendRelayResponse(event_id, reed);
    sessionStorage.removeItem(key);
    console.log(`Reed '${reedId}' relayed`);
  }

  private relayPendingKey(reedId: string): string {
    const serverId = localStorage.getItem('serverId') ?? '';
    const userId = localStorage.getItem('userId') ?? '';
    return md5(`RELAY_REQUEST:${serverId}/${userId}/${reedId}`);
  }

  syncRequest(): void {
    const requestId = crypto.randomUUID();
    sessionStorage.setItem('syncRequestId', requestId);
    this.send({ type: 'SYNC_REQUEST', data: { request_id: requestId } });
  }

  async subscribeProfile(userId: string): Promise<void> {
    await this.connect();
    this.send({ type: 'SUBSCRIBE_PROFILE', data: { user_id: userId } });
  }

  unsubscribeProfile(userId: string): void {
    this.send({ type: 'UNSUBSCRIBE_PROFILE', data: { user_id: userId } });
  }

  subscribeToBroadcast(): void {
    this.send({ type: 'SUBSCRIBE_BROADCAST' });
  }

  unsubscribeFromBroadcast(): void {
    this.send({ type: 'UNSUBSCRIBE_BROADCAST' });
  }

  private send(message: { type: string; data?: any }): void {
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
