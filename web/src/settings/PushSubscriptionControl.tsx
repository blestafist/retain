import { useEffect, useState } from 'react';
import { ApiError, createPushSubscription, deletePushSubscription, getPushVapidKey } from '../api/client';
import { t } from '../i18n/en';
import { Button } from '../ui/Button';

type PushState = 'checking' | 'unsupported' | 'disabled' | 'enabled' | 'denied';

function supportsPush(): boolean {
  return typeof window !== 'undefined'
    && 'serviceWorker' in navigator
    && 'PushManager' in window
    && 'Notification' in window;
}

function applicationServerKey(value: string): Uint8Array<ArrayBuffer> {
  const normalized = value.replace(/-/g, '+').replace(/_/g, '/');
  const decoded = window.atob(normalized.padEnd(Math.ceil(normalized.length / 4) * 4, '='));
  const bytes = new Uint8Array(decoded.length);
  for (let index = 0; index < decoded.length; index += 1) bytes[index] = decoded.charCodeAt(index);
  return bytes;
}

function pushError(error: unknown): string {
  if (error instanceof ApiError && error.code === 'push_unavailable') return t('pushUnavailable');
  if (error instanceof ApiError && error.code === 'push_subscription_conflict') return t('pushSubscriptionConflict');
  return t('pushSetupFailed');
}

export function PushSubscriptionControl({ reminderConfigured }: { reminderConfigured: boolean }) {
  const [state, setState] = useState<PushState>('checking');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    if (!supportsPush()) {
      setState('unsupported');
      return;
    }
    if (Notification.permission === 'denied') {
      setState('denied');
      return;
    }
    let cancelled = false;
    void navigator.serviceWorker.ready
      .then((registration) => registration.pushManager.getSubscription())
      .then((subscription) => {
        if (!cancelled) setState(subscription ? 'enabled' : 'disabled');
      })
      .catch(() => {
        if (!cancelled) setState('unsupported');
      });
    return () => { cancelled = true; };
  }, []);

  async function enable() {
    if (!supportsPush() || !reminderConfigured) return;
    setBusy(true);
    setError('');
    try {
      const permission = await Notification.requestPermission();
      if (permission !== 'granted') {
        setState(permission === 'denied' ? 'denied' : 'disabled');
        return;
      }
      const { public_key: publicKey } = await getPushVapidKey();
      const registration = await navigator.serviceWorker.ready;
      const subscription = await registration.pushManager.getSubscription()
        ?? await registration.pushManager.subscribe({
          userVisibleOnly: true,
          applicationServerKey: applicationServerKey(publicKey),
        });
      const json = subscription.toJSON();
      if (!json.keys?.p256dh || !json.keys.auth) throw new Error('Invalid browser subscription');
      await createPushSubscription({
        endpoint: subscription.endpoint,
        keys: { p256dh: json.keys.p256dh, auth: json.keys.auth },
      });
      setState('enabled');
    } catch (cause) {
      setError(pushError(cause));
    } finally {
      setBusy(false);
    }
  }

  async function disable() {
    if (!supportsPush()) return;
    setBusy(true);
    setError('');
    try {
      const registration = await navigator.serviceWorker.ready;
      const subscription = await registration.pushManager.getSubscription();
      if (subscription) {
        await deletePushSubscription(subscription.endpoint);
        if (!await subscription.unsubscribe()) throw new Error('Unable to unsubscribe');
      }
      setState('disabled');
    } catch (cause) {
      setError(pushError(cause));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="grid gap-3 rounded-2xl bg-surface-container p-4">
      <div>
        <h3 className="m-0 font-medium text-on-surface">{t('pushThisDevice')}</h3>
        <p className="m-0 mt-1 text-sm text-on-surface-variant">{t('pushThisDeviceHint')}</p>
      </div>
      {!reminderConfigured && <p className="m-0 text-sm text-on-surface-variant">{t('pushSaveReminderFirst')}</p>}
      {state === 'checking' && <p role="status" className="m-0 text-sm text-on-surface-variant">{t('loading')}</p>}
      {state === 'enabled' && <p role="status" className="m-0 text-sm text-on-surface-variant">{t('pushNotificationsEnabled')}</p>}
      {state === 'disabled' && <p role="status" className="m-0 text-sm text-on-surface-variant">{t('pushNotificationsDisabled')}</p>}
      {state === 'denied' && <p role="status" className="m-0 text-sm text-on-surface-variant">{t('pushPermissionDenied')}</p>}
      {state === 'unsupported' && <p role="status" className="m-0 text-sm text-on-surface-variant">{t('pushUnsupported')}</p>}
      <p className="m-0 text-sm text-on-surface-variant">{t('pushIOSHint')}</p>
      {state === 'disabled' && reminderConfigured && <Button type="button" onClick={() => void enable()} disabled={busy}>{t(busy ? 'enablingPushNotifications' : 'enablePushNotifications')}</Button>}
      {state === 'enabled' && <Button type="button" variant="tonal" onClick={() => void disable()} disabled={busy}>{t(busy ? 'disablingPushNotifications' : 'disablePushNotifications')}</Button>}
      {error && <p role="alert" className="m-0 text-sm text-error">{error}</p>}
    </div>
  );
}
