import { t, type TranslationKey } from './i18n/en';
import './app-navigation.css';

const destinations: { href: string; label: TranslationKey; path: string }[] = [
  { href: '/', label: 'decksWorkspace', path: 'M4 5h12v14H4z M8 2h12v14 M7 9h6 M7 12h6' },
  { href: '/study', label: 'study', path: 'M5 4h14v16H5z M8 8h8 M8 12h8 M8 16h5' },
  { href: '/stats', label: 'stats', path: 'M4 20h16 M6 17v-5 M12 17V5 M18 17V9' },
  { href: '/friends', label: 'friends', path: 'M9 11a3 3 0 1 0 0-6 3 3 0 0 0 0 6ZM3 19v-1a6 6 0 0 1 12 0v1z M16 5a3 3 0 0 1 0 6 M17 13a5 5 0 0 1 4 5v1' },
  { href: '/settings', label: 'settings', path: 'M4 7h16 M4 17h16 M8 4v6 M16 14v6' },
  { href: '/admin', label: 'admin', path: 'M12 3 4 6v5c0 5 3.5 8 8 10 4.5-2 8-5 8-10V6z M9 12l2 2 4-4' },
];

export function AppNavigation({ isAdmin, pathname }: { isAdmin: boolean; pathname: string }) {
  return (
    <nav className="app-navigation" aria-label={t('navigation')}>
      {destinations.filter((item) => isAdmin || item.href !== '/admin').map((item) => (
        <a className="app-navigation-link" key={item.href} href={item.href} aria-current={pathname === item.href ? 'page' : undefined}>
          <span className="app-navigation-indicator" aria-hidden="true">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
              <path d={item.path} />
            </svg>
          </span>
          <span className="app-navigation-label">{t(item.label)}</span>
        </a>
      ))}
    </nav>
  );
}
