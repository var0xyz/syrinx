import { requestSigner } from './request-signer';
import { authService } from './auth';

export type ServerEventHandler = (data: any) => void;

type PendingRequest = { resolve: (data: any) => void; reject: (err: any) => void };

export enum ServerEvent {
  ReedNotification = 'reed_notification',
  RelayRequest     = 'RELAY_REQUEST',
  RequestAck       = 'REQUEST_ACK',
  DataResponse     = 'DATA_RESPONSE',
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

          if (message.type === 'DATA_RESPONSE') {
            const pending = this.pendingRequests.get(message.data.request_id);
            if (pending) {
              pending.resolve(message.data.data);
              this.pendingRequests.delete(message.data.request_id);
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

  requestReedContent(reedId: string): Promise<any> {
    const requestId = crypto.randomUUID();
    const promise = new Promise<any>((resolve, reject) => {
      this.pendingRequests.set(requestId, { resolve, reject });
    });
    this.send({ type: 'REQUEST_REED', data: { request_id: requestId, reed_id: reedId } });
    return promise;
  }

  sendRelayResponse(eventId: string, data: any): void {
    this.send({ type: 'RELAY_RESPONSE', data: { event_id: eventId, data } });
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
