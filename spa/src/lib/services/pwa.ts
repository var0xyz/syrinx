import { writable } from 'svelte/store';

export const isInstalled = writable(false);
export const canInstall = writable(false);
export const isOnline = writable(true);

let deferredPrompt: any = null;

export function initializePWA() {
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
  const updateOnlineStatus = () => {
    const online = navigator.onLine;
    isOnline.set(online);

    // Update preload behavior based on online status
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
  };

  window.addEventListener('online', updateOnlineStatus);
  window.addEventListener('offline', updateOnlineStatus);
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
    navigator.serviceWorker.register(swUrl, swOptions)
    .then((registration) => {
      console.log('PWA: Service Worker registered');

      // Force update on registration
      registration.update();

      // Wait for service worker to be ready
      if (registration.installing) {
        console.log('PWA: Service worker is installing...');
        registration.installing.addEventListener('statechange', () => {
          if (registration.installing?.state === 'installed') {
            console.log('PWA: Service worker installed, waiting for activation...');
          }
        });
      }

      if (registration.waiting) {
        console.log('PWA: Service worker is waiting, activating...');
        registration.waiting.postMessage({ type: 'SKIP_WAITING' });
      }

      if (registration.active) {
        console.log('PWA: Service worker is active');
      }

      // Listen for updates
      registration.addEventListener('updatefound', () => {
        const newWorker = registration.installing;
        if (newWorker) {
          newWorker.addEventListener('statechange', () => {
            if (newWorker.state === 'installed' && navigator.serviceWorker.controller) {
              // New content is available, show update notification
              console.log('PWA: New content available');
              // You can dispatch a custom event or update a store here
            }
          });
        }
      });
    })
    .catch((error) => {
      console.error('PWA: Service Worker registration failed:', error);
      navigator.serviceWorker.register(swUrl, swOptions).catch((fallbackError) => {
        console.error('PWA: Fallback registration also failed:', fallbackError);
      });
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
