import { writable } from 'svelte/store';
import { apiService } from './api';
import { authService } from './auth';
import { signedUsername } from '$lib/verifiers';

/** The username this user lost to an older claim, while they haven't
 * picked a new one; the app is blocked until they do. */
export const lostUsername = writable<string | null>(null);

/** Checks this user's own profile on the server for a lost username. */
export async function checkOwnUsername(): Promise<void> {
  const userID = localStorage.getItem('userId');
  if (!userID || !authService.isLoggedIn()) return;
  try {
    const user = await apiService.getUserProfile(userID);
    if (user.username) {
      lostUsername.set(null);
      return;
    }
    const name = await signedUsername(user);
    if (name === null) return;
    await authService.saveUserToStorage(user);
    lostUsername.set(name);
  } catch (error) {
    console.warn('usernameCollision: own profile check failed:', error);
  }
}
