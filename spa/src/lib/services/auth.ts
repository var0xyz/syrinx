import { get } from 'svelte/store';
import type * as api from '$lib/types/api';
import { requestSigner } from './request-signer';
import { isImportInProgress } from './importRun';
import { isRecoveryInProgress } from './recoveryRun';
import { serverConnection } from './serverConnection';
import { refreshServerInfo, serverInfo } from './serverInfo';

interface SignupUser {
  username: string;
  publicKey: string;
  signature: string;
  userSignature: string;
  userID: string;
  userIDSignature: string;
  userIDFingerprint: string;
  inviteID?: string;
  inviteSecret?: string;
}

export class AuthService {
  private _user: api.User | null = null;

  /**
   * Identity material is present locally (userId). Does not mean the user
   * has a finished session — mid-recovery also has userId after backup write.
   */
  hasLocalIdentity(): boolean {
    return !!localStorage.getItem('userId');
  }

  /**
   * Usable finished session: local identity present, import not mid-run, and
   * recovery not mid-run. Uses only localStorage markers — no network.
   */
  isLoggedIn(): boolean {
    return (
      this.hasLocalIdentity() &&
      !isImportInProgress() &&
      !isRecoveryInProgress()
    );
  }

  /**
   * Get the current user from IndexedDB, falling back to the in-memory
   * session user (e.g. right after signup, before verify-on-put can
   * succeed because the attested public key is not cached yet).
   */
  async getCurrentUser(): Promise<api.User | null> {
    try {
      const userId = localStorage.getItem('userId');
      if (!userId) {
        this._user = null;
        return null;
      }

      const { userRepository } = await import('$lib/repositories/user');
      const user = await userRepository.get(userId);
      if (user) {
        this._user = user;
        return user;
      }
      if (this._user?.id === userId) {
        return this._user;
      }
      this._user = null;
    } catch (error) {
      // Keep in-memory user if IDB read fails mid-signup.
      if (this._user && localStorage.getItem('userId') === this._user.id) {
        return this._user;
      }
      this._user = null;
    }

    return this._user;
  }

  /**
   * Save user data to both localStorage and IndexedDB
   */
  async saveUserToStorage(user: api.User): Promise<void> {
    localStorage.setItem('userId', user.id);
    this._user = user;

    // Also store in IndexedDB (verify-on-put needs the attested public key
    // already cached — callers that run before that must tolerate failure).
    try {
      const { userRepository } = await import('$lib/repositories/user');
      await userRepository.put(user);
    } catch (error) {
      console.error('Failed to store user in IndexedDB:', error);
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
   * Get the server name from cached server info, refreshing if needed.
   */
  async getServerName(): Promise<string> {
    let info = get(serverInfo);
    if (!info) {
      info = await refreshServerInfo();
    }
    if (!info) {
      throw new Error('Failed to fetch server info');
    }
    return info.name;
  }

  /**
   * Signup the user and store the user ID
   */
  async signup(userData: SignupUser): Promise<api.User> {
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
    formData.append('userSignature', userData.userSignature);
    formData.append('userID', userData.userID);
    formData.append('userIDSignature', userData.userIDSignature);
    formData.append('userIDFingerprint', userData.userIDFingerprint);
    if (userData.inviteID) {
      formData.append('inviteID', userData.inviteID);
    }
    if (userData.inviteSecret) {
      formData.append('inviteSecret', userData.inviteSecret);
    }

    const response = await fetch('/api/users/signup', {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: formData.toString()
    });

    if (!response.ok) {
      let message = `HTTP ${response.status}`;
      try {
        const body = await response.json();
        if (typeof body === 'string' && body.trim() !== '') {
          message = body;
        }
      } catch {
        // keep status message
      }
      throw new Error(message);
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

    serverConnection.disconnect();

    // Reset user state
    this._user = null;
  }
}

export const authService = new AuthService();
