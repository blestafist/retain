// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
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
    ...render(<QueryClientProvider client={queryClient}><App /></QueryClientProvider>),
    queryClient,
  };
}

function unauthorized() {
  return response({ error: { code: 'unauthorized', message: 'Sign in required.' } }, 401);
}

const signedInResponse = {
  user: { id: 1, email: 'friend@example.com', name: 'Morgan', is_admin: false },
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

    expect(await screen.findByRole('heading', { name: t('decksWorkspace') })).toBeTruthy();
    expect(screen.getByRole('heading', { name: 'German course' })).toBeTruthy();
    expect(screen.getByText('Morgan')).toBeTruthy();
    expect(meCalls).toBe(2);
  });

  it('uses the current-user query for session state and clears it after logout', async () => {
    const user = userEvent.setup();
    setFetchHandler(async (url) => {
      if (url === '/api/v1/me') return response(signedInResponse);
      if (url === '/api/v1/decks') return response([]);
      if (url === '/api/v1/auth/logout') return response(undefined, 204);
      throw new Error(`Unexpected request: ${url}`);
    });
    const { queryClient } = renderApp();

    expect(await screen.findByText('Morgan')).toBeTruthy();
    expect(screen.getByText('friend@example.com')).toBeTruthy();
    queryClient.setQueryData(['deck-notes', '42'], [{ id: 7, term: 'Wort' }]);
    expect(queryClient.getQueryData(['decks'])).toBeTruthy();
    await user.click(screen.getByRole('button', { name: t('signOut') }));
    await screen.findByRole('heading', { name: t('signInHeading') });

    expect(calls.map(({ url }) => url)).toEqual(['/api/v1/me', '/api/v1/decks', '/api/v1/auth/logout']);
    await waitFor(() => expect(screen.queryByText('Morgan')).toBeNull());
    expect(queryClient.getQueryData(['decks'])).toBeUndefined();
    expect(queryClient.getQueryData(['deck-notes', '42'])).toBeUndefined();
  });

  it('clears cached content when the active session changes owners', async () => {
    let meCalls = 0;
    setFetchHandler(async (url) => {
      if (url === '/api/v1/me') {
        meCalls += 1;
        return meCalls === 1
          ? response(signedInResponse)
          : response({ ...signedInResponse, user: { ...signedInResponse.user, id: 2, name: 'Alex' } });
      }
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
});
