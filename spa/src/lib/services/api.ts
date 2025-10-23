import type * as api from '$lib/types/api';
import { requestSigner } from './request-signer';
import { authService } from './auth';
import { sessionStore } from '$lib/stores/session';

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
];

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  let signedInit = init;

  // Check if this is an authenticated request
  const isAuthenticated = !UNAUTHENTICATED_ENDPOINTS.some(endpoint => path.startsWith(endpoint));

  if (isAuthenticated) {
    try {
      // Check if request signer is initialized
      if (!requestSigner.isInitialized()) {
        // Try to get session data from memory
        const fingerprint = sessionStore.get('fingerprint');
        const passphrase = sessionStore.get('passphrase');

        if (fingerprint && passphrase) {
          // We have session data, try to initialize
          await requestSigner.initializeWorker(fingerprint, passphrase);
        } else {
          // No session data, user needs to enter passphrase
          throw new Error('Authentication required. Please enter your passphrase.');
        }
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
  async createUserKeys(userId: string, email: string): Promise<{ success: boolean }> {
    const formData = new URLSearchParams();
    formData.append('email', email);

    return request<{ success: boolean }>(`/users/${userId}/keys`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: formData.toString()
    });
  },

  async createReed(signature: string): Promise<any> {
    const formData = new URLSearchParams();
    formData.append('signature', signature);

    return request('/reeds', {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: formData.toString()
    });
  },

  async getReed(reedId: string): Promise<any> {
    return request(`/reeds/${reedId}`, { method: 'GET' });
  }
};