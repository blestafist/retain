// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { UserSettings } from '../api/client';
import { ThemeProvider } from '../theme/context';
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
});
