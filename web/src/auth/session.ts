import { ApiError, getMe } from '../api/client';

export const meQueryKey = ['me'] as const;

export async function loadCurrentUser() {
  try {
    return await getMe();
  } catch (error) {
    if (error instanceof ApiError && error.status === 401) return null;
    throw error;
  }
}
