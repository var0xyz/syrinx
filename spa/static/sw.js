/**
 * Custom Service Worker wrapping Workbox
 * Handles request signing for offline-first PWA
 */

console.log('Service Worker: Starting initialization...');

try {
  importScripts('https://storage.googleapis.com/workbox-cdn/releases/6.5.4/workbox-sw.js');
  console.log('Service Worker: Workbox loaded successfully');
} catch (error) {
  console.error('Service Worker: Failed to load Workbox:', error);
}

try {
  importScripts('./openpgp.min.js');
  console.log('Service Worker: OpenPGP loaded successfully');
} catch (error) {
  console.error('Service Worker: Failed to load OpenPGP:', error);
}

console.log('Service Worker: External scripts loaded');

const { precacheAndRoute, cleanupOutdatedCaches } = workbox.precaching;
const { registerRoute } = workbox.routing;
const { NetworkOnly } = workbox.strategies;

// Precache and route static assets
if (self.__WB_MANIFEST) {
  precacheAndRoute(self.__WB_MANIFEST);
  cleanupOutdatedCaches();
}

// PGP operations handled directly in service worker
let privateKey = null;
let keyReady = false;
let serviceWorkerReady = false;

// Mark service worker as ready after scripts are loaded
console.log('Service Worker: Marking as ready');

// Listen for the activate event to ensure we claim clients
self.addEventListener('activate', (event) => {
  console.log('Service Worker: Activate event received');
  event.waitUntil(
    self.clients.claim().then(() => {
      console.log('Service Worker: Successfully claimed clients on activate');
      serviceWorkerReady = true;
    }).catch(error => {
      console.error('Service Worker: Failed to claim clients on activate:', error);
      serviceWorkerReady = true;
    })
  );
});

// Mark as ready immediately for testing
serviceWorkerReady = true;

// Handle skip waiting message
self.addEventListener('message', (event) => {
  if (event.data && event.data.type === 'SKIP_WAITING') {
    console.log('Service Worker: Received SKIP_WAITING message');
    self.skipWaiting();
  }
});

// Initialize PGP key directly in service worker
async function initKey(armoredKey, passphrase) {
  try {
    const { readPrivateKey, decryptKey } = openpgp;

    // Parse the private key
    const parsedKey = await readPrivateKey({ armoredKey });

    // Decrypt the private key
    privateKey = await decryptKey({
      privateKey: parsedKey,
      passphrase
    });
    keyReady = true;

    console.log('PGP key initialized successfully in service worker');
  } catch (error) {
    console.error('Failed to initialize PGP key in service worker:', error);
    throw error;
  }
}

// Sign text using PGP directly in service worker
async function signText(text) {
  if (!keyReady) {
    throw new Error('PGP key not ready in service worker');
  }

  try {
    const { createMessage, sign } = openpgp;

    // Create message and sign it
    const message = await createMessage({ text });
    const signature = await sign({
      message,
      signingKeys: privateKey,
      detached: true
    });

    // Strip armor delimiters to reduce payload size
    const strippedSignature = signature.replace(
      /-----BEGIN PGP SIGNATURE-----\n\n/g,
      ''
    ).replace(
      /\n-----END PGP SIGNATURE-----/g,
      ''
    );

    return strippedSignature;
  } catch (error) {
    console.error('Failed to sign text in service worker:', error);
    throw error;
  }
}

// Request signing utilities
function buildCanonicalRequestString(method, path, body = '', timestamp = '') {
  const builder = [];

  // Add method and path
  builder.push(`${method} ${path}`);

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

function escapeSignature(signature) {
  return signature.replace(/\n/g, '\\n');
}

// Sign request with PGP
async function signRequest(request) {
  try {
    // Clone request to read body
    const clone = request.clone();
    const method = clone.method;
    const url = new URL(clone.url);
    const path = url.pathname + (url.search || '');

    // Get body
    let body = '';
    if (method !== 'GET' && method !== 'HEAD') {
      const contentType = clone.headers.get('content-type') || '';
      if (contentType.includes('multipart/form-data')) {
        // For FormData, we need to parse it differently
        const formData = await clone.formData();
        const formDataEntries = [];
        for (const [key, value] of formData.entries()) {
          formDataEntries.push(`${key}=${value}`);
        }
        body = formDataEntries.join('&');
      } else {
        body = await clone.text();
      }
    }

    // Generate timestamp for replay protection
    const timestamp = Math.floor(Date.now() / 1000).toString();

    // Build canonical request string
    const canonicalRequest = buildCanonicalRequestString(method, path, body, timestamp);

    // Sign the request
    const signature = await signText(canonicalRequest);

    // Add signature header
    const signedRequest = new Request(request, {
      headers: {
        ...Object.fromEntries(request.headers.entries()),
        'X-Syrinx-Signature': escapeSignature(signature),
        'X-Syrinx-Timestamp': timestamp,
      }
    });

    return signedRequest;
  } catch (error) {
    console.error('Failed to sign request:', error);
    throw error;
  }
}

// Listen for messages from main app
self.addEventListener('message', async (event) => {
  console.log('Service Worker: Received message:', event.data.type);

  // Check if service worker is ready
  if (!serviceWorkerReady) {
    console.error('Service Worker: Not ready yet');
    event.ports[0].postMessage({ success: false, error: 'Service worker not ready' });
    return;
  }

  const { type, data } = event.data;

  if (type === 'INIT_KEY') {
    try {
      console.log('Service Worker: Initializing PGP key...');
      await initKey(data.armoredKey, data.passphrase);
      console.log('Service Worker: PGP key initialization successful');
      event.ports[0].postMessage({ success: true });
    } catch (error) {
      console.error('Service Worker: PGP key initialization failed:', error);
      event.ports[0].postMessage({ success: false, error: error.message });
    }
  } else if (type === 'CLEAR_KEY') {
    console.log('Service Worker: Clearing PGP key');
    privateKey = null;
    keyReady = false;
    event.ports[0].postMessage({ success: true });
  } else if (type === 'SIGN_TEXT') {
    try {
      console.log('Service Worker: Signing text...');
      const signature = await signText(data.text);
      console.log('Service Worker: Text signed successfully');
      event.ports[0].postMessage({ success: true, signature });
    } catch (error) {
      console.error('Service Worker: Text signing failed:', error);
      event.ports[0].postMessage({ success: false, error: error.message });
    }
  } else if (type === 'TEST_COMMUNICATION') {
    console.log('Service Worker: Received test communication message');
    event.ports[0].postMessage({ success: true, message: 'Service worker is ready' });
  }
});

// Register route for API requests
registerRoute(
  ({ url }) => url.pathname.startsWith('/api/'),
  async ({ request }) => {
    try {
      // Check if this is an authenticated request (has user headers)
      const userId = request.headers.get('X-Syrinx-User-Id');
      const fingerprint = request.headers.get('X-Syrinx-Fingerprint');

      if (userId && fingerprint && keyReady) {
        // Sign the request
        const signedRequest = await signRequest(request);
        const strategy = new NetworkOnly();
        return strategy.handle({ request: signedRequest });
      } else {
        // Unauthenticated request, forward as-is
        const strategy = new NetworkOnly();
        return strategy.handle({ request });
      }
    } catch (error) {
      console.error('Service worker error:', error);
      const strategy = new NetworkOnly();
      return strategy.handle({ request });
    }
  }
);