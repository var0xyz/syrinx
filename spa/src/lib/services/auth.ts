import type * as api from '$lib/types/api';
import { requestSigner } from './request-signer';
import { websocketService } from './websocket';

interface SignupUser {
  username: string;
  publicKey: string;
  signature: string;
}

export class AuthService {
  private _user: api.User | null = null;

  isLoggedIn(): boolean {
    return !!localStorage.getItem('userId');
  }

  /**
   * Get the current user from localStorage
   */
  async getCurrentUser(): Promise<api.User | null> {
    try {
      // Check if user ID exists in localStorage
      const userId = localStorage.getItem('userId');
      if (!userId) {
        this._user = null;
        return null;
      }

      const { userRepository } = await import('$lib/repositories/user');
      const user = await userRepository.get(userId);
      this._user = user;
    } catch (error) {
      this._user = null;
    }

    return this._user;
  }

  /**
   * Save user data to both localStorage and IndexedDB
   */
  async saveUserToStorage(user: api.User): Promise<void> {
    localStorage.setItem('userId', user.id);

    // Also store in IndexedDB
    try {
      const { userRepository } = await import('$lib/repositories/user');
      await userRepository.put(user);
      this._user = user;
    } catch (error) {
      console.error('Failed to store user in IndexedDB:', error);
      // Don't throw - localStorage is the primary storage
    }
  }

  /**
   * Set the active key fingerprint
   */
  setActiveKey(fingerprint: string): void {
    localStorage.setItem('keyFingerprint', fingerprint);
  }

  /**
   * Get the active key fingerprint
   */
  getActiveKeyFingerprint(): string | null {
    return localStorage.getItem('keyFingerprint');
  }

  /**
   * Set the passphrase in localStorage
   */
  setPassphrase(passphrase: string): void {
    localStorage.setItem('keyPassphrase', passphrase);
  }

  /**
   * Get the passphrase from localStorage
   */
  getPassphrase(): string | null {
    return localStorage.getItem('keyPassphrase');
  }

  /**
   * Get the server name from the /api/server/info endpoint
   */
  async getServerName(): Promise<string> {
    const response = await fetch('/api/server/info');

    if (!response.ok) {
      throw new Error(`HTTP ${response.status}`);
    }

    const server = await response.json();
    localStorage.setItem('serverId', server.id);
    return server.name;
  }

  /**
   * Signup the user and store the user ID
   */
  async signup(userData: SignupUser): Promise<string> {
    try {
      const serverName = await this.getServerName();
      localStorage.setItem('serverName', serverName);
    } catch (error) {
      throw new Error('Failed to fetch server info', { cause: error });
    }

    const formData = new URLSearchParams();
    formData.append('username', userData.username);
    formData.append('publicKey', userData.publicKey);
    formData.append('signature', userData.signature);

    const response = await fetch('/api/users/signup', {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: formData.toString()
    });

    if (!response.ok) {
      throw new Error(`HTTP ${response.status}`);
    }

    const user = await response.json();
    localStorage.setItem('userId', user.id);
    this._user = user;

    return user;
  }

  /**
   * Clear service worker session and disconnect WebSocket
   * Note: localStorage and IndexedDB clearing should be handled separately
   * via clearLocalStorage() and dbService.deleteDatabase()
   */
  async clearSession(): Promise<void> {
    // Clear service worker session
    try {
      await requestSigner.clearSession();
    } catch (error) {
      console.error('Failed to clear request signer session:', error);
      // Continue even if this fails
    }

    // Disconnect WebSocket if connected
    if (websocketService.isConnected()) {
      websocketService.disconnect();
    }

    // Reset user state
    this._user = null;
  }
}

export const authService = new AuthService();
