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

      // Check if we can communicate with the active service worker directly
      navigator.serviceWorker.getRegistration().then(registration => {
        if (registration?.active && registration.active.state === 'activated') {
          console.log('RequestSigner: Service worker is active, using direct communication');
          resolve();
          return;
        }
      });

      console.log('RequestSigner: Waiting for service worker to be ready...');

      // Check if service worker is registered
      navigator.serviceWorker.getRegistration().then(registration => {
        if (!registration) {
          reject(new Error('Service worker not registered'));
          return;
        }

        console.log('RequestSigner: Service worker registration found:', registration);

        // Check if service worker is already active
        if (registration.active && registration.active.state === 'activated') {
          console.log('RequestSigner: Service worker is active, waiting for controller...');
        } else if (registration.installing) {
          console.log('RequestSigner: Service worker is installing...');
        } else if (registration.waiting) {
          console.log('RequestSigner: Service worker is waiting, trying to activate...');
          // Try to activate the waiting service worker
          registration.waiting.postMessage({ type: 'SKIP_WAITING' });
        }

        // Wait for service worker to be ready with longer timeout
        const timeout = setTimeout(() => {
          console.error('RequestSigner: Service worker timeout - registration:', registration);
          reject(new Error('Service worker initialization timeout - service worker may be taking too long to load external scripts'));
        }, 5000); // 5 second timeout

        const checkController = () => {
          if (navigator.serviceWorker.controller) {
            console.log('RequestSigner: Service worker controller is ready');
            clearTimeout(timeout);
            resolve();
          }
        };

        // Listen for controller change
        navigator.serviceWorker.addEventListener('controllerchange', checkController);

        // Also check periodically in case the event doesn't fire
        const interval = setInterval(() => {
          if (navigator.serviceWorker.controller) {
            console.log('RequestSigner: Service worker controller found via polling');
            clearTimeout(timeout);
            clearInterval(interval);
            navigator.serviceWorker.removeEventListener('controllerchange', checkController);
            resolve();
          }
        }, 100); // Check more frequently

        // If service worker is active but not controlling, wait a bit longer
        if (registration.active && registration.active.state === 'activated' && !navigator.serviceWorker.controller) {
          console.log('RequestSigner: Service worker is active but not controlling, waiting for controller...');
          // Wait a bit longer for the service worker to take control
          setTimeout(() => {
            if (navigator.serviceWorker.controller) {
              console.log('RequestSigner: Service worker controller became available after delay');
              clearTimeout(timeout);
              clearInterval(interval);
              navigator.serviceWorker.removeEventListener('controllerchange', checkController);
              resolve();
            }
          }, 2000); // Wait 2 seconds for the service worker to claim control
        }

        // Try to test communication with service worker after a short delay
        setTimeout(async () => {
          if (navigator.serviceWorker.controller) {
            try {
              console.log('RequestSigner: Testing service worker communication...');
              const channel = new MessageChannel();
              const testPromise = new Promise((resolveTest, rejectTest) => {
                const testTimeout = setTimeout(() => {
                  rejectTest(new Error('Service worker communication test timeout'));
                }, 2000);

                channel.port1.onmessage = (event) => {
                  clearTimeout(testTimeout);
                  if (event.data && event.data.success !== undefined) {
                    console.log('RequestSigner: Service worker communication test successful');
                    resolveTest(undefined);
                  } else {
                    rejectTest(new Error('Service worker communication test failed'));
                  }
                };
              });

              // Send a test message
              navigator.serviceWorker.controller.postMessage(
                { type: 'TEST_COMMUNICATION' },
                [channel.port2]
              );

              await testPromise;
              console.log('RequestSigner: Service worker is ready and responsive');
            } catch (error) {
              console.warn('RequestSigner: Service worker communication test failed:', error);
            }
          }
        }, 1000);

        // Clean up on timeout
        setTimeout(() => {
          clearInterval(interval);
          navigator.serviceWorker.removeEventListener('controllerchange', checkController);
        }, 15000);
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
    try {
      console.log('RequestSigner: Starting service worker initialization...');

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
          reject(new Error('Service worker key initialization timeout'));
        }, 10000); // 10 second timeout for key initialization

        channel.port1.onmessage = (event) => {
          clearTimeout(timeout);
          if (event.data.success) {
            console.log('RequestSigner: Service worker key initialization successful');
            this.initialized = true;
            resolve();
          } else {
            reject(new Error(event.data.error || 'Failed to initialize worker'));
          }
        };
      });

      console.log('RequestSigner: Sending INIT_KEY message to service worker');
      // Send initialization message to service worker
      const registration = await navigator.serviceWorker.getRegistration();
      const serviceWorker = navigator.serviceWorker.controller || registration?.active;

      if (!serviceWorker) {
        throw new Error('No service worker available');
      }

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
    } catch (error) {
      console.error('Failed to initialize request signer:', error);
      throw error;
    }
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
      const serviceWorker = navigator.serviceWorker.controller || registration?.active;

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
    console.log('RequestSigner: Canonical request string:\n', canonicalRequest);

    // Get signature from service worker
    const signature = await this.getSignatureFromWorker(canonicalRequest);

    // Add signature headers
    const signedHeaders = new Headers(options.headers);
    signedHeaders.set('X-Syrinx-User-Id', user.id);
    signedHeaders.set('X-Syrinx-Fingerprint', fingerprint);
    signedHeaders.set('X-Syrinx-Algorithm', 'PGP');
    signedHeaders.set('X-Syrinx-Signature-Scope', 'body');
    signedHeaders.set('X-Syrinx-Timestamp', timestamp);
    signedHeaders.set('X-Syrinx-Signature', this.escapeSignature(signature));

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

    // Add method and path
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
   * Escape signature for header
   */
  private escapeSignature(signature: string): string {
    return signature.replace(/\n/g, '\\n');
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
      const serviceWorker = navigator.serviceWorker.controller || registration?.active;

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
