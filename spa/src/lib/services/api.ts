import type * as api from '$lib/types/api';
import { requestSigner } from './request-signer';
import { authService } from './auth';

export type SignReedResponse = {
  id: string;
  fingerprint: string;
  timestamp: string;
  algorithm: string;
  signature: string;
};

export type SignupInput = {
  username: string;
  publicKey: string;
  signature: string;
};

const BASE_URL = '/api';

// Unauthenticated endpoints that don't need signing.
// `/server/keys` is public verification material — required so clients can
// verify countersignatures even when their active user key is revoked
// (e.g. mid-rotation, right after RevokeKey).
const UNAUTHENTICATED_ENDPOINTS = [
  '/users/signup',
  '/check-username',
  '/keys',
  '/server/info',
  '/server/keys',
  '/recovery/identity/claim',
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
        if (!fingerprint || !passphrase) {
          throw new Error('Cannot sign request: active key or passphrase not available');
        }

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

  async getUserWithStatus(userId: string): Promise<{ status: number; user?: api.User }> {
    try {
      const user = await request<api.User>(`/users/${userId}`, { method: 'GET' });
      return { status: 200, user };
    } catch (error: any) {
      const match = error?.message?.match(/HTTP (\d+)/);
      return { status: match ? parseInt(match[1]) : 0 };
    }
  },
  // updateUser mints a fresh signed identity record for the caller.
  // Every accepted request is a *full* replacement of the signed
  // user-authored fields — partial patches are no longer supported.
  // Callers must pass the complete post-edit tuple
  // (username, avatarURL, bio) plus a base64(armored PGP) detached
  // signature over `buildUserIdentityPayload(username, fingerprint,
  // avatarURL, bio)`. The server uses byte-equality between the
  // submitted userSignature and the row's stored user_signature as a
  // no-op fast path, so a caller that resubmits the current identity
  // record's signature will get a 200 back with no state change.
  async updateUser(userData: {
    username: string;
    avatarURL: string;
    bio: string;
    userSignature: string;
  }): Promise<api.User> {
    const formData = new URLSearchParams();
    formData.append('username', userData.username);
    formData.append('avatarURL', userData.avatarURL);
    formData.append('bio', userData.bio);
    formData.append('userSignature', userData.userSignature);

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

  async createReed(reedId: string, signature: string): Promise<SignReedResponse> {
    const formData = new URLSearchParams();
    formData.append('signature', signature);
    formData.append('reedID', reedId);

    return request('/reeds', {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: formData.toString()
    });
  },

  async getReed(userId: string, reedId: string): Promise<any> {
    return request(`/reeds/${userId}/${reedId}`, { method: 'GET' });
  },

  // Ask the server to verify a stored (userSignature, serverSignature) pair
  // against the canonical payload for the reed. Used by e2e tests and,
  // eventually, by clients that want a second opinion on their own history.
  async verifyReed(userId: string, reedId: string, userSignature: string, serverSignature: string): Promise<void> {
    const formData = new FormData();
    formData.append('userSignature', userSignature);
    formData.append('serverSignature', serverSignature);
    return request(`/reeds/${userId}/${reedId}/verify`, {
      method: 'POST',
      body: formData,
    });
  },

  async deleteReed(userId: string, reedId: string): Promise<void> {
    return request(`/reeds/${userId}/${reedId}`, { method: 'DELETE' });
  },

  async revokeKey(
    userId: string,
    fingerprint: string,
    reason: string,
    userSignature: string
  ): Promise<api.PublicKey> {
    const formData = new URLSearchParams();
    formData.append('reason', reason);
    formData.append('userSignature', userSignature);

    return request<api.PublicKey>(`/users/${userId}/keys/${fingerprint}/revoke`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: formData.toString()
    });
  },

  async getKeyRevocation(userId: string, fingerprint: string): Promise<api.KeyRevocation> {
    return request<api.KeyRevocation>(
      `/users/${userId}/keys/${fingerprint}/revocation`,
      { method: 'GET' }
    );
  },

  async followUser(targetUserId: string): Promise<void> {
    return request<void>(`/users/${targetUserId}/follow`, { method: 'POST' });
  },

  async unfollowUser(targetUserId: string): Promise<void> {
    return request<void>(`/users/${targetUserId}/follow`, { method: 'DELETE' });
  },

  async getPublicKey(userID: string, fingerprint: string): Promise<api.PublicKey> {
    return request<api.PublicKey>(`/users/${userID}/keys/${fingerprint}`, { method: 'GET' });
  },

  /** Fetch a historical server signing public key by fingerprint. */
  async getServerPublicKey(fingerprint: string): Promise<{ fingerprint: string; armor: string }> {
    return request<{ fingerprint: string; armor: string }>(
      `/server/keys/${fingerprint}`,
      { method: 'GET' }
    );
  },

  async getUserReedIds(userId: string, from?: string): Promise<string[]> {
    const params = from ? `?from=${encodeURIComponent(from)}` : '';
    return request<string[]>(`/users/${userId}/reeds${params}`, { method: 'GET' });
  },

  async addPublicKey(
    userID: string,
    publicKey: string,
    revokedKeyFingerprint: string,
    revokedKeySignature: string,
    newKeySignature: string
  ): Promise<api.PublicKey> {
    const formData = new URLSearchParams();
    formData.append('userID', userID);
    formData.append('publicKey', publicKey);
    formData.append('revokedKeyFingerprint', revokedKeyFingerprint);
    formData.append('revokedKeySignature', revokedKeySignature);
    formData.append('newKeySignature', newKeySignature);

    return request<api.PublicKey>('/keys', {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: formData.toString()
    });
  }
};
