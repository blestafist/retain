// @vitest-environment jsdom
import { cleanup, render, screen, within, fireEvent, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { isDevShowcase } from '../App';
import { t, type TranslationKey } from '../i18n/en';
import { ThemeProvider } from '../theme/context';
import { createThemePalettes } from '../theme/palette';
import { DEFAULT_THEME_SETTINGS } from '../theme/settings';
import Showcase, { hasLowContrast } from './Showcase';

afterEach(cleanup);
beforeEach(() => { localStorage.clear(); window.history.replaceState(null, '', '/'); });
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
    expect(screen.queryByText(t('invalidHex'))).toBeNull();
    await user.tab();
    expect(screen.getByRole('alert').textContent).toBe(t('invalidHex'));
    await user.click(screen.getAllByRole('button', { name: t('openSheet') })[0]);
    expect(screen.getByRole('dialog')).toBeTruthy();
    await user.keyboard('{Escape}');
    await user.click(screen.getAllByRole('button', { name: t('showSnackbar') })[0]);
    expect(await screen.findByText(t('snackbarMessage'))).toBeTruthy();
  });
  it('commits a fully typed hex only on blur or Enter and handles other color controls', async () => {
    const user = userEvent.setup();
    render(<ThemeProvider><Showcase /></ThemeProvider>);
    const hex = screen.getByLabelText(t('hexColor')) as HTMLInputElement;
    const picker = screen.getByLabelText(t('customColor')) as HTMLInputElement;
    const original = picker.value;
    await user.clear(hex);
    await user.type(hex, '#123456');
    expect(hex.value).toBe('#123456');
    expect(picker.value).toBe(original);
    expect(screen.queryByText(t('invalidHex'))).toBeNull();
    await user.keyboard('{Enter}');
    expect(picker.value).toBe('#123456');
    await user.clear(hex);
    await user.type(hex, 'bad!');
    await user.keyboard('{Enter}');
    expect(screen.getByText(t('invalidHex'))).toBeTruthy();
    await user.click(screen.getByRole('button', { name: t('ocean') }));
    expect(hex.value.toLowerCase()).toBe(picker.value);
    expect(screen.queryByText(t('invalidHex'))).toBeNull();
    fireEvent.change(picker, { target: { value: '#abcdef' } });
    expect(hex.value.toLowerCase()).toBe('#abcdef');
  });
  it('uses configured responsive breakpoints for the preview grid', () => {
    const source = readFileSync('src/dev/Showcase.tsx', 'utf8');
    expect(readFileSync('tailwind.config.js', 'utf8')).toMatch(/screens:\s*\{\s*medium:\s*'600px',\s*expanded:\s*'840px'\s*\}/);
    expect(source).toContain('expanded:grid-cols-2');
    expect(source).not.toMatch(/\b(?:sm|lg):/);
  });
  it.each(['light', 'dark'] as const)('keeps %s overlays and notifications inside their preview', async (mode) => {
    const user = userEvent.setup();
    render(<ThemeProvider><Showcase /></ThemeProvider>);
    const preview = document.querySelector(`[data-theme-preview="${mode}"]`) as HTMLElement;
    await user.click(within(preview).getByRole('button', { name: t('openSheet') }));
    expect(preview.contains(screen.getByRole('dialog'))).toBe(true);
    await user.keyboard('{Escape}');
    for (const label of ['openActions', 'chooseMode'] as const) {
      await user.click(within(preview).getByRole('button', { name: t(label) }));
      expect(preview.contains(screen.getByRole('menu'))).toBe(true);
      await user.keyboard('{Escape}');
    }
    await user.click(within(preview).getByRole('button', { name: t('showSnackbar') }));
    expect(await within(preview).findByText(t('snackbarMessage'))).toBeTruthy();
  });
  it('navigates to actual preview sections and moves the selection', async () => {
    const user = userEvent.setup();
    render(<ThemeProvider><Showcase /></ThemeProvider>);
    const preview = document.querySelector('[data-theme-preview="dark"]') as HTMLElement;
    const nav = within(preview).getByRole('navigation');
    const home = within(nav).getByRole('link', { name: t('home') });
    const decks = within(nav).getByRole('link', { name: t('decks') });
    for (const link of within(nav).getAllByRole('link')) expect(preview.querySelector(link.getAttribute('href')!)).toBeTruthy();
    await user.click(decks);
    await waitFor(() => expect(decks.getAttribute('aria-current')).toBe('page'));
    expect(decks.firstElementChild?.className).toContain('bg-secondary-container');
    expect(home.getAttribute('aria-current')).toBeNull();
    expect(home.firstElementChild?.className).not.toContain('bg-secondary-container');
  });
  it('syncs navigation with direct hashes and history hash changes', () => {
    window.history.replaceState(null, '', '#dark-decks');
    render(<ThemeProvider><Showcase /></ThemeProvider>);
    const dark = within(document.querySelector('[data-theme-preview="dark"]') as HTMLElement).getByRole('navigation');
    const light = within(document.querySelector('[data-theme-preview="light"]') as HTMLElement).getByRole('navigation');
    const selected = (nav: HTMLElement, id: 'home' | 'decks' | 'settings') => within(nav).getByRole('link', { name: t(id) }).getAttribute('aria-current');
    expect(selected(dark, 'decks')).toBe('page');
    expect(selected(light, 'home')).toBe('page');
    window.history.replaceState(null, '', '#light-settings');
    fireEvent(window, new HashChangeEvent('hashchange'));
    expect(selected(light, 'settings')).toBe('page');
    expect(selected(dark, 'home')).toBe('page');
    expect(selected(dark, 'decks')).toBeNull();
    window.history.replaceState(null, '', '#dark-decks');
    fireEvent(window, new HashChangeEvent('hashchange'));
    expect(selected(dark, 'decks')).toBe('page');
    expect(selected(light, 'home')).toBe('page');
    window.history.replaceState(null, '', '#unknown');
    fireEvent(window, new HashChangeEvent('hashchange'));
    expect(selected(dark, 'home')).toBe('page');
    expect(selected(light, 'home')).toBe('page');
  });
  it('detects insufficient palette contrast', () => {
    const palettes = createThemePalettes(DEFAULT_THEME_SETTINGS);
    palettes.light['on-surface'] = palettes.light.surface;
    expect(hasLowContrast(palettes)).toBe(true);
  });
});
