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

  const deviceID = getDeviceID();
  if (!deviceID) {
    const newDeviceID = window.crypto.randomUUID();
    setDeviceID(newDeviceID);
    console.log('PWA: New device ID generated:', newDeviceID);
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

  // Register service worker immediately
  if ('serviceWorker' in navigator) {
    navigator.serviceWorker.register('/sw.js')
      .then((registration) => {
        console.log('PWA: Service Worker registered', registration);

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

// Biometric authentication functions
export async function isBiometricSupported(): Promise<boolean> {
  return 'credentials' in navigator && 'create' in navigator.credentials;
}

export async function createBiometricCredential(userId: string, username: string): Promise<boolean> {
  if (!await isBiometricSupported()) {
    alert('Biometric authentication is not supported on this device');
    return false;
  }

  // Validate required parameters
  if (!userId || !username) {
    console.error('PWA: Missing required parameters - userId:', userId, 'username:', username);
    return false;
  }

  try {
    console.log('PWA: Creating credential with userId:', userId, 'username:', username);

    // Generate a deterministic, short user ID for WebAuthn (max 64 bytes)
    const userIdBytes = new TextEncoder().encode(userId);
    let webauthnUserId = userIdBytes;

    // If userId is too long, hash it to create a shorter, deterministic ID
    if (userIdBytes.length > 64) {
      const hashBuffer = await crypto.subtle.digest('SHA-256', userIdBytes);
      webauthnUserId = new Uint8Array(hashBuffer);
    }

    const credentialOptions = {
      publicKey: {
        challenge: new Uint8Array(32),
        rp: {
          name: 'Syrinx',
          id: window.location.hostname
        },
        user: {
          id: webauthnUserId,
          name: username,
          displayName: username
        },
        pubKeyCredParams: [
          { type: 'public-key' as const, alg: -7 }, // ES256
          { type: 'public-key' as const, alg: -257 } // RS256
        ],
        authenticatorSelection: {
          authenticatorAttachment: 'platform' as AuthenticatorAttachment,
          userVerification: 'required' as UserVerificationRequirement
        },
        timeout: 60000,
        attestation: 'direct' as AttestationConveyancePreference
      }
    };

    console.log('PWA: Credential options:', JSON.stringify(credentialOptions, null, 2));

    const credential = await navigator.credentials.create(credentialOptions);

    if (credential) {
      // Store the credential ID for later use
      localStorage.setItem('biometric_credential_id', userId);
      return true;
    }
    return false;
  } catch (error) {
    alert(error);
    console.error('PWA: Error creating biometric credential', error);
    return false;
  }
}

export async function authenticateWithBiometric(): Promise<boolean> {
  if (!await isBiometricSupported()) {
    return false;
  }

  try {
    const credential = await navigator.credentials.get({
      publicKey: {
        challenge: new Uint8Array(32),
        timeout: 60000,
        userVerification: 'required'
      }
    });

    return !!credential;
  } catch (error) {
    console.error('PWA: Error authenticating with biometric', error);
    return false;
  }
}

export async function isBiometricEnabled(): Promise<boolean> {
  return localStorage.getItem('biometric_credential_id') !== null;
}

export async function disableBiometric(): Promise<void> {
  localStorage.removeItem('biometric_credential_id');
}

export function getDeviceID(): string | null {
  return localStorage.getItem('device.id') || null;
}

function setDeviceID(deviceID: string): void {
  localStorage.setItem('device.id', deviceID);
}