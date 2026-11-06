// @vitest-environment jsdom
import { cleanup, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { isDevShowcase } from '../App';
import { t, type TranslationKey } from '../i18n/en';
import { ThemeProvider } from '../theme/context';
import { createThemePalettes } from '../theme/palette';
import { DEFAULT_THEME_SETTINGS } from '../theme/settings';
import Showcase, { hasLowContrast } from './Showcase';

afterEach(cleanup);
beforeEach(() => localStorage.clear());
describe('development showcase', () => {
  it('gates the exact route to development', () => {
    expect(isDevShowcase(true, '/dev/ui')).toBe(true);
    expect(isDevShowcase(false, '/dev/ui')).toBe(false);
    expect(isDevShowcase(true, '/dev/other')).toBe(false);
  });
  it('renders all component sections and updates real theme settings', async () => {
    const user = userEvent.setup();
    render(<ThemeProvider><Showcase /></ThemeProvider>);
    const headings: TranslationKey[] = ['connectedGroup', 'fabMenu', 'wavyProgress', 'loadingIndicator', 'floatingToolbar', 'splitButton', 'responsiveNav', 'snackbar', 'sheet', 'shapes'];
    for (const key of headings) expect(screen.getAllByRole('heading', { name: t(key) })).toHaveLength(2);
    await user.click(screen.getByRole('button', { name: t('ocean') }));
    expect(screen.getByRole('button', { name: t('ocean') }).getAttribute('aria-pressed')).toBe('true');
    await user.selectOptions(screen.getByLabelText(t('schemeStyle')), 'vibrant');
    await user.selectOptions(screen.getByLabelText(t('mode')), 'dark');
    expect(document.documentElement.dataset.themeMode).toBe('dark');
    expect((screen.getByLabelText(t('schemeStyle')) as HTMLSelectElement).value).toBe('vibrant');
    await user.clear(screen.getByLabelText(t('hexColor')));
    expect(screen.getByRole('alert').textContent).toBe(t('invalidHex'));
    await user.click(screen.getAllByRole('button', { name: t('openSheet') })[0]);
    expect(screen.getByRole('dialog')).toBeTruthy();
    await user.keyboard('{Escape}');
    await user.click(screen.getAllByRole('button', { name: t('showSnackbar') })[0]);
    expect(await screen.findByText(t('snackbarMessage'))).toBeTruthy();
  });
  it('detects insufficient palette contrast', () => {
    const palettes = createThemePalettes(DEFAULT_THEME_SETTINGS);
    palettes.light['on-surface'] = palettes.light.surface;
    expect(hasLowContrast(palettes)).toBe(true);
  });
});
