/**
 * WebSocket Service
 * Handles real-time WebSocket connections with PGP+base64 authentication
 */

import { requestSigner } from './request-signer';
import { authService } from './auth';

export interface WebSocketMessage {
  type: string;
  data?: any;
}

export interface ReedNotification {
  reedId: string;
  userId: string;
  username: string;
  content: string;
  timestamp: number;
}

export interface UserUpdate {
  userId: string;
  updateType: string;
  timestamp: number;
}

export type WebSocketEventHandler = (data: any) => void;

class WebSocketService {
  private ws: WebSocket | null = null;
  private reconnectAttempts = 3;
  private reconnectDelay = 1000; // Start with 1 second
  private isConnecting = false;
  private eventHandlers: Map<string, WebSocketEventHandler[]> = new Map();
  private pingInterval: number | null = null;
  private isAuthenticated = false;

  /**
   * Connect to WebSocket with authentication
   */
  async connect(): Promise<void> {
    console.log('Connecting to WebSocket...');
    if (this.ws?.readyState === WebSocket.OPEN || this.isConnecting) {
      console.log('WebSocket already connected or connecting');
      return;
    }

    // Check if user is authenticated
    const user = await authService.getCurrentUser();
    if (!user) {
      console.log('No user found, skipping WebSocket connection');
      return;
    }

    // Ensure request signer is initialized
    if (!requestSigner.isInitialized()) {
      const fingerprint = authService.getActiveKeyFingerprint();
      const passphrase = authService.getPassphrase();

      if (!fingerprint || !passphrase) {
        console.log('No auth data available for request signer initialization');
        return;
      }

      try {
        await requestSigner.initializeWorker(fingerprint, passphrase);
        console.log('Request signer initialized successfully');
      } catch (error) {
        console.error('Failed to initialize request signer:', error);
        return;
      }
    }

    this.isConnecting = true;

    try {
      // Get authentication headers
      const authParameters = await this.getAuthParameters();

      // Create WebSocket URL
      const wsUrl = this.buildWebSocketUrl(authParameters);

      console.log('Connecting to WebSocket...');
      this.ws = new WebSocket(wsUrl);

      // Set up message and close handlers first (before connection opens)
      // so we don't miss any early messages
      this.setupMessageHandlers();

      // Wait for connection to open (this will set onopen and onerror handlers)
      await this.waitForConnection();

      console.log('WebSocket connected successfully');
      this.isAuthenticated = true;
      this.reconnectAttempts = 0;
      this.startPingInterval();

    } catch (error) {
      console.error('Failed to connect to WebSocket:', error);
      this.isConnecting = false;
      throw error;
    } finally {
      this.isConnecting = false;
    }
  }

  /**
   * Disconnect from WebSocket
   */
  disconnect(): void {
    if (this.pingInterval) {
      clearInterval(this.pingInterval);
      this.pingInterval = null;
    }

    if (this.ws) {
      this.ws.close();
      this.ws = null;
    }

    this.isAuthenticated = false;
    console.log('WebSocket disconnected');
  }

  /**
   * Subscribe to user-specific notifications
   */
  subscribeToUser(): void {
    if (!this.isConnected()) {
      console.warn('WebSocket not connected, cannot subscribe to user notifications');
      return;
    }

    this.sendMessage({
      type: 'SUBSCRIBE_USER',
      data: 'Subscribe to user notifications'
    });
  }

  /**
   * Subscribe to broadcast notifications (all traffic)
   */
  subscribeToBroadcast(): void {
    if (!this.isConnected()) {
      console.warn('WebSocket not connected, cannot subscribe to broadcast notifications');
      return;
    }

    this.sendMessage({
      type: 'SUBSCRIBE_BROADCAST',
      data: 'Subscribe to broadcast notifications'
    });
  }

  /**
   * Unsubscribe from user notifications
   */
  unsubscribeFromUser(): void {
    if (!this.isConnected()) {
      return;
    }

    this.sendMessage({
      type: 'UNSUBSCRIBE_USER',
      data: 'Unsubscribe from user notifications'
    });
  }

  /**
   * Unsubscribe from broadcast notifications
   */
  unsubscribeFromBroadcast(): void {
    if (!this.isConnected()) {
      return;
    }

    this.sendMessage({
      type: 'UNSUBSCRIBE_BROADCAST',
      data: 'Unsubscribe from broadcast notifications'
    });
  }

  /**
   * Add event handler for specific message types
   */
  on(eventType: string, handler: WebSocketEventHandler): void {
    if (!this.eventHandlers.has(eventType)) {
      this.eventHandlers.set(eventType, []);
    }
    this.eventHandlers.get(eventType)!.push(handler);
  }

  /**
   * Remove event handler
   */
  off(eventType: string, handler: WebSocketEventHandler): void {
    const handlers = this.eventHandlers.get(eventType);
    if (handlers) {
      const index = handlers.indexOf(handler);
      if (index > -1) {
        handlers.splice(index, 1);
      }
    }
  }

  /**
   * Check if WebSocket is connected
   */
  isConnected(): boolean {
    return this.ws?.readyState === WebSocket.OPEN && this.isAuthenticated;
  }

  /**
   * Get authentication parameters for WebSocket connection
   */
  private async getAuthParameters(): Promise<Record<string, string>> {
    const user = await authService.getCurrentUser();
    const fingerprint = authService.getActiveKeyFingerprint();

    if (!user || !fingerprint) {
      throw new Error('User or active key not found');
    }

    // Generate timestamp for replay protection
    const timestamp = Math.floor(Date.now() / 1000).toString();

    // Sign the timestamp using the generic sign method
    const signature = await requestSigner.sign(timestamp);

    // Return simplified authentication parameters
    return {
      timestamp,
      signature,
      fingerprint,
      userID: user.id,
      algorithm: 'PGP+base64'
    };
  }

  /**
   * Build WebSocket URL with authentication headers
   */
  private buildWebSocketUrl(authParams: Record<string, string>): string {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    // Use the current port from window.location to ensure we connect to the right dev server
    const host = window.location.host;
    const url = new URL(`${protocol}//${host}/ws/`);
    console.log('WebSocket URL:', url.toString());
    console.log('Current location:', window.location.href);

    Object.entries(authParams).forEach(([key, value]) => {
      url.searchParams.set(key, value);
    });

    return url.toString();
  }

  /**
   * Setup message and close handlers (onopen and onerror are handled in waitForConnection)
   */
  private setupMessageHandlers(): void {
    if (!this.ws) return;

    this.ws.onmessage = (event) => {
      try {
        // Try to parse as JSON first (for testing)
        const message = JSON.parse(event.data);
        this.handleMessage(message);
      } catch {
        // If not JSON, try to parse as protobuf binary
        this.handleBinaryMessage(event.data);
      }
    };

    this.ws.onclose = (event) => {
      console.log('WebSocket connection closed:', event.code, event.reason);
      this.isAuthenticated = false;

      if (this.pingInterval) {
        clearInterval(this.pingInterval);
        this.pingInterval = null;
      }

      // Attempt to reconnect if not a clean close
      if (event.code !== 1000 && this.reconnectAttempts > 0) {
        this.scheduleReconnect();
      }
    };
  }

  /**
   * Handle JSON messages (for testing)
   */
  private handleMessage(message: WebSocketMessage): void {
    console.log('Received WebSocket message:', message);

    switch (message.type) {
      case 'pong':
        console.log('Received pong:', message.data);
        break;
      case 'subscribed':
        console.log('Subscription confirmed:', message.data);
        break;
      case 'reed_notification':
        this.emit('reed_notification', message.data);
        break;
      case 'user_update':
        this.emit('user_update', message.data);
        break;
      default:
        console.log('Unknown message type:', message.type);
    }
  }

  /**
   * Handle binary protobuf messages
   */
  private handleBinaryMessage(data: ArrayBuffer): void {
    // TODO: Implement protobuf message parsing
    console.log('Received binary message (protobuf):', data.byteLength, 'bytes');
  }

  /**
   * Send message to WebSocket
   */
  private sendMessage(message: WebSocketMessage): void {
    if (!this.isConnected()) {
      console.warn('WebSocket not connected, cannot send message');
      return;
    }

    // For now, send as JSON (in production, use protobuf)
    this.ws!.send(JSON.stringify(message));
  }

  /**
   * Emit event to registered handlers
   */
  private emit(eventType: string, data: any): void {
    const handlers = this.eventHandlers.get(eventType);
    if (handlers) {
      handlers.forEach(handler => handler(data));
    }
  }

  /**
   * Wait for WebSocket connection to open
   */
  private waitForConnection(): Promise<void> {
    return new Promise((resolve, reject) => {
      if (!this.ws) {
        reject(new Error('WebSocket not initialized'));
        return;
      }

      // Check if already open
      if (this.ws.readyState === WebSocket.OPEN) {
        console.log('WebSocket connection already open');
        resolve();
        return;
      }

      const timeout = setTimeout(() => {
        reject(new Error('WebSocket connection timeout'));
      }, 10000); // 10 second timeout

      this.ws.onopen = () => {
        console.log('WebSocket connection opened');
        clearTimeout(timeout);
        resolve();
      };

      this.ws.onerror = (error) => {
        console.error('WebSocket connection error:', error);
        clearTimeout(timeout);
        reject(new Error('WebSocket connection failed'));
      };
    });
  }

  /**
   * Schedule reconnection attempt
   */
  private scheduleReconnect(): void {
    const delay = this.reconnectDelay * Math.pow(2, this.reconnectAttempts - 1); // Exponential backoff

    console.log(`Scheduling WebSocket reconnection... ${this.reconnectAttempts} attempts left (next in ${delay}ms)`);

    setTimeout(() => {
      this.connect().catch(error => {
        console.error('Reconnection failed:', error);
      });
    }, delay);


    this.reconnectAttempts--;
  }

  /**
   * Start ping interval to keep connection alive
   */
  private startPingInterval(): void {
    this.pingInterval = window.setInterval(() => {
      if (this.isConnected()) {
        this.sendMessage({
          type: 'ping',
          data: 'ping'
        });
      }
    }, 30000); // Ping every 30 seconds
  }
}

export const websocketService = new WebSocketService();
