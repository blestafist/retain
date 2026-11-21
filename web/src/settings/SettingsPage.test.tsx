// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { UserSettings } from '../api/client';
import { meQueryKey } from '../auth/session';
import { ThemeProvider } from '../theme/context';
import { createThemePalette } from '../theme/palette';
import { t } from '../i18n/en';
import { SettingsPage } from './SettingsPage';

const settings: UserSettings = {
  new_per_day: 20,
  max_reviews_per_day: 200,
  request_retention: 0.9,
  maximum_interval: 36500,
  learning_steps: '1m 10m',
  relearning_steps: '10m',
  fsrs_weights: null,
  timezone: 'UTC',
  day_start_hour: 4,
  reminder_time: null,
  tts_auto: false,
  theme_mode: 'system',
  theme_seed: '#3A4527',
  theme_variant: 'fidelity',
  theme_contrast: 0,
};

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('study settings', () => {
  it('validates retention and saves the study schedule settings', async () => {
    const user = userEvent.setup();
    const fetchMock = vi.fn<typeof fetch>().mockImplementation(async (_input, init) => {
      const submitted = JSON.parse(String(init?.body)) as Partial<UserSettings>;
      const next = { ...settings, ...submitted };
      return new Response(JSON.stringify(next), { status: 200, headers: { 'Content-Type': 'application/json' } });
    });
    vi.stubGlobal('fetch', fetchMock);
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });

    render(<ThemeProvider><QueryClientProvider client={queryClient}><SettingsPage settings={settings} /></QueryClientProvider></ThemeProvider>);
    const retention = screen.getByLabelText(t('desiredRetention'));
    await user.clear(retention);
    await user.type(retention, '1');
    await user.click(screen.getByRole('button', { name: t('saveSettings') }));
    expect(await screen.findByText(t('invalidRequestRetention'))).toBeTruthy();
    expect(fetchMock).not.toHaveBeenCalled();

    await user.clear(retention);
    await user.type(retention, '0.95');
    await user.click(screen.getByRole('button', { name: t('saveSettings') }));
    expect(await screen.findByText(t('settingsSaved'))).toBeTruthy();
    expect(JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body))).toEqual({ request_retention: 0.95 });
  });

  it('keeps reminder details accessible and saves a configured time', async () => {
    const user = userEvent.setup();
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify({ ...settings, reminder_time: '07:30' }), {
      status: 200, headers: { 'Content-Type': 'application/json' },
    }));
    vi.stubGlobal('fetch', fetchMock);
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    render(<ThemeProvider><QueryClientProvider client={queryClient}><SettingsPage settings={settings} /></QueryClientProvider></ThemeProvider>);

    expect(screen.queryByLabelText(t('reminderTime'))).toBeNull();
    await user.click(screen.getByRole('checkbox', { name: t('dailyReminders'), description: t('dailyRemindersHint') }));
    const time = screen.getByLabelText(t('reminderTime'));
    await user.click(screen.getByRole('button', { name: t('saveSettings') }));
    expect(time.getAttribute('aria-invalid')).toBe('true');
    expect(fetchMock).not.toHaveBeenCalled();

    fireEvent.change(time, { target: { value: '07:30' } });
    await user.click(screen.getByRole('button', { name: t('saveSettings') }));
    expect(await screen.findByText(t('settingsSaved'))).toBeTruthy();
    expect(JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body))).toEqual({ reminder_time: '07:30' });
  });

  it('previews appearance locally and saves the accent, mode, and schedule in one request', async () => {
    const user = userEvent.setup();
    const refreshedSettings = { ...settings, max_reviews_per_day: 250 };
    const fetchMock = vi.fn<typeof fetch>().mockImplementation(async (_input, init) => {
      return Response.json({ ...refreshedSettings, ...JSON.parse(String(init?.body)) });
    });
    vi.stubGlobal('fetch', fetchMock);
    const queryClient = new QueryClient();
    queryClient.setQueryData(meQueryKey, { settings });
    const page = (profileSettings: UserSettings) => <ThemeProvider><QueryClientProvider client={queryClient}><SettingsPage settings={profileSettings} /></QueryClientProvider></ThemeProvider>;
    const { rerender } = render(page(settings));
    const rootPrimary = document.documentElement.style.getPropertyValue('--md-sys-color-primary');
    const dayStart = screen.getByLabelText(t('dayStartHour')) as HTMLInputElement;
    const ocean = screen.getByRole('button', { name: t('ocean') });

    fireEvent.change(dayStart, { target: { value: '6' } });
    await user.selectOptions(screen.getByLabelText(t('themeMode')), 'dark');
    await user.click(ocean);
    rerender(page(refreshedSettings));

    expect(ocean.getAttribute('aria-pressed')).toBe('true');
    expect(screen.getByRole('button', { name: t('swamp') }).getAttribute('aria-pressed')).toBe('false');
    expect(dayStart.value).toBe('6');
    expect((screen.getByLabelText(t('maxReviewsPerDay')) as HTMLInputElement).value).toBe('250');
    const preview = screen.getByRole('group', { name: t('appearancePreview') }).querySelector<HTMLElement>('.settings-appearance-preview');
    const palette = createThemePalette({ seed: '#006874', mode: 'dark', variant: 'fidelity', contrastLevel: 0 }, 'dark');
    expect(preview?.style.colorScheme).toBe('dark');
    expect(preview?.style.getPropertyValue('--md-sys-color-primary')).toBe(palette.primary);
    expect(document.documentElement.style.getPropertyValue('--md-sys-color-primary')).toBe(rootPrimary);
    expect(fetchMock).not.toHaveBeenCalled();

    await user.click(screen.getByRole('button', { name: t('saveSettings') }));
    expect(await screen.findByText(t('settingsSaved'))).toBeTruthy();
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body))).toEqual({
      day_start_hour: 6, theme_seed: '#006874', theme_mode: 'dark',
    });
    expect(queryClient.getQueryData(meQueryKey)).toMatchObject({ settings: { theme_seed: '#006874', day_start_hour: 6 } });
  });

  it('resolves a draft system theme independently of the saved explicit mode', async () => {
    const query = Object.assign(new EventTarget(), { matches: true });
    vi.stubGlobal('matchMedia', () => query);
    const queryClient = new QueryClient();
    const user = userEvent.setup();
    render(<ThemeProvider><QueryClientProvider client={queryClient}><SettingsPage settings={{ ...settings, theme_mode: 'light' }} /></QueryClientProvider></ThemeProvider>);
    const preview = screen.getByRole('group', { name: t('appearancePreview') }).querySelector<HTMLElement>('.settings-appearance-preview');

    expect(preview?.style.colorScheme).toBe('light');
    await user.selectOptions(screen.getByLabelText(t('themeMode')), 'system');
    expect(preview?.style.colorScheme).toBe('dark');
    act(() => {
      query.matches = false;
      query.dispatchEvent(new Event('change'));
    });
    expect(preview?.style.colorScheme).toBe('light');
  });
});
