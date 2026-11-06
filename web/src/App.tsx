import './app.css';
import { t } from './i18n/en';

export function App() {
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
