import { lazy, Suspense, useEffect, useState, type ReactNode } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import * as DropdownMenu from '@radix-ui/react-dropdown-menu';
import { ApiError, logout, type MeResponse } from './api/client';
import { AuthForms } from './auth/AuthForms';
import { loadCurrentUser, meQueryKey } from './auth/session';
import './app.css';
import { t } from './i18n/en';
import { Button } from './ui/Button';
import { DecksWorkspace } from './content/DecksWorkspace';
import { AdminPage } from './admin/AdminPage';
import { SettingsPage } from './settings/SettingsPage';
import { StudySession } from './study/StudySession';
import { clearOfflineData } from './offline/database';
import { synchronizeOfflineData } from './offline/sync';
import { AppNavigation } from './AppNavigation';
import { useTheme } from './theme/context';
import { themeFromServer } from './theme/settings';

const DevShowcase = import.meta.env.DEV ? lazy(() => import('./dev/Showcase')) : null;
const StatsPage = lazy(async () => ({ default: (await import('./stats/StatsPage')).StatsPage }));
const FriendsPage = lazy(async () => ({ default: (await import('./social/FriendsPage')).FriendsPage }));

export function isDevShowcase(dev: boolean, pathname: string) {
  return dev && pathname === '/dev/ui';
}

export function App() {
  if (DevShowcase && isDevShowcase(import.meta.env.DEV, window.location.pathname)) {
    return <Suspense fallback={null}><DevShowcase /></Suspense>;
  }

  return <AuthenticatedApp />;
}

function AppBrand() {
  return (
    <a className="app-brand" href="/" aria-label={t('appName')}>
      <svg className="app-brand-mark" viewBox="0 0 36 36" aria-hidden="true" focusable="false">
        <rect x="5" y="4" width="16" height="22" rx="4" transform="rotate(-12 13 15)" fill="currentColor" opacity="0.35" />
        <rect x="13" y="10" width="16" height="22" rx="4" fill="currentColor" />
        <path d="M17 17h8m-8 5h5" fill="none" strokeWidth="1.75" strokeLinecap="round" />
      </svg>
      <span className="app-name">{t('appName')}</span>
    </a>
  );
}

function AccountMenu({ user, signingOut, onSignOut }: {
  user: MeResponse['user'];
  signingOut: boolean;
  onSignOut: () => void;
}) {
  const initial = Array.from(user.name.trim() || user.email)[0]?.toLocaleUpperCase('en');

  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger
        className="app-account-trigger"
        aria-label={`${t('signedIn')}: ${user.name}`}
        aria-busy={signingOut}
      >
        <span className="app-account-avatar" aria-hidden="true">{initial}</span>
        <span className="app-account-trigger-name" aria-hidden="true">{user.name}</span>
        <svg className="app-account-chevron" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" focusable="false">
          <path d="m8 10 4 4 4-4" />
        </svg>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content className="app-account-menu" align="end" sideOffset={8} collisionPadding={16}>
          <DropdownMenu.Label className="app-account-identity">
            <span className="app-account-caption">{t('signedIn')}</span>
            <span className="app-user-name">{user.name}</span>
            <span className="app-user-email">{user.email}</span>
          </DropdownMenu.Label>
          <DropdownMenu.Separator className="app-account-separator" />
          <DropdownMenu.Item className="app-account-menu-item" disabled={signingOut} onSelect={onSignOut}>
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" focusable="false">
              <path d="M10 4H5v16h5m5-12 4 4-4 4m-6-4h14" />
            </svg>
            {t(signingOut ? 'signingOut' : 'signOut')}
          </DropdownMenu.Item>
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
}

function AppFrame({ children, signedIn = false }: { children: ReactNode; signedIn?: boolean }) {
  return (
    <div className={`app-shell${signedIn ? ' app-shell-signed-in' : ''}`}>
      <a className="skip-link" href="#main-content">
        {t('skipToMain')}
      </a>

      {!signedIn && <header className="app-header" aria-label={t('appName')}><AppBrand /></header>}

      {children}
    </div>
  );
}

function AuthenticatedApp() {
  const queryClient = useQueryClient();
  const [isOnline, setIsOnline] = useState(() => typeof navigator === 'undefined' || navigator.onLine);
  const meQuery = useQuery({
    queryKey: meQueryKey,
    queryFn: async () => {
      const previousProfile = queryClient.getQueryData<MeResponse | null>(meQueryKey);
      const profile = await loadCurrentUser();
      if (!profile
        || previousProfile?.user.id !== profile.user.id
        || previousProfile.user.sync_scope !== profile.user.sync_scope) {
        queryClient.removeQueries({ predicate: (query) => query.queryKey[0] !== meQueryKey[0] });
      }
      return profile;
    },
    retry: false,
    staleTime: 5 * 60 * 1000,
  });
  const logoutMutation = useMutation({
    mutationFn: logout,
    onSuccess: async () => {
      try {
        await clearOfflineData();
      } catch {
        // The server session is already closed; local cleanup is best-effort.
      }
      queryClient.setQueryData(meQueryKey, null);
      queryClient.removeQueries({ predicate: (query) => query.queryKey[0] !== meQueryKey[0] });
    },
  });

  const { settings: themeSettings, setThemeSettings } = useTheme();
  const serverSettings = meQuery.data?.settings;
  useEffect(() => {
    const next = serverSettings ? themeFromServer(serverSettings) : null;
    if (next && JSON.stringify(next) !== JSON.stringify(themeSettings)) setThemeSettings(next);
  }, [serverSettings]);

  useEffect(() => {
    const updateNetworkState = () => setIsOnline(navigator.onLine);
    window.addEventListener('online', updateNetworkState);
    window.addEventListener('offline', updateNetworkState);
    return () => {
      window.removeEventListener('online', updateNetworkState);
      window.removeEventListener('offline', updateNetworkState);
    };
  }, []);

  useEffect(() => {
    const profile = meQuery.data;
    if (!profile || !isOnline) return undefined;
    const sync = () => {
      void synchronizeOfflineData(profile.user.id, profile.user.sync_scope, profile.settings).catch(() => undefined);
    };
    sync();
    const retryTimer = window.setInterval(sync, 30_000);
    window.addEventListener('online', sync);
    return () => {
      window.clearInterval(retryTimer);
      window.removeEventListener('online', sync);
    };
  }, [isOnline, meQuery.data]);

  if (meQuery.isPending) {
    return (
      <AppFrame>
        <main className="main-content auth-content" id="main-content" tabIndex={-1}>
          <p className="auth-loading" role="status" aria-live="polite">{t('checkingSession')}</p>
        </main>
      </AppFrame>
    );
  }

  if (meQuery.isError) {
    const message = meQuery.error instanceof ApiError && meQuery.error.serverMessage
      ? meQuery.error.serverMessage
      : t('sessionLoadFailed');
    return (
      <AppFrame>
        <main className="main-content auth-content" id="main-content" tabIndex={-1}>
          <section className="auth-card" aria-labelledby="session-error-heading">
            <p className="auth-kicker">{t('accountAccess')}</p>
            <h1 id="session-error-heading">{t('sessionUnavailable')}</h1>
            <p className="auth-description" role="alert">{message}</p>
            <Button type="button" onClick={() => void meQuery.refetch()}>{t('tryAgain')}</Button>
          </section>
        </main>
      </AppFrame>
    );
  }

  return (
    <AppFrame signedIn={!!meQuery.data}>
      {meQuery.data && <AppNavigation isAdmin={!!meQuery.data.user.is_admin} pathname={window.location.pathname} />}
      {meQuery.data && (
        <header className="app-user-bar" aria-label={t('appName')}>
          <AppBrand />
          <div className="app-user-controls">
            {!isOnline && (
              <span className="app-connection-status" role="status" aria-live="polite">
                {t('connectionOffline')}
              </span>
            )}
            <AccountMenu user={meQuery.data.user} signingOut={logoutMutation.isPending} onSignOut={() => logoutMutation.mutate()} />
          </div>
        </header>
      )}
      <main className={`main-content ${meQuery.data ? 'app-content' : 'auth-content'}`} id="main-content" tabIndex={-1}>
        {meQuery.data ? (
          <>
            {logoutMutation.isError && (
              <p className="auth-form-error" role="alert">
                {logoutMutation.error instanceof ApiError && logoutMutation.error.serverMessage
                  ? logoutMutation.error.serverMessage
                  : t('requestFailed')}
              </p>
            )}
            {window.location.pathname === '/study' ? (
              <StudySession
                ownerID={meQuery.data.user.id}
                ownerScopeID={meQuery.data.user.sync_scope}
                settings={meQuery.data.settings}
                ttsAuto={meQuery.data.settings.tts_auto}
              />
            ) : window.location.pathname === '/settings' ? (
              <SettingsPage settings={meQuery.data.settings} />
            ) : window.location.pathname === '/admin' ? (
              meQuery.data.user.is_admin ? <AdminPage /> : <section className="mx-auto max-w-3xl rounded-3xl bg-error-container p-6 text-on-error-container" role="alert">{t('adminAccessDenied')}</section>
            ) : window.location.pathname === '/stats' ? (
              <Suspense fallback={<p role="status" className="auth-loading">{t('statsLoading')}</p>}>
                <StatsPage timezone={meQuery.data.settings.timezone} />
              </Suspense>
            ) : window.location.pathname === '/friends' ? (
              <Suspense fallback={<p role="status" className="auth-loading">{t('socialLoading')}</p>}>
                <FriendsPage userId={meQuery.data.user.id} />
              </Suspense>
            ) : (
              <DecksWorkspace ownerId={meQuery.data.user.id} />
            )}
          </>
        ) : (
          <AuthForms mode={window.location.pathname === '/signup' ? 'signup' : 'signin'} />
        )}
      </main>
    </AppFrame>
  );
}
