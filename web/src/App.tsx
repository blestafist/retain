import { lazy, Suspense, type ReactNode } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ApiError, logout } from './api/client';
import { AuthForms } from './auth/AuthForms';
import { loadCurrentUser, meQueryKey } from './auth/session';
import './app.css';
import { t } from './i18n/en';
import { Button } from './ui/Button';

const DevShowcase = import.meta.env.DEV ? lazy(() => import('./dev/Showcase')) : null;

export function isDevShowcase(dev: boolean, pathname: string) {
  return dev && pathname === '/dev/ui';
}

export function App() {
  if (DevShowcase && isDevShowcase(import.meta.env.DEV, window.location.pathname)) {
    return <Suspense fallback={null}><DevShowcase /></Suspense>;
  }

  return <AuthenticatedApp />;
}

function AppFrame({ children }: { children: ReactNode }) {
  return (
    <div className="app-shell">
      <a className="skip-link" href="#main-content">
        {t('skipToMain')}
      </a>

      <header className="app-header">
        <p className="app-name">{t('appName')}</p>
      </header>

      {children}
    </div>
  );
}

function AuthenticatedApp() {
  const queryClient = useQueryClient();
  const meQuery = useQuery({
    queryKey: meQueryKey,
    queryFn: loadCurrentUser,
    retry: false,
    staleTime: 5 * 60 * 1000,
  });
  const logoutMutation = useMutation({
    mutationFn: logout,
    onSuccess: () => queryClient.setQueryData(meQueryKey, null),
  });

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
    <AppFrame>
      <main className="main-content auth-content" id="main-content" tabIndex={-1}>
        {meQuery.data ? (
          <section className="auth-card profile-card" aria-labelledby="profile-heading">
            <p className="auth-kicker">{t('signedIn')}</p>
            <h1 id="profile-heading">{t('welcomeBack')}</h1>
            <p className="profile-name">{meQuery.data.user.name}</p>
            <p className="profile-email">{meQuery.data.user.email}</p>
            {logoutMutation.isError && (
              <p className="auth-form-error" role="alert">
                {logoutMutation.error instanceof ApiError && logoutMutation.error.serverMessage
                  ? logoutMutation.error.serverMessage
                  : t('requestFailed')}
              </p>
            )}
            <Button
              className="auth-submit"
              variant="tonal"
              type="button"
              disabled={logoutMutation.isPending}
              onClick={() => logoutMutation.mutate()}
            >
              {t(logoutMutation.isPending ? 'signingOut' : 'signOut')}
            </Button>
          </section>
        ) : (
          <AuthForms mode={window.location.pathname === '/signup' ? 'signup' : 'signin'} />
        )}
      </main>
    </AppFrame>
  );
}
