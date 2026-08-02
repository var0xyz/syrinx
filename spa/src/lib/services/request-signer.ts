/**
 * Request Signing Service
 * Communicates with service worker to sign requests
 * Does NOT store decrypted key in memory (only in service worker)
 *
 * After OS discards the SW heap, re-INIT_KEY from localStorage
 * (fingerprint + passphrase) on resume or before sign when the SW reports no key.
 */

import { privateKeyRepository } from '../repositories/privateKey';
import { authService } from './auth';

class RequestSignerService {
  private initialized = false;
  private resumeHookInstalled = false;
  private reinitInFlight: Promise<void> | null = null;

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

  private async getServiceWorker(): Promise<ServiceWorker> {
    await this.waitForServiceWorker();
    const registration = await navigator.serviceWorker.getRegistration();
    const serviceWorker = navigator.serviceWorker.controller || registration?.active || registration?.waiting;
    if (!serviceWorker) {
      throw new Error('No service worker available');
    }
    return serviceWorker;
  }

  private postToWorker<T>(type: string, data?: unknown): Promise<T> {
    return new Promise(async (resolve, reject) => {
      try {
        const serviceWorker = await this.getServiceWorker();
        const channel = new MessageChannel();
        const timeout = setTimeout(() => {
          reject(new Error(`Service worker ${type} timeout`));
        }, 30000);

        channel.port1.onmessage = (event) => {
          clearTimeout(timeout);
          if (event.data?.success) {
            resolve(event.data as T);
          } else {
            reject(new Error(event.data?.error || `Service worker ${type} failed`));
          }
        };

        serviceWorker.postMessage({ type, data }, [channel.port2]);
      } catch (error) {
        reject(error);
      }
    });
  }

  private installResumeHook(): void {
    if (this.resumeHookInstalled || typeof document === 'undefined') return;
    this.resumeHookInstalled = true;

    const rehydrate = () => {
      if (document.visibilityState !== 'visible') return;
      void this.ensureWorkerKey().catch((error) => {
        console.warn('RequestSigner: failed to rehydrate key on resume:', error);
      });
    };

    document.addEventListener('visibilitychange', rehydrate);
    window.addEventListener('pageshow', rehydrate);
    window.addEventListener('focus', rehydrate);
  }

  /**
   * Re-INIT_KEY from localStorage when the SW no longer has a decrypted key.
   */
  async ensureWorkerKey(): Promise<void> {
    if (this.reinitInFlight) {
      return this.reinitInFlight;
    }

    this.reinitInFlight = (async () => {
      try {
        const status = await this.postToWorker<{ success: boolean; hasKey?: boolean }>('HAS_KEY');
        if (status.hasKey) {
          this.initialized = true;
          return;
        }

        const fingerprint = authService.getActiveKeyFingerprint();
        const passphrase = authService.getPassphrase();
        if (!fingerprint || !passphrase) {
          this.initialized = false;
          throw new Error('Private key not initialized');
        }

        this.initialized = false;
        await this.initializeWorker(fingerprint, passphrase);
      } finally {
        this.reinitInFlight = null;
      }
    })();

    return this.reinitInFlight;
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

        await this.waitForServiceWorker();
        console.log('RequestSigner: Service worker is ready');

        const keyData = await privateKeyRepository.getPrivateKey(fingerprint);
        if (!keyData) {
          throw new Error('Private key not found');
        }
        console.log('RequestSigner: Private key retrieved from IndexedDB');

        const user = await authService.getCurrentUser();
        if (!user) {
          throw new Error('User not found');
        }
        console.log('RequestSigner: User data retrieved');

        console.log('RequestSigner: Sending INIT_KEY message to service worker');
        await this.postToWorker('INIT_KEY', {
          armoredKey: keyData.armor,
          passphrase,
          userId: user.id,
          fingerprint,
        });

        this.initialized = true;
        this.installResumeHook();
        console.log('RequestSigner: Service worker initialization complete');
        return;
      } catch (error) {
        lastError = error as Error;
        console.error(`RequestSigner: Attempt ${attempt} failed:`, error);

        if (attempt < maxRetries) {
          console.log(`RequestSigner: Retrying in 2 seconds...`);
          await new Promise(resolve => setTimeout(resolve, 2000));
        }
      }
    }

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
        this.initialized = false;
        return;
      }

      serviceWorker.postMessage(
        { type: 'CLEAR_KEY' },
        [channel.port2]
      );

      await clearPromise;
    } catch (error) {
      this.initialized = false;
      console.error('Failed to clear request signer session:', error);
      throw error;
    }
  }


  /**
   * Sign arbitrary text using the service worker
   * This is the core signing primitive
   */
  async sign(text: string): Promise<string> {
    await this.ensureWorkerKey();

    try {
      const signature = await this.getSignatureFromWorker(text);
      return this.encodeBase64Signature(signature);
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      if (!message.includes('Private key not initialized')) {
        throw error;
      }
      this.initialized = false;
      await this.ensureWorkerKey();
      const signature = await this.getSignatureFromWorker(text);
      return this.encodeBase64Signature(signature);
    }
  }

  /**
   * Sign a request by adding the required headers and signature
   * Communicates with service worker to get the signature
   */
  async signRequest(url: string, options: RequestInit = {}): Promise<RequestInit> {
    await this.ensureWorkerKey();

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
    const result = await this.postToWorker<{ success: boolean; signature: string }>('SIGN_TEXT', { text });
    return result.signature;
  }
}

export const requestSigner = new RequestSignerService();
