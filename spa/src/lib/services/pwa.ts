import { get, writable } from 'svelte/store';

export const isInstalled = writable(false);
export const canInstall = writable(false);
export const isOnline = writable(
  typeof navigator !== 'undefined' ? navigator.onLine : true
);

type ReconnectListener = () => void;
const reconnectListeners = new Set<ReconnectListener>();

/** Fires when the device transitions from offline to online. */
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

function applyOnlineStatus(online: boolean): void {
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
  }
}

let deferredPrompt: any = null;
let pwaInitialized = false;

export function initializePWA() {
  if (pwaInitialized) {
    applyOnlineStatus(navigator.onLine);
    return;
  }
  pwaInitialized = true;

  // Check if app is already installed
  if (window.matchMedia('(display-mode: standalone)').matches) {
    isInstalled.set(true);
  }

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
  // Background tabs often miss 'online'; re-check when the page becomes visible.
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'visible') {
      updateOnlineStatus();
    }
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

    // Reload once when a new worker takes control (update path only).
    // Guard with localStorage + a time window: sessionStorage alone is not
    // always available (some PWA / private modes), and flip-flopping SW bytes
    // during a rolling deploy can fire controllerchange repeatedly.
    const hadController = !!navigator.serviceWorker.controller;
    const RELOAD_COUNT_KEY = 'syrinx:sw-reload-count';
    const RELOAD_TIME_KEY = 'syrinx:sw-reload-time';
    const RELOAD_WINDOW_MS = 60_000;
    const MAX_RELOADS_PER_WINDOW = 2;

    let controllerChangeTimer: ReturnType<typeof setTimeout> | null = null;

    function mayReloadForSWUpdate(): boolean {
      try {
        const now = Date.now();
        const last = Number(localStorage.getItem(RELOAD_TIME_KEY) || '0');
        let count = Number(localStorage.getItem(RELOAD_COUNT_KEY) || '0');
        if (!last || now - last > RELOAD_WINDOW_MS) {
          count = 0;
        }
        if (count >= MAX_RELOADS_PER_WINDOW) {
          console.warn(
            'PWA: Service Worker updated again within reload window; skipping reload to avoid a loop.'
          );
          return false;
        }
        localStorage.setItem(RELOAD_COUNT_KEY, String(count + 1));
        localStorage.setItem(RELOAD_TIME_KEY, String(now));
        return true;
      } catch {
        // Storage blocked — prefer a working stale shell over an infinite reload.
        return false;
      }
    }

    navigator.serviceWorker.addEventListener('controllerchange', () => {
      if (!hadController) return;
      if (controllerChangeTimer) return;
      controllerChangeTimer = setTimeout(() => {
        controllerChangeTimer = null;
        if (!mayReloadForSWUpdate()) return;
        window.location.reload();
      }, 100);
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
