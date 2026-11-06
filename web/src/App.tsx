import { lazy, Suspense } from 'react';
import './app.css';
import { t } from './i18n/en';

const DevShowcase = import.meta.env.DEV ? lazy(() => import('./dev/Showcase')) : null;

export function isDevShowcase(dev: boolean, pathname: string) {
  return dev && pathname === '/dev/ui';
}

export function App() {
  if (DevShowcase && isDevShowcase(import.meta.env.DEV, window.location.pathname)) {
    return <Suspense fallback={null}><DevShowcase /></Suspense>;
  }
  return (
    <div className="app-shell">
      <a className="skip-link" href="#main-content">
        {t('skipToMain')}
      </a>

      <header className="app-header">
        <p className="app-name">{t('appName')}</p>
      </header>

      <main className="main-content" id="main-content" tabIndex={-1}>
        <h1>{t('welcomeHeading')}</h1>
        <p className="placeholder-description">
          {t('placeholderDescription')}
        </p>
      </main>
    </div>
  );
}
