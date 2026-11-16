// @vitest-environment jsdom
import { cleanup, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { AppNavigation } from './AppNavigation';
import { t } from './i18n/en';

afterEach(cleanup);

describe('application navigation', () => {
  it.each([
    ['/', 'decks'], ['/study', 'study'], ['/stats', 'stats'],
    ['/friends', 'friends'], ['/settings', 'settings'], ['/admin', 'admin'],
  ] as const)('marks %s as the current page', (pathname, label) => {
    render(<AppNavigation isAdmin pathname={pathname} />);
    const navigation = screen.getByRole('navigation', { name: t('navigation') });
    expect(within(navigation).getByRole('link', { name: t(label) }).getAttribute('aria-current')).toBe('page');
    expect(navigation.querySelectorAll('[aria-current="page"]')).toHaveLength(1);
  });

  it('does not expose the admin link to non-administrators', () => {
    render(<AppNavigation isAdmin={false} pathname="/" />);
    expect(screen.queryByRole('link', { name: t('admin') })).toBeNull();
    expect(screen.getByRole('link', { name: t('study') }).getAttribute('href')).toBe('/study');
  });

  it('keeps every administrator destination in one navigation with concise labels', () => {
    render(<AppNavigation isAdmin pathname="/" />);
    const navigation = screen.getByRole('navigation', { name: t('navigation') });
    expect(within(navigation).getAllByRole('link').map((link) => ({
      label: link.textContent,
      href: link.getAttribute('href'),
    }))).toEqual([
      { label: t('decks'), href: '/' },
      { label: t('study'), href: '/study' },
      { label: t('stats'), href: '/stats' },
      { label: t('friends'), href: '/friends' },
      { label: t('settings'), href: '/settings' },
      { label: t('admin'), href: '/admin' },
    ]);
    expect(within(navigation).queryByText(t('decksWorkspace'))).toBeNull();
    expect(navigation.querySelectorAll('.app-navigation-indicator[aria-hidden="true"] svg')).toHaveLength(6);
  });
});
