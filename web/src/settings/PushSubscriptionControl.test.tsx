// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { t } from '../i18n/en';
import { PushSubscriptionControl } from './PushSubscriptionControl';

const fetchMock = vi.fn<typeof fetch>();

function jsonResponse(body: unknown) {
  return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } });
}

function stubPushBrowser(subscription: PushSubscription | null) {
  const pushManager = {
    getSubscription: vi.fn(async () => subscription),
    subscribe: vi.fn(async () => subscription ?? {
      endpoint: 'https://fcm.googleapis.com/fcm/send/example',
      toJSON: () => ({ keys: { p256dh: 'public-key', auth: 'auth-key' } }),
      unsubscribe: vi.fn(async () => true),
    }),
  };
  Object.defineProperty(navigator, 'serviceWorker', {
    configurable: true,
    value: { ready: Promise.resolve({ pushManager }) },
  });
  vi.stubGlobal('PushManager', {});
  vi.stubGlobal('Notification', { permission: 'default', requestPermission: vi.fn(async () => 'granted') });
  return pushManager;
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  Reflect.deleteProperty(navigator, 'serviceWorker');
  fetchMock.mockReset();
});

describe('PushSubscriptionControl', () => {
  it('requests permission and registers the browser subscription with the server', async () => {
    const manager = stubPushBrowser(null);
    fetchMock.mockImplementation(async (input, init) => {
      if (String(input) === '/api/v1/push/vapid-key') return jsonResponse({ public_key: 'BA' });
      if (String(input) === '/api/v1/push/subscribe' && init?.method === 'POST') return new Response(null, { status: 204 });
      throw new Error(`Unexpected request: ${String(input)}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<PushSubscriptionControl reminderConfigured />);
    const enable = await screen.findByRole('button', { name: t('enablePushNotifications') });
    fireEvent.click(enable);

    expect(await screen.findByText(t('pushNotificationsEnabled'))).toBeTruthy();
    expect(manager.subscribe).toHaveBeenCalledWith(expect.objectContaining({ userVisibleOnly: true }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/v1/push/subscribe', expect.objectContaining({ method: 'POST' })));
    expect(screen.getByText(t('pushIOSHint'))).toBeTruthy();
  });

  it('removes the current endpoint when notifications are disabled', async () => {
    const subscription = {
      endpoint: 'https://fcm.googleapis.com/fcm/send/example',
      toJSON: () => ({ keys: { p256dh: 'public-key', auth: 'auth-key' } }),
      unsubscribe: vi.fn(async () => true),
    } as unknown as PushSubscription;
    stubPushBrowser(subscription);
    fetchMock.mockImplementation(async (input, init) => {
      if (String(input) === '/api/v1/push/subscribe' && init?.method === 'DELETE') return new Response(null, { status: 204 });
      throw new Error(`Unexpected request: ${String(input)}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<PushSubscriptionControl reminderConfigured />);
    fireEvent.click(await screen.findByRole('button', { name: t('disablePushNotifications') }));

    expect(await screen.findByText(t('pushNotificationsDisabled'))).toBeTruthy();
    expect(subscription.unsubscribe).toHaveBeenCalledOnce();
    expect(JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body))).toEqual({ endpoint: subscription.endpoint });
  });
});
