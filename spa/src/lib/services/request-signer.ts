/**
 * Request Signing Service
 * Communicates with service worker to sign requests
 * Does NOT store decrypted key in memory (only in service worker)
 */

import { privateKeyRepository } from '../repositories/privateKey';
import { authService } from './auth';

class RequestSignerService {
  private initialized = false;

  /**
   * Wait for service worker to be ready
   */
  private async waitForServiceWorker(): Promise<void> {
    return new Promise((resolve, reject) => {
      if (!('serviceWorker' in navigator)) {
        reject(new Error('Service worker not supported'));
        return;
      }

      // If service worker is already ready
      if (navigator.serviceWorker.controller) {
        console.log('RequestSigner: Service worker already controlling');
        resolve();
        return;
      }

      console.log('RequestSigner: Waiting for service worker to be ready...');

      // Check if service worker is registered
      navigator.serviceWorker.getRegistration().then(registration => {
        if (!registration) {
          reject(new Error('Service worker not registered'));
          return;
        }

        console.log('RequestSigner: Service worker registration found');

        // If there's an active service worker, use it directly
        if (registration.active && registration.active.state === 'activated') {
          console.log('RequestSigner: Service worker is active, using direct communication');
          resolve();
          return;
        }

        // If there's a waiting service worker, try to activate it
        if (registration.waiting) {
          console.log('RequestSigner: Activating waiting service worker');
          registration.waiting.postMessage({ type: 'SKIP_WAITING' });
        }

        // Wait for controller with timeout
        const timeout = setTimeout(() => {
          console.error('RequestSigner: Service worker timeout');
          reject(new Error('Service worker initialization timeout'));
        }, 5000); // 5 second timeout

        const checkController = () => {
          if (navigator.serviceWorker.controller) {
            console.log('RequestSigner: Service worker controller is ready');
            clearTimeout(timeout);
            navigator.serviceWorker.removeEventListener('controllerchange', checkController);
            resolve();
          }
        };

        // Listen for controller change
        navigator.serviceWorker.addEventListener('controllerchange', checkController);

        // Also check periodically
        const interval = setInterval(() => {
          if (navigator.serviceWorker.controller) {
            console.log('RequestSigner: Service worker controller found via polling');
            clearTimeout(timeout);
            clearInterval(interval);
            navigator.serviceWorker.removeEventListener('controllerchange', checkController);
            resolve();
          }
        }, 100);

        // Clean up on timeout
        setTimeout(() => {
          clearInterval(interval);
          navigator.serviceWorker.removeEventListener('controllerchange', checkController);
        }, 6000);

      }).catch(error => {
        console.error('RequestSigner: Service worker registration check failed:', error);
        reject(new Error(`Service worker registration check failed: ${error.message}`));
      });
    });
  }


  /**
   * Initialize the service worker with a decrypted private key
   * Key is decrypted, passed to worker, then immediately discarded
   */
  async initializeWorker(fingerprint: string, passphrase: string): Promise<void> {
    if (!fingerprint) {
      throw new Error('RequestSigner: fingerprint is required to initialize');
    }
    if (!passphrase) {
      throw new Error('RequestSigner: passphrase is required to initialize');
    }

    const maxRetries = 3;
    let lastError: Error | null = null;

    for (let attempt = 1; attempt <= maxRetries; attempt++) {
      try {
        console.log(`RequestSigner: Starting service worker initialization (attempt ${attempt}/${maxRetries})...`);

        // Wait for service worker to be ready
        await this.waitForServiceWorker();
        console.log('RequestSigner: Service worker is ready');

      // Get the private key from IndexedDB
      const keyData = await privateKeyRepository.getPrivateKey(fingerprint);
      if (!keyData) {
        throw new Error('Private key not found');
      }
      console.log('RequestSigner: Private key retrieved from IndexedDB');

      // Get user ID for headers
      const user = await authService.getCurrentUser();
      if (!user) {
        throw new Error('User not found');
      }
      console.log('RequestSigner: User data retrieved');

      // Create a message channel for communication
      const channel = new MessageChannel();

      // Set up promise to wait for response
      const initPromise = new Promise<void>((resolve, reject) => {
        const timeout = setTimeout(() => {
          console.error('RequestSigner: Service worker initialization timeout after 30 seconds');
          reject(new Error('Service worker key initialization timeout'));
        }, 30000); // 30 second timeout for key initialization

        channel.port1.onmessage = (event) => {
          clearTimeout(timeout);
          if (event.data.success) {
            console.log('RequestSigner: Service worker key initialization successful');
            this.initialized = true;
            resolve();
          } else {
            console.error('RequestSigner: Service worker initialization failed:', event.data.error);
            reject(new Error(event.data.error || 'Failed to initialize worker'));
          }
        };
      });

      console.log('RequestSigner: Sending INIT_KEY message to service worker');
      // Send initialization message to service worker
      const registration = await navigator.serviceWorker.getRegistration();
      const serviceWorker = navigator.serviceWorker.controller || registration?.active || registration?.waiting;

      if (!serviceWorker) {
        throw new Error('No service worker available');
      }

      console.log('RequestSigner: Using service worker:', serviceWorker.state);

      serviceWorker.postMessage(
        {
          type: 'INIT_KEY',
          data: {
            armoredKey: keyData.armor,
            passphrase: passphrase,
            userId: user.id,
            fingerprint: fingerprint
          }
        },
        [channel.port2]
      );

        await initPromise;
        console.log('RequestSigner: Service worker initialization complete');
        return; // Success, exit the retry loop
      } catch (error) {
        lastError = error as Error;
        console.error(`RequestSigner: Attempt ${attempt} failed:`, error);

        if (attempt < maxRetries) {
          console.log(`RequestSigner: Retrying in 2 seconds...`);
          await new Promise(resolve => setTimeout(resolve, 2000));
        }
      }
    }

    // If we get here, all retries failed
    console.error('RequestSigner: All initialization attempts failed');
    throw lastError || new Error('Failed to initialize request signer after all retries');
  }

  /**
   * Check if the service worker is initialized and ready
   */
  isInitialized(): boolean {
    return this.initialized;
  }

  /**
   * Clear the session and remove key from service worker
   */
  async clearSession(): Promise<void> {
    try {
      // Wait for service worker to be ready
      await this.waitForServiceWorker();

      const channel = new MessageChannel();

      const clearPromise = new Promise<void>((resolve, reject) => {
        channel.port1.onmessage = (event) => {
          if (event.data.success) {
            this.initialized = false;
            resolve();
          } else {
            reject(new Error(event.data.error || 'Failed to clear session'));
          }
        };
      });

      const registration = await navigator.serviceWorker.getRegistration();
      const serviceWorker = navigator.serviceWorker.controller || registration?.active || registration?.waiting;

      if (!serviceWorker) {
        throw new Error('No service worker available');
      }

      serviceWorker.postMessage(
        { type: 'CLEAR_KEY' },
        [channel.port2]
      );

      await clearPromise;
    } catch (error) {
      console.error('Failed to clear request signer session:', error);
      throw error;
    }
  }


  /**
   * Sign arbitrary text using the service worker
   * This is the core signing primitive
   */
  async sign(text: string): Promise<string> {
    if (!this.initialized) {
      throw new Error('Request signer not initialized');
    }

    // Get signature from service worker
    const signature = await this.getSignatureFromWorker(text);

    // Return the base64-encoded signature
    return this.encodeBase64Signature(signature);
  }

  /**
   * Sign a request by adding the required headers and signature
   * Communicates with service worker to get the signature
   */
  async signRequest(url: string, options: RequestInit = {}): Promise<RequestInit> {
    if (!this.initialized) {
      throw new Error('Request signer not initialized');
    }

    // Get user info
    const user = await authService.getCurrentUser();
    const fingerprint = authService.getActiveKeyFingerprint();

    if (!user || !fingerprint) {
      throw new Error('User or active key not found');
    }

    // Build canonical request string
    const method = options.method || 'GET';
    const urlObj = new URL(url, window.location.origin);
    const path = urlObj.pathname + (urlObj.search || '');

    // Get body (always include, even if empty)
    let body = '';
    if (method !== 'GET' && method !== 'HEAD' && options.body) {
      if (options.body instanceof FormData) {
        // Convert FormData to a consistent string representation
        const formDataEntries: string[] = [];
        for (const [key, value] of options.body.entries()) {
          formDataEntries.push(`${key}=${value}`);
        }
        body = formDataEntries.join('&');
      } else {
        body = options.body as string || '';
      }
    }

    // Generate timestamp for replay protection
    const timestamp = Math.floor(Date.now() / 1000).toString();

    // Build canonical request string (no headers needed)
    const canonicalRequest = this.buildCanonicalRequestString(method, path, body, timestamp);

    // Get signature using the generic sign method (already base64 encoded)
    const signature = await this.sign(canonicalRequest);

    // Add signature headers
    const signedHeaders = new Headers(options.headers);
    signedHeaders.set('X-Syrinx-User-Id', user.id);
    signedHeaders.set('X-Syrinx-Fingerprint', fingerprint);
    signedHeaders.set('X-Syrinx-Signature-Scope', 'body');
    signedHeaders.set('X-Syrinx-Timestamp', timestamp);
    signedHeaders.set('X-Syrinx-Signature', signature);

    return {
      ...options,
      headers: signedHeaders,
      // Remove credentials since we're using signatures
      credentials: undefined
    };
  }

  /**
   * Build canonical request string for signing
   * Only signs method + path + query + body + timestamp (no headers)
   */
  private buildCanonicalRequestString(method: string, path: string, body: string = '', timestamp: string = ''): string {
    const builder = [];

    // Add method and path (path already includes query string from signRequest)
    builder.push(`${method} ${path}`);
    console.log('RequestSigner: Method:', method);
    console.log('RequestSigner: Path:', path);

    // Always add body (even if empty) - this ensures there's always something to sign
    builder.push('');
    builder.push(body);

    // Add timestamp for replay protection
    if (timestamp) {
      builder.push('');
      builder.push(timestamp);
    }

    return builder.join('\n');
  }

  /**
   * Encode signature as base64
   */
  private encodeBase64Signature(signature: string): string {

    return btoa(signature.trim());
  }

  /**
   * Get signature from service worker
   */
  private async getSignatureFromWorker(text: string): Promise<string> {
    try {
      // Wait for service worker to be ready
      await this.waitForServiceWorker();

      const channel = new MessageChannel();

      const signPromise = new Promise<string>((resolve, reject) => {
        channel.port1.onmessage = (event) => {
          if (event.data.success) {
            resolve(event.data.signature);
          } else {
            reject(new Error(event.data.error || 'Failed to sign text'));
          }
        };
      });

      // Send sign text message to service worker
      const registration = await navigator.serviceWorker.getRegistration();
      const serviceWorker = navigator.serviceWorker.controller || registration?.active || registration?.waiting;

      if (!serviceWorker) {
        throw new Error('No service worker available');
      }

      serviceWorker.postMessage(
        {
          type: 'SIGN_TEXT',
          data: { text }
        },
        [channel.port2]
      );

      return await signPromise;
    } catch (error) {
      console.error('Failed to sign text:', error);
      throw error;
    }
  }
}

export const requestSigner = new RequestSignerService();
