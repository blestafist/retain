// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, cleanup, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ThemeProvider } from './theme/context';
import { App } from './App';
import { meQueryKey } from './auth/session';
import { t } from './i18n/en';

const fetchMock = vi.fn<typeof fetch>();
const calls: Array<{ url: string; init?: RequestInit }> = [];

function response(body?: unknown, status = 200) {
  return new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
  });
}

function setFetchHandler(handler: (url: string, init?: RequestInit) => Promise<Response>) {
  fetchMock.mockImplementation((input, init) => {
    const url = String(input);
    calls.push({ url, init });
    return handler(url, init);
  });
  vi.stubGlobal('fetch', fetchMock);
}

function renderApp() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return {
    ...render(<ThemeProvider><QueryClientProvider client={queryClient}><App /></QueryClientProvider></ThemeProvider>),
    queryClient,
  };
}

function unauthorized() {
  return response({ error: { code: 'unauthorized', message: 'Sign in required.' } }, 401);
}

const signedInResponse = {
  user: { id: 1, sync_scope: 1, email: 'friend@example.com', name: 'Morgan', is_admin: false },
  settings: {
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
  },
};

const deck = {
  id: 42,
  parent_id: null,
  name: 'German course',
  lang_target: 'de',
  lang_native: 'pl',
  reverse_cards: false,
  wrong_article_rating: 2,
  counts: { notes: 0, new: 0, learning: 0, due: 0 },
  children: [],
};

beforeEach(() => {
  fetchMock.mockReset();
  calls.length = 0;
  window.history.replaceState(null, '', '/');
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('account screens', () => {
  it('shows invite-only sign-up and validates required fields inline', async () => {
    const user = userEvent.setup();
    setFetchHandler(async () => unauthorized());
    window.history.replaceState(null, '', '/signup');
    renderApp();

    await screen.findByRole('heading', { name: t('signUpHeading') });
    await user.click(screen.getByRole('button', { name: t('createAccount') }));

    expect(await screen.findByText(t('inviteRequired'))).toBeTruthy();
    expect(screen.getByLabelText(t('invitationCode')).getAttribute('aria-invalid')).toBe('true');
    expect(screen.getByLabelText(t('email')).getAttribute('aria-invalid')).toBe('true');
    expect(screen.getByLabelText(t('password')).getAttribute('aria-invalid')).toBe('true');
    expect(screen.getByLabelText(t('invitationCode'))).toBe(document.activeElement);
  });

  it('prefills the invitation query and does not create a session during registration', async () => {
    const user = userEvent.setup();
    setFetchHandler(async (url) => {
      if (url === '/api/v1/me') return unauthorized();
      if (url === '/api/v1/auth/register') return response(undefined, 201);
      throw new Error(`Unexpected request: ${url}`);
    });
    window.history.replaceState(null, '', '/signup?invite=friend-code-123');
    renderApp();

    await screen.findByRole('heading', { name: t('signUpHeading') });
    expect((screen.getByLabelText(t('invitationCode')) as HTMLInputElement).value).toBe('friend-code-123');
    await user.type(screen.getByLabelText(t('name')), 'Morgan');
    await user.type(screen.getByLabelText(t('email')), 'friend@example.com');
    await user.type(screen.getByLabelText(t('password')), 'long-password-123');
    await user.click(screen.getByRole('button', { name: t('createAccount') }));

    expect(await screen.findByRole('heading', { name: t('accountReadyHeading') })).toBeTruthy();
    expect(screen.getByRole('link', { name: t('signIn') }).getAttribute('href')).toBe('/');
    expect(calls.map(({ url }) => url)).toEqual(['/api/v1/me', '/api/v1/auth/register']);
    expect(JSON.parse(String(calls[1]?.init?.body))).toEqual({
      invite: 'friend-code-123', email: 'friend@example.com', name: 'Morgan', password: 'long-password-123',
    });
  });

  it('shows login errors and then opens the deck workspace after sign-in', async () => {
    const user = userEvent.setup();
    let meCalls = 0;
    let loginCalls = 0;
    setFetchHandler(async (url) => {
      if (url === '/api/v1/me') {
        meCalls += 1;
        return meCalls === 1 ? unauthorized() : response(signedInResponse);
      }
      if (url === '/api/v1/auth/login') {
        loginCalls += 1;
        return loginCalls === 1
          ? response({ error: { code: 'invalid_credentials', message: 'Email or password is incorrect.' } }, 401)
          : response(undefined, 204);
      }
      if (url === '/api/v1/llm/status') return response({ enabled: false });
      if (url === '/api/v1/decks') return response([deck]);
      if (url === '/api/v1/decks/42/notes') return response([]);
      if (url === '/api/v1/import/batches') return response([]);
      throw new Error(`Unexpected request: ${url}`);
    });
    renderApp();

    await screen.findByRole('heading', { name: t('signInHeading') });
    await user.type(screen.getByLabelText(t('email')), 'friend@example.com');
    await user.type(screen.getByLabelText(t('password')), 'long-password-123');
    await user.click(screen.getByRole('button', { name: t('signIn') }));
    expect(await screen.findByRole('alert')).toHaveProperty('textContent', 'Email or password is incorrect.');
    await user.click(screen.getByRole('button', { name: t('signIn') }));

    expect(await screen.findByRole('region', { name: t('decksWorkspace') })).toBeTruthy();
    expect(await screen.findByRole('heading', { name: 'German course' })).toBeTruthy();
    const banner = screen.getByRole('banner', { name: t('appName') });
    expect(within(banner).getByRole('link', { name: t('appName') })).toBeTruthy();
    expect(within(banner).getByRole('button', { name: `${t('signedIn')}: Morgan` })).toBeTruthy();
    expect(screen.getByRole('main').contains(banner)).toBe(false);
    expect(screen.queryByText('friend@example.com')).toBeNull();
    expect(screen.queryByText(t('signOut'))).toBeNull();
    expect(screen.queryByText(t('connectionOnline'))).toBeNull();
    expect(meCalls).toBe(2);
  });

  it('uses the current-user query for session state and clears it after logout', async () => {
    const user = userEvent.setup();
    setFetchHandler(async (url) => {
      if (url === '/api/v1/me') return response(signedInResponse);
      if (url === '/api/v1/llm/status') return response({ enabled: false });
      if (url === '/api/v1/decks') return response([]);
      if (url === '/api/v1/auth/logout') return response(undefined, 204);
      throw new Error(`Unexpected request: ${url}`);
    });
    const { queryClient } = renderApp();

    const account = await screen.findByRole('button', { name: `${t('signedIn')}: Morgan` });
    expect(account.querySelector('.app-account-avatar')?.textContent).toBe('M');
    expect(screen.queryByText('friend@example.com')).toBeNull();
    expect(screen.queryByRole('menuitem', { name: t('signOut') })).toBeNull();
    queryClient.setQueryData(['deck-notes', '42'], [{ id: 7, term: 'Wort' }]);
    await waitFor(() => expect(queryClient.getQueryData(['decks'])).toBeTruthy());
    await user.click(account);
    const menu = await screen.findByRole('menu', { name: `${t('signedIn')}: Morgan` });
    expect(within(menu).getByText('friend@example.com')).toBeTruthy();
    await user.click(within(menu).getByRole('menuitem', { name: t('signOut') }));
    await screen.findByRole('heading', { name: t('signInHeading') });

    expect(calls.map(({ url }) => url)).toContain('/api/v1/llm/status');
    expect(calls.map(({ url }) => url)).toContain('/api/v1/auth/logout');
    await waitFor(() => expect(screen.queryByText('Morgan')).toBeNull());
    expect(queryClient.getQueryData(['decks'])).toBeUndefined();
    expect(queryClient.getQueryData(['deck-notes', '42'])).toBeUndefined();
  });

  it('opens the account menu with a keyboard and restores focus when dismissed', async () => {
    const user = userEvent.setup();
    setFetchHandler(async (url) => {
      if (url === '/api/v1/me') return response(signedInResponse);
      if (url === '/api/v1/llm/status') return response({ enabled: false });
      if (url === '/api/v1/decks') return response([]);
      throw new Error(`Unexpected request: ${url}`);
    });
    renderApp();

    const account = await screen.findByRole('button', { name: `${t('signedIn')}: Morgan` });
    expect(account.getAttribute('aria-haspopup')).toBe('menu');
    expect(account.getAttribute('aria-expanded')).toBe('false');
    account.focus();
    await user.keyboard('{Enter}');

    const menu = await screen.findByRole('menu', { name: `${t('signedIn')}: Morgan` });
    expect(within(menu).getByText('Morgan')).toBeTruthy();
    expect(within(menu).getByText('friend@example.com')).toBeTruthy();
    const signOut = within(menu).getByRole('menuitem', { name: t('signOut') });
    await waitFor(() => expect(document.activeElement).toBe(signOut));
    expect(account.getAttribute('aria-expanded')).toBe('true');

    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('menu')).toBeNull());
    expect(document.activeElement).toBe(account);
    expect(calls.some(({ url }) => url === '/api/v1/auth/logout')).toBe(false);
  });

  it('shows an offline status in the banner only while disconnected', async () => {
    const online = vi.spyOn(navigator, 'onLine', 'get').mockReturnValue(true);
    setFetchHandler(async (url) => {
      if (url === '/api/v1/me') return response(signedInResponse);
      if (url === '/api/v1/llm/status') return response({ enabled: false });
      if (url === '/api/v1/decks') return response([]);
      throw new Error(`Unexpected request: ${url}`);
    });
    renderApp();

    await screen.findByRole('button', { name: `${t('signedIn')}: Morgan` });
    const banner = screen.getByRole('banner', { name: t('appName') });
    expect(within(banner).queryByRole('status')).toBeNull();
    expect(screen.queryByText(t('connectionOnline'))).toBeNull();

    online.mockReturnValue(false);
    act(() => { window.dispatchEvent(new Event('offline')); });
    const status = within(banner).getByRole('status');
    expect(status.textContent).toBe(t('connectionOffline'));
    expect(status.getAttribute('aria-live')).toBe('polite');
    expect(screen.getByRole('main').contains(status)).toBe(false);

    online.mockReturnValue(true);
    act(() => { window.dispatchEvent(new Event('online')); });
    expect(within(banner).queryByRole('status')).toBeNull();
    expect(screen.queryByText(t('connectionOnline'))).toBeNull();
  });

  it('opens the study queue for the deck linked from the workspace', async () => {
    setFetchHandler(async (url) => {
      if (url === '/api/v1/me') return response(signedInResponse);
      if (url === '/api/v1/llm/status') return response({ enabled: false });
      if (url === '/api/v1/decks') return response([deck]);
      if (url === '/api/v1/study/queue?deck_id=42&limit=50') return response([]);
      throw new Error(`Unexpected request: ${url}`);
    });
    window.history.replaceState(null, '', '/study?deck=42');
    renderApp();

    expect(await screen.findByText(t('noCardsToStudy'))).toBeTruthy();
    expect(calls.some(({ url }) => url === '/api/v1/study/queue?deck_id=42&limit=50')).toBe(true);
  });

  it('retains the session and exposes the server error when menu sign-out fails', async () => {
    const user = userEvent.setup();
    setFetchHandler(async (url) => {
      if (url === '/api/v1/me') return response(signedInResponse);
      if (url === '/api/v1/llm/status') return response({ enabled: false });
      if (url === '/api/v1/decks') return response([]);
      if (url === '/api/v1/auth/logout') return response({ error: { code: 'unavailable', message: 'Please try signing out again.' } }, 503);
      throw new Error(`Unexpected request: ${url}`);
    });
    const { queryClient } = renderApp();

    const account = await screen.findByRole('button', { name: `${t('signedIn')}: Morgan` });
    await user.click(account);
    await user.click(await screen.findByRole('menuitem', { name: t('signOut') }));

    expect(await screen.findByRole('alert')).toHaveProperty('textContent', 'Please try signing out again.');
    expect(queryClient.getQueryData(meQueryKey)).toEqual(signedInResponse);
    expect(screen.getByRole('main').contains(screen.getByRole('alert'))).toBe(true);
    await user.click(account);
    expect(await screen.findByRole('menuitem', { name: t('signOut') })).toBeTruthy();
  });

  it('prevents duplicate sign-out requests while the first request is pending', async () => {
    const user = userEvent.setup();
    let completeLogout: (value: Response) => void = () => undefined;
    const logoutResponse = new Promise<Response>((resolve) => { completeLogout = resolve; });
    setFetchHandler(async (url) => {
      if (url === '/api/v1/me') return response(signedInResponse);
      if (url === '/api/v1/llm/status') return response({ enabled: false });
      if (url === '/api/v1/decks') return response([]);
      if (url === '/api/v1/auth/logout') return logoutResponse;
      throw new Error(`Unexpected request: ${url}`);
    });
    renderApp();

    const account = await screen.findByRole('button', { name: `${t('signedIn')}: Morgan` });
    await user.click(account);
    await user.click(await screen.findByRole('menuitem', { name: t('signOut') }));
    expect(account.getAttribute('aria-busy')).toBe('true');
    await user.click(account);
    const pendingSignOut = await screen.findByRole('menuitem', { name: t('signingOut') });
    expect(pendingSignOut.getAttribute('aria-disabled')).toBe('true');
    await user.click(pendingSignOut);
    expect(calls.filter(({ url }) => url === '/api/v1/auth/logout')).toHaveLength(1);

    completeLogout(response(undefined, 204));
    expect(await screen.findByRole('heading', { name: t('signInHeading') })).toBeTruthy();
  });

  it('clears cached content when the active session changes owners', async () => {
    let meCalls = 0;
    setFetchHandler(async (url) => {
      if (url === '/api/v1/me') {
        meCalls += 1;
        return meCalls === 1
          ? response(signedInResponse)
          : response({ ...signedInResponse, user: { ...signedInResponse.user, id: 2, sync_scope: 2, name: 'Alex' } });
      }
      if (url === '/api/v1/llm/status') return response({ enabled: false });
      if (url === '/api/v1/decks') return response(meCalls === 1 ? [deck] : []);
      if (url === '/api/v1/decks/42/notes') return response([]);
      if (url === '/api/v1/import/batches') return response([]);
      throw new Error(`Unexpected request: ${url}`);
    });
    const { queryClient } = renderApp();

    await screen.findByRole('heading', { name: 'German course' });
    queryClient.setQueryData(['deck-notes', '42'], [{ id: 7, term: 'Wort' }]);
    queryClient.setQueryData(['private-test-query'], { owner: 1 });

    await queryClient.invalidateQueries({ queryKey: meQueryKey });
    expect(await screen.findByText('Alex')).toBeTruthy();
    await waitFor(() => expect(screen.queryByRole('heading', { name: 'German course' })).toBeNull());
    expect(queryClient.getQueryData(['deck-notes', '42'])).toBeUndefined();
    expect(queryClient.getQueryData(['private-test-query'])).toBeUndefined();
  });

  it('routes administrators to LLM settings', async () => {
    setFetchHandler(async (url) => {
      if (url === '/api/v1/me') return response({
        ...signedInResponse,
        user: { ...signedInResponse.user, is_admin: true },
      });
      if (url === '/api/v1/llm/status') return response({ enabled: false });
      if (url === '/api/v1/admin/llm-usage') return response({ users: [] });
      throw new Error(`Unexpected request: ${url}`);
    });
    window.history.replaceState(null, '', '/admin');
    renderApp();

    expect(await screen.findByRole('heading', { name: t('adminLlmHeading') })).toBeTruthy();
    expect(screen.getByRole('link', { name: t('admin') }).getAttribute('href')).toBe('/admin');
    expect(screen.queryByRole('region', { name: t('decksWorkspace') })).toBeNull();
  });

  it('routes signed-in users to analytics and loads the stats API', async () => {
    const year = new Date().getUTCFullYear();
    const dayCount = new Date(Date.UTC(year + 1, 0, 0)).getUTCDate();
    setFetchHandler(async (url) => {
      if (url === '/api/v1/me') return response(signedInResponse);
      if (url === '/api/v1/stats/overview') return response({
        timezone: 'UTC', day_start_hour: 4, streak_days: 0,
        today: { reviews: 0, new_cards: 0, review_cards: 0, recalled_reviews: 0, retention: 0, duration_ms: 0 },
        cards: { total: 0, new: 0, learning: 0, review: 0, relearning: 0, due: 0, young: 0, mature: 0, leeches: 0 },
        reviews: { total: 0, total_duration_ms: 0, average_duration_ms: 0 },
        known_right_now: 0, desired_retention: 0.9,
      });
      if (url === '/api/v1/stats/distribution') return response({
        states: [], stability: [], difficulty: [], leeches: [], deck_median_stability: [],
      });
      if (url.startsWith('/api/v1/stats/forecast?')) return response({
        days: 30,
        due: Array.from({ length: 30 }, (_, index) => ({
          date: new Date(Date.UTC(year, 0, index + 1)).toISOString().slice(0, 10), cards: 0,
        })),
      });
      if (url === '/api/v1/stats/retention?period=week') return response({ period: 'week', desired_retention: 0.9, points: [] });
      if (url === `/api/v1/stats/heatmap?year=${year}`) return response({
        year,
        days: Array.from({ length: dayCount }, (_, index) => ({
          date: new Date(Date.UTC(year, 0, index + 1)).toISOString().slice(0, 10), reviews: 0, duration_ms: 0,
        })),
        vocabulary_growth: [],
      });
      throw new Error(`Unexpected request: ${url}`);
    });
    window.history.replaceState(null, '', '/stats');
    renderApp();

    expect(await screen.findByRole('heading', { name: t('statsHeading') })).toBeTruthy();
    expect(screen.getByRole('link', { name: t('stats') }).getAttribute('aria-current')).toBe('page');
    expect(calls.map(({ url }) => url)).toContain('/api/v1/stats/overview');
    expect(calls.map(({ url }) => url)).toContain('/api/v1/stats/distribution');
  });
});
