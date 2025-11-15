import type * as api from '$lib/types/api';
import { requestSigner } from './request-signer';
import { authService } from './auth';

export type SignupInput = {
  username: string;
  publicKey: string;
  signature: string;
};

const BASE_URL = '/api';

// Unauthenticated endpoints that don't need signing
const UNAUTHENTICATED_ENDPOINTS = [
  '/users/signup',
  '/check-username',
  '/keys',
];

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  let signedInit = init;

  // Check if this is an authenticated request
  const isAuthenticated = !UNAUTHENTICATED_ENDPOINTS.some(endpoint => path.startsWith(endpoint));

  if (isAuthenticated) {
    try {
      // Check if request signer is initialized
      if (!requestSigner.isInitialized()) {
        // Try to get auth data from auth service
        const fingerprint = authService.getActiveKeyFingerprint();
        const passphrase = authService.getPassphrase();

        await requestSigner.initializeWorker(fingerprint, passphrase);
      }

      // Sign the request
      signedInit = await requestSigner.signRequest(`${BASE_URL}${path}`, init);
    } catch (error) {
      console.error('Failed to sign request:', error);
      throw error;
    }
  }

  const res = await fetch(`${BASE_URL}${path}`, signedInit);

  if (!res.ok) {
    // For 400 errors, try to parse the response as JSON to get the actual error message
    if (res.status === 400) {
      try {
        const errorData = await res.json();
        throw new Error(typeof errorData === 'string' ? errorData : errorData.message || 'Bad Request');
      } catch {
        // If JSON parsing fails, fall back to status text or generic message
        throw new Error(res.statusText || 'Bad Request');
      }
    }
    // For 401 errors, throw a specific authentication error
    if (res.status === 401) {
      throw new Error('Authentication failed. Please check your credentials.');
    }
    throw new Error(`HTTP ${res.status}`);
  }

  try {
    return (await res.json()) as T;
  } catch {
    return undefined as T;
  }
}

export const apiService = {
  async signup(input: SignupInput): Promise<api.User> {
    const formData = new URLSearchParams();
    formData.append('username', input.username);
    formData.append('publicKey', input.publicKey);
    formData.append('signature', input.signature);

    return request<api.User>('/users/signup', {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: formData.toString()
    });
  },
  async whoami() {
    return request<{ id: string; username: string }>('/users/me', { method: 'GET' });
  },
  async getUser(userId: string): Promise<api.User> {
    return request<api.User>(`/users/${userId}`, {
      method: 'GET'
    });
  },
  async updateUser(userData: { username?: string; avatarURL?: string; bio?: string }): Promise<api.User> {
    const formData = new URLSearchParams();
    if (userData.username) {
      formData.append('username', userData.username);
    }
    if (userData.avatarURL) {
      formData.append('avatarURL', userData.avatarURL);
    }
    if (userData.bio) {
      formData.append('bio', userData.bio);
    }

    return request<api.User>('/users/me', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: formData.toString()
    });
  },
  async deleteAccount(): Promise<void> {
    return request<void>('/users/me', { method: 'DELETE' });
  },
  async createUserKeys(userId: string, email: string): Promise<{ success: boolean }> {
    const formData = new URLSearchParams();
    formData.append('email', email);

    return request<{ success: boolean }>(`/users/${userId}/keys`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: formData.toString()
    });
  },

  async createReed(reedID: string, signature: string): Promise<any> {
    const formData = new URLSearchParams();
    formData.append('signature', signature);
    formData.append('reedID', reedID);

    return request('/reeds', {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: formData.toString()
    });
  },

  async getReed(userId: string, reedId: string): Promise<any> {
    return request(`/reeds/${userId}/${reedId}`, { method: 'GET' });
  },

  async deleteReed(userId: string, reedId: string): Promise<void> {
    return request(`/reeds/${userId}/${reedId}`, { method: 'DELETE' });
  },

  async revokeKey(userId: string, fingerprint: string, reason: string): Promise<void> {
    const formData = new URLSearchParams();
    formData.append('reason', reason);

    return request(`/users/${userId}/keys/${fingerprint}/revoke`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: formData.toString()
    });
  },

  async addPublicKey(
    userID: string,
    publicKey: string,
    revokedKeyFingerprint: string,
    revokedKeySignature: string,
    newKeySignature: string
  ): Promise<any> {
    const formData = new URLSearchParams();
    formData.append('userID', userID);
    formData.append('publicKey', publicKey);
    formData.append('revokedKeyFingerprint', revokedKeyFingerprint);
    formData.append('revokedKeySignature', revokedKeySignature);
    formData.append('newKeySignature', newKeySignature);

    return request('/keys', {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: formData.toString()
    });
  }
};
