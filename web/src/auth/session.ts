import { ApiError, getMe } from '../api/client';
import { clearOfflineData, getCachedProfile, prepareOfflineOwner } from '../offline/database';

export const meQueryKey = ['me'] as const;

export async function loadCurrentUser() {
  try {
    const profile = await getMe();
    try {
      await prepareOfflineOwner(profile);
    } catch {
      // IndexedDB may be unavailable or full; the online app remains usable.
    }
    return profile;
  } catch (error) {
    if (error instanceof ApiError && error.status === 401) {
      try {
        await clearOfflineData();
      } catch {
        // A failed local cleanup must not change the server's auth result.
      }
      return null;
    }
    const browserOffline = typeof navigator !== 'undefined' && !navigator.onLine;
    if (browserOffline || error instanceof TypeError) {
      try {
        return await getCachedProfile();
      } catch {
        // Fall through and show the normal session error if storage is unavailable.
      }
    }
    throw error;
  }
}
