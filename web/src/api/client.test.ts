import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError, getMe, login, logout, patchMeSettings, register } from './client';

const fetchMock = vi.fn<typeof fetch>();

function response(body?: unknown, status = 200, statusText?: string) {
  return new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    statusText,
    headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
  });
}

beforeEach(() => {
  fetchMock.mockReset();
  vi.stubGlobal('fetch', fetchMock);
});

afterEach(() => vi.unstubAllGlobals());

describe('auth API client', () => {
  it('uses the versioned API path and preserves the structured 401 error', async () => {
    fetchMock.mockResolvedValue(response({ error: { code: 'unauthorized', message: 'Sign in required.' } }, 401));

    await expect(getMe()).rejects.toMatchObject({
      name: 'ApiError',
      status: 401,
      code: 'unauthorized',
      serverMessage: 'Sign in required.',
    });
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/me', expect.objectContaining({ credentials: 'same-origin' }));
    const headers = new Headers(fetchMock.mock.calls[0]?.[1]?.headers);
    expect(headers.get('Accept')).toBe('application/json');
  });

  it('sends register, login, and logout requests to the confirmed routes', async () => {
    fetchMock
      .mockResolvedValueOnce(response(undefined, 201))
      .mockResolvedValueOnce(response(undefined, 204))
      .mockResolvedValueOnce(response(undefined, 204));

    await register({ invite: 'invite-123', email: 'friend@example.com', name: 'Friend', password: 'long-password-123' });
    await login({ email: 'friend@example.com', password: 'long-password-123' });
    await logout();

    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      '/api/v1/auth/register',
      '/api/v1/auth/login',
      '/api/v1/auth/logout',
    ]);
    expect(fetchMock.mock.calls[0]?.[1]).toEqual(expect.objectContaining({
      method: 'POST',
      credentials: 'same-origin',
      body: JSON.stringify({ invite: 'invite-123', email: 'friend@example.com', name: 'Friend', password: 'long-password-123' }),
    }));
    expect(fetchMock.mock.calls[1]?.[1]).toEqual(expect.objectContaining({
      method: 'POST',
      credentials: 'same-origin',
      body: JSON.stringify({ email: 'friend@example.com', password: 'long-password-123' }),
    }));
    expect(fetchMock.mock.calls[2]?.[1]).toEqual(expect.objectContaining({
      method: 'POST',
      credentials: 'same-origin',
      body: undefined,
    }));
  });

  it('sends settings as a partial flat snake_case object and returns the settings response', async () => {
    fetchMock.mockResolvedValue(response({ new_per_day: 12, max_reviews_per_day: 200 }));

    const result = await patchMeSettings({ new_per_day: 12 });

    expect(result).toEqual({ new_per_day: 12, max_reviews_per_day: 200 });
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/me/settings', expect.objectContaining({
      method: 'PATCH',
      body: JSON.stringify({ new_per_day: 12 }),
      credentials: 'same-origin',
    }));
  });

  it('falls back safely when an error response does not use the API envelope', async () => {
    fetchMock.mockResolvedValue(new Response('<html>Unavailable</html>', { status: 503, statusText: 'Service Unavailable' }));

    const error = await getMe().catch((caught: unknown) => caught);

    expect(error).toBeInstanceOf(ApiError);
    expect(error).toMatchObject({ status: 503, code: 'request_failed', serverMessage: 'Service Unavailable' });
  });
});
