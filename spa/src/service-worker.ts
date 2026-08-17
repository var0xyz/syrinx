/// <reference lib="webworker" />
/**
 * Service worker: PGP session for request signing + API fetch intercept.
 * OpenPGP is bundled from npm (openpgp/lightweight).
 *
 * Decrypted key is cached in memory. The page re-sends INIT_KEY after
 * focus/visibility when the OS has discarded the SW heap.
 */
import { precacheAndRoute, cleanupOutdatedCaches, createHandlerBoundToURL } from 'workbox-precaching';
import { registerRoute, NavigationRoute } from 'workbox-routing';
import * as openpgp from 'openpgp/lightweight';

declare let self: ServiceWorkerGlobalScope & {
  __WB_MANIFEST?: Array<string | { url: string; revision: string | null }>;
};

// Injected at build time by vite-plugin-pwa; empty array in Vite/SvelteKit dev.
precacheAndRoute(self.__WB_MANIFEST ?? []);
cleanupOutdatedCaches();

// SPA navigations: serve precached index.html (API/WS stay network-only).
// Skipped in Vite/SvelteKit dev when the precache is empty.
try {
  registerRoute(
    new NavigationRoute(createHandlerBoundToURL('index.html'), {
      denylist: [/^\/api\//, /^\/ws\//]
    })
  );
} catch {
  // createHandlerBoundToURL throws when index.html is not in the precache.
}

let privateKey: openpgp.PrivateKey | null = null;

// Activate immediately so a new deploy is not stuck waiting behind a tab
// whose page JS never loaded (stale-shell white screen).
self.addEventListener('install', (event) => {
  event.waitUntil(self.skipWaiting());
});

self.addEventListener('activate', (event) => {
  event.waitUntil(self.clients.claim());
});

self.addEventListener('message', (event) => {
  if (event.data?.type === 'SKIP_WAITING') {
    self.skipWaiting();
  }
});

async function initKey(armoredKey: string, passphrase: string): Promise<void> {
  const parsedKey = await openpgp.readPrivateKey({ armoredKey });
  privateKey = await openpgp.decryptKey({
    privateKey: parsedKey,
    passphrase
  });
}

/** Return cached decrypted key, or throw if the page has not (re)initialized. */
async function ensureDecryptedKey(): Promise<openpgp.PrivateKey> {
  if (privateKey) {
    return privateKey;
  }
  throw new Error('Private key not initialized');
}

async function signText(text: string): Promise<string> {
  const key = await ensureDecryptedKey();
  const message = await openpgp.createMessage({ text });
  const signature = await openpgp.sign({
    message,
    signingKeys: key,
    detached: true,
    format: 'armored'
  });
  return (signature as string).trim();
}

function buildCanonicalRequestString(method: string, path: string, body = '', timestamp = ''): string {
  const builder = [`${method} ${path}`, '', body];
  if (timestamp) {
    builder.push('');
    builder.push(timestamp);
  }
  return builder.join('\n');
}

function escapeSignature(signature: string): string {
  return signature.replace(/\n/g, '\\n');
}

async function signRequest(request: Request): Promise<Request> {
  const clone = request.clone();
  const method = clone.method;
  const url = new URL(clone.url);
  const path = url.pathname + (url.search || '');

  let body = '';
  if (method !== 'GET' && method !== 'HEAD') {
    const contentType = clone.headers.get('content-type') || '';
    if (contentType.includes('multipart/form-data')) {
      const formData = await clone.formData();
      const formDataEntries: string[] = [];
      for (const [key, value] of formData.entries()) {
        formDataEntries.push(`${key}=${value}`);
      }
      body = formDataEntries.join('&');
    } else {
      body = await clone.text();
    }
  }

  const timestamp = Math.floor(Date.now() / 1000).toString();
  const canonicalRequest = buildCanonicalRequestString(method, path, body, timestamp);
  const signature = await signText(canonicalRequest);

  return new Request(request, {
    headers: {
      ...Object.fromEntries(request.headers.entries()),
      'X-Syrinx-Signature': escapeSignature(signature),
      'X-Syrinx-Timestamp': timestamp
    }
  });
}

self.addEventListener('message', async (event) => {
  if (!event.data?.type) return;

  const { type, data } = event.data;
  if (type === 'SKIP_WAITING') return;

  if (!event.ports?.[0]) return;
  const port = event.ports[0];

  if (type === 'INIT_KEY') {
    try {
      await initKey(data.armoredKey, data.passphrase);
      port.postMessage({ success: true });
    } catch (error) {
      port.postMessage({
        success: false,
        error: error instanceof Error ? error.message : String(error)
      });
    }
  } else if (type === 'CLEAR_KEY') {
    privateKey = null;
    port.postMessage({ success: true });
  } else if (type === 'HAS_KEY') {
    port.postMessage({ success: true, hasKey: !!privateKey });
  } else if (type === 'SIGN_TEXT') {
    try {
      const signature = await signText(data.text);
      port.postMessage({ success: true, signature });
    } catch (error) {
      port.postMessage({
        success: false,
        error: error instanceof Error ? error.message : String(error)
      });
    }
  } else if (type === 'TEST_COMMUNICATION') {
    port.postMessage({ success: true, message: 'Service worker is ready' });
  }
});

registerRoute(
  ({ url }) => url.pathname.startsWith('/api/'),
  async ({ request }) => {
    try {
      const publicKeyId = request.headers.get('X-Syrinx-Public-Key-Id');
      const alreadySigned = request.headers.get('X-Syrinx-Signature');

      if (publicKeyId && !alreadySigned) {
        return fetch(await signRequest(request));
      }
      return fetch(request);
    } catch {
      return fetch(request);
    }
  }
);
