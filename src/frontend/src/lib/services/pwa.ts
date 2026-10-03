import { get, writable } from 'svelte/store';

export const isInstalled = writable(
  typeof window !== 'undefined' ? isRunningAsPWA() : false
);
export const canInstall = writable(false);
export const isOnline = writable(
  typeof navigator !== 'undefined' ? navigator.onLine : true
);
/** A new service worker has taken control of this page — the running app
 * is stale. Set once by the controllerchange handler below; cleared only
 * by reloading (applyUpdate), never automatically. */
export const updateAvailable = writable(false);

type ReconnectListener = () => void;
const reconnectListeners = new Set<ReconnectListener>();

/** Fires when the device transitions from offline to online, or the app
 * comes back to the foreground (the OS may have killed its socket). */
export function onReconnect(listener: ReconnectListener): () => void {
  reconnectListeners.add(listener);
  return () => reconnectListeners.delete(listener);
}

function notifyReconnect(): void {
  for (const listener of reconnectListeners) {
    try {
      listener();
    } catch (error) {
      console.error('Reconnect listener failed:', error);
    }
  }
}

/** Returns whether this call fired the reconnect listeners. */
function applyOnlineStatus(online: boolean): boolean {
  const wasOnline = get(isOnline);
  isOnline.set(online);

  const body = document.body;
  if (body) {
    if (online) {
      body.setAttribute('data-sveltekit-preload-data', 'hover');
      body.removeAttribute('data-sveltekit-reload');
    } else {
      body.setAttribute('data-sveltekit-preload-data', 'off');
      body.setAttribute('data-sveltekit-reload', '');
    }
  }

  if (online && !wasOnline) {
    notifyReconnect();
    return true;
  }
  return false;
}

let deferredPrompt: any = null;
let pwaInitialized = false;

export function initializePWA() {
  if (pwaInitialized) {
    applyOnlineStatus(navigator.onLine);
    return;
  }
  pwaInitialized = true;

  // Listen for beforeinstallprompt event
  window.addEventListener('beforeinstallprompt', (e) => {
    console.log('PWA: Install prompt available');
    e.preventDefault();
    deferredPrompt = e;
    canInstall.set(true);
  });

  // Listen for appinstalled event
  window.addEventListener('appinstalled', () => {
    console.log('PWA: App installed');
    isInstalled.set(true);
    canInstall.set(false);
    deferredPrompt = null;
  });

  // Listen for online/offline events
  const updateOnlineStatus = () => applyOnlineStatus(navigator.onLine);

  window.addEventListener('online', updateOnlineStatus);
  window.addEventListener('offline', updateOnlineStatus);
  // Background tabs often miss 'online', and a backgrounded app's socket
  // may be dead while still reporting OPEN: resync on every return.
  let wasHidden = document.visibilityState === 'hidden';
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState !== 'visible') {
      wasHidden = true;
      return;
    }
    const notified = updateOnlineStatus();
    if (wasHidden && !notified && navigator.onLine) {
      notifyReconnect();
    }
    wasHidden = false;
  });
  updateOnlineStatus();

  // Register service worker immediately.
  // Built SW is an IIFE that still contains import.meta.url (from openpgp);
  // it must be registered as a module in both dev and production.
  if ('serviceWorker' in navigator) {
    const swUrl = '/service-worker.js';
    const swOptions: RegistrationOptions = {
      type: 'module',
      updateViaCache: 'none'
    };

    // WebKit fires controllerchange on plain reloads even when the same
    // worker re-claims its clients, not just on genuine updates. Confirm via
    // the controller's actual build version before showing the banner.
    const hadController = !!navigator.serviceWorker.controller;
    let reloadPending = false;

    navigator.serviceWorker.addEventListener('controllerchange', () => {
      if (!hadController || reloadPending) return;
      const controller = navigator.serviceWorker.controller;
      if (!controller) return;

      workerVersion(controller).then((version) => {
        if (version && version !== __APP_VERSION__) {
          reloadPending = true;
          updateAvailable.set(true);
        }
      });
    });

    navigator.serviceWorker.register(swUrl, swOptions)
    .then((registration) => {
      console.log('PWA: Service Worker registered');

      // install handler in service-worker.ts already calls skipWaiting(); only
      // nudge a worker that was left waiting from a previous visit.
      registration.addEventListener('updatefound', () => {
        const newWorker = registration.installing;
        if (!newWorker) return;
        newWorker.addEventListener('statechange', () => {
          if (newWorker.state === 'installed' && navigator.serviceWorker.controller) {
            newWorker.postMessage({ type: 'SKIP_WAITING' });
          }
        });
      });

      if (registration.waiting && navigator.serviceWorker.controller) {
        registration.waiting.postMessage({ type: 'SKIP_WAITING' });
      }

      // Check for a new version at startup. Fails soft when offline —
      // the existing precache keeps serving the app.
      registration.update().catch((error) => {
        console.log('PWA: Update check skipped:', error);
      });
    })
    .catch((error) => {
      console.error('PWA: Service Worker registration failed:', error);
    });
  }
}

// Resolves undefined if the worker never answers, e.g. one that throws.
function workerVersion(worker: ServiceWorker): Promise<string | undefined> {
  return new Promise((resolve) => {
    const timer = setTimeout(() => resolve(undefined), 3000);
    const channel = new MessageChannel();
    channel.port1.onmessage = (event) => {
      clearTimeout(timer);
      resolve(event.data?.success ? event.data.version : undefined);
    };
    worker.postMessage({ type: 'GET_VERSION' }, [channel.port2]);
  });
}

export type UpdateCheckResult = 'updating' | 'up-to-date' | 'unsupported';

/** Asks the server for a newer version now. One that's found installs and
 * takes over, and the controllerchange handler then shows the banner. */
export async function checkForUpdates(): Promise<UpdateCheckResult> {
  if (!('serviceWorker' in navigator)) return 'unsupported';
  const registration = await navigator.serviceWorker.getRegistration();
  if (!registration) return 'unsupported';

  await registration.update();
  if (registration.installing || registration.waiting) return 'updating';

  // A newer worker may already control the page without the banner showing.
  const controller = navigator.serviceWorker.controller;
  const version = controller ? await workerVersion(controller) : undefined;
  if (version && version !== __APP_VERSION__) {
    updateAvailable.set(true);
    return 'updating';
  }
  return 'up-to-date';
}

/** Reload onto the new version — called by the update banner's button. */
export function applyUpdate(): void {
  window.location.reload();
}

export async function installPWA(): Promise<boolean> {
  if (!deferredPrompt) {
    console.log('PWA: Install prompt not available');
    return false;
  }

  try {
    // Show the install prompt
    deferredPrompt.prompt();

    // Wait for the user to respond to the prompt
    const { outcome } = await deferredPrompt.userChoice;

    if (outcome === 'accepted') {
      console.log('PWA: User accepted the install prompt');
      canInstall.set(false);
      return true;
    } else {
      console.log('PWA: User dismissed the install prompt');
      return false;
    }
  } catch (error) {
    console.error('PWA: Error during installation', error);
    return false;
  } finally {
    deferredPrompt = null;
  }
}

export function checkInstallability(): boolean {
  return deferredPrompt !== null;
}

// Utility function to check if running as PWA
export function isRunningAsPWA(): boolean {
  return window.matchMedia('(display-mode: standalone)').matches ||
         (window.navigator as any).standalone === true ||
         document.referrer.includes('android-app://');
}

// Utility function to get device type
export function getDeviceType(): 'mobile' | 'tablet' | 'desktop' {
  const userAgent = navigator.userAgent;
  const isMobile = /Android|webOS|iPhone|iPad|iPod|BlackBerry|IEMobile|Opera Mini/i.test(userAgent);
  const isTablet = /iPad|Android(?=.*\bMobile\b)/i.test(userAgent);

  if (isTablet) return 'tablet';
  if (isMobile) return 'mobile';
  return 'desktop';
}

// Utility function to request persistent storage
export async function requestPersistentStorage(): Promise<boolean> {
  if ('storage' in navigator && 'persist' in navigator.storage) {
    try {
      const isPersistent = await navigator.storage.persist();
      console.log('PWA: Persistent storage granted:', isPersistent);
      return isPersistent;
    } catch (error) {
      console.error('PWA: Error requesting persistent storage', error);
      return false;
    }
  }
  return false;
}

// Utility function to get storage quota
export async function getStorageQuota(): Promise<{ used: number; total: number } | null> {
  if ('storage' in navigator && 'estimate' in navigator.storage) {
    try {
      const estimate = await navigator.storage.estimate();
      console.log('estimate:', estimate);
      return {
        used: estimate.usage || 0,
        total: estimate.quota || 0
      };
    } catch (error) {
      console.error('PWA: Error getting storage quota', error);
      return null;
    }
  }
  return null;
}
