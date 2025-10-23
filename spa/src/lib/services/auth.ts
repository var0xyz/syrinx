import type * as api from '$lib/types/api';
import type * as db from '$lib/types/db';

interface SignupUser {
  username: string;
  publicKey: string;
  signature: string;
}

export class AuthService {
  private _user: db.User | null = null;

  /**
   * Get the current user from localStorage
   */
  async getCurrentUser(): Promise<db.User | null> {
    try {
      // Check if user ID exists in localStorage
      const userId = localStorage.getItem('user.id');
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
  async saveUserToStorage(user: api.User): Promise<db.User> {
    localStorage.setItem('user.id', user.id);

    // Also store in IndexedDB
    try {
      const { userRepository } = await import('$lib/repositories/user');
      const storedUser = await userRepository.put(user);
      this._user = storedUser;
      return storedUser;
    } catch (error) {
      console.error('Failed to store user in IndexedDB:', error);
      // Don't throw - localStorage is the primary storage
    }
  }

  /**
   * Set the active key fingerprint
   */
  setActiveKey(fingerprint: string): void {
    localStorage.setItem('user.activeKeyFingerprint', fingerprint);
  }

  /**
   * Get the active key fingerprint
   */
  getActiveKeyFingerprint(): string | null {
    return localStorage.getItem('user.activeKeyFingerprint');
  }

  /**
   * Set the authentication method used during signup
   */
  setAuthMethod(method: 'password' | 'biometric'): void {
    localStorage.setItem('user.authMethod', method);
  }

  /**
   * Get the authentication method used during signup
   */
  getAuthMethod(): 'password' | 'biometric' | null {
    const method = localStorage.getItem('user.authMethod');
    return method === 'password' || method === 'biometric' ? method : null;
  }

  /**
   * Signup the user and store the user ID
   */
  async signup(userData: SignupUser): Promise<string> {
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
    localStorage.setItem('user.id', user.id);
    this._user = user;

    return user;
  }
}

export const authService = new AuthService();