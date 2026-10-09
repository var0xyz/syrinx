/// <reference lib="webworker" />
/**
 * Service worker: PGP session for request signing + API fetch intercept.
 * OpenPGP is bundled from npm (openpgp/lightweight).
 *
 * The key is cached in memory. The page re-sends INIT_KEY after
 * focus/visibility when the OS has discarded the SW heap.
 */
import { precacheAndRoute, cleanupOutdatedCaches, createHandlerBoundToURL } from 'workbox-precaching';
import { registerRoute, NavigationRoute } from 'workbox-routing';
import * as openpgp from 'openpgp/lightweight';

declare let self: ServiceWorkerGlobalScope & {
  __WB_MANIFEST?: Array<string | { url: string; revision: string | null }>;
};

declare const __APP_VERSION__: string;

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

/**
 * Same-origin senders only; otherwise the key handlers are a signing
 * oracle. `event.origin` is '' for some clients, so fall back to the
 * client URL and reject when neither identifies the sender.
 */
function isSameOrigin(event: ExtendableMessageEvent): boolean {
  if (event.origin) {
    return event.origin === self.location.origin;
  }
  const source = event.source;
  if (source && 'url' in source && source.url) {
    try {
      return new URL(source.url).origin === self.location.origin;
    } catch {
      return false;
    }
  }
  return false;
}

self.addEventListener('message', (event) => {
  if (!isSameOrigin(event)) return;
  if (event.data?.type === 'SKIP_WAITING') {
    self.skipWaiting();
  }
});

async function initKey(armoredKey: string): Promise<void> {
  // Armor arrives unencrypted: at rest it is sealed by a non-extractable
  // wrapping key, and a PGP passphrase on top would have to live beside it.
  privateKey = await openpgp.readPrivateKey({ armoredKey });
}

/** Return cached decrypted key, or throw if the page has not (re)initialized. */
async function ensureDecryptedKey(): Promise<openpgp.PrivateKey> {
  if (privateKey) {
    return privateKey;
  }
  throw new Error('Private key not initialized');
}

async function signText(text: string | Uint8Array): Promise<string> {
  const key = await ensureDecryptedKey();
  const message = await openpgp.createMessage({
    binary: typeof text === 'string' ? new TextEncoder().encode(text) : text,
  });
  const signature = await openpgp.sign({
    message,
    signingKeys: key,
    detached: true,
    format: 'armored'
  });
  return (signature as string).trim();
}

/** Decrypt a message encrypted to this key (mailbox/relay delivery). */
async function decryptOwn(armored: string): Promise<string> {
  const key = await ensureDecryptedKey();
  const message = await openpgp.readMessage({ armoredMessage: armored });
  const { data } = await openpgp.decrypt({ message, decryptionKeys: key });
  return data as string;
}

/** method + path, the body bytes and the timestamp, as middlewares.go's
 * buildCanonicalRequestString joins them. */
function buildCanonicalRequestBytes(method: string, path: string, body: Uint8Array, timestamp: string): Uint8Array {
  const head = new TextEncoder().encode(`${method} ${path}\n\n`);
  const tail = new TextEncoder().encode(`\n\n${timestamp}`);
  const out = new Uint8Array(head.length + body.length + tail.length);
  out.set(head, 0);
  out.set(body, head.length);
  out.set(tail, head.length + body.length);
  return out;
}

function escapeSignature(signature: string): string {
  return signature.replace(/\n/g, '\\n');
}

async function signRequest(request: Request): Promise<Request> {
  const clone = request.clone();
  const method = clone.method;
  const url = new URL(clone.url);
  const path = url.pathname + (url.search || '');

  let body = new Uint8Array();
  if (method !== 'GET' && method !== 'HEAD') {
    body = new Uint8Array(await clone.arrayBuffer());
  }

  const timestamp = Math.floor(Date.now() / 1000).toString();
  const canonicalRequest = buildCanonicalRequestBytes(method, path, body, timestamp);
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
  if (!isSameOrigin(event)) return;
  if (!event.data?.type) return;

  const { type, data } = event.data;
  if (type === 'SKIP_WAITING') return;

  if (!event.ports?.[0]) return;
  const port = event.ports[0];

  if (type === 'INIT_KEY') {
    try {
      await initKey(data.armoredKey);
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
      const signature = await signText(data.bytes ?? data.text);
      port.postMessage({ success: true, signature });
    } catch (error) {
      port.postMessage({
        success: false,
        error: error instanceof Error ? error.message : String(error)
      });
    }
  } else if (type === 'DECRYPT_OWN') {
    try {
      const plaintext = await decryptOwn(data.armored);
      port.postMessage({ success: true, plaintext });
    } catch (error) {
      port.postMessage({
        success: false,
        error: error instanceof Error ? error.message : String(error)
      });
    }
  } else if (type === 'TEST_COMMUNICATION') {
    port.postMessage({ success: true, message: 'Service worker is ready' });
  } else if (type === 'GET_VERSION') {
    port.postMessage({ success: true, version: __APP_VERSION__ });
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
