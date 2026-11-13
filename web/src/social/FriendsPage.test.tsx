// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { FriendsPage } from './FriendsPage';
import { t } from '../i18n/en';

const fetchMock = vi.fn<typeof fetch>();

function jsonResponse(body?: unknown, status = 200) {
  return new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
  });
}

function renderPage() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(<QueryClientProvider client={queryClient}><FriendsPage userId={7} /></QueryClientProvider>);
}

beforeEach(() => {
  fetchMock.mockReset();
  vi.stubGlobal('fetch', fetchMock);
  vi.stubGlobal('confirm', vi.fn(() => true));
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('FriendsPage', () => {
  it('requests friends without account discovery and shares a deck with an accepted friend', async () => {
    const user = userEvent.setup();
    const requests: unknown[] = [];
    let shares: Array<Record<string, unknown>> = [];
    let setting = { visible: false, updated_at: '2026-11-09T12:00:00Z' };
    fetchMock.mockImplementation(async (input, init) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (url === '/api/v1/decks' && method === 'GET') return jsonResponse([{
        id: 11, owner_id: 7, can_edit: true, name: 'German', parent_id: null, children: [],
      }]);
      if (url === '/api/v1/friends' && method === 'GET') return jsonResponse([{ id: 9, email: 'friend@example.test', name: 'Friend', friends_at: '2026-11-01T00:00:00Z' }]);
      if (url === '/api/v1/friends/requests' && method === 'GET') return jsonResponse(requests);
      if (url === '/api/v1/friends/requests' && method === 'POST') {
        expect(JSON.parse(String(init?.body))).toEqual({ email: 'unknown@example.test' });
        return jsonResponse({ accepted: true }, 202);
      }
      if (url === '/api/v1/social/settings' && method === 'GET') return jsonResponse(setting);
      if (url === '/api/v1/social/settings' && method === 'PATCH') {
        setting = { ...setting, visible: (JSON.parse(String(init?.body)) as { visible: boolean }).visible };
        return jsonResponse(setting);
      }
      if (url === '/api/v1/friends/activity' && method === 'GET') return jsonResponse([]);
      if (url === '/api/v1/friends/leaderboard?period=week' && method === 'GET') return jsonResponse([{
        rank: 1, user_id: 7, name: 'Learner', review_count: 3, learned_count: 1, streak_days: 2,
      }]);
      if (url === '/api/v1/friends/shares' && method === 'GET') return jsonResponse(shares);
      if (url === '/api/v1/decks/11/shares' && method === 'POST') {
        expect(JSON.parse(String(init?.body))).toEqual({ recipient_id: 9, mode: 'copy' });
        shares = [{
          id: 31, source_deck_id: 11, source_deck_name: 'German', source_owner_id: 7,
          source_owner_name: 'Learner', recipient_id: 9, recipient_name: 'Friend',
          mode: 'copy', status: 'complete', role: 'owner',
        }];
        return jsonResponse(shares[0], 201);
      }
      throw new Error(`Unexpected request: ${method} ${url}`);
    });

    renderPage();
    await screen.findByRole('heading', { name: t('friendsHeading') });
    await screen.findByRole('option', { name: 'Friend' });

    await user.type(screen.getByLabelText(t('friendEmail')), 'unknown@example.test');
    await user.click(screen.getByRole('button', { name: t('sendFriendRequest') }));
    await screen.findByText(t('friendRequestSent'));

    await user.click(screen.getByRole('checkbox', { name: t('socialVisibility') }));
    await waitFor(() => expect(setting.visible).toBe(true));

    await user.selectOptions(screen.getByLabelText(t('chooseDeckToShare')), '11');
    await user.selectOptions(screen.getByLabelText(t('chooseFriend')), '9');
    await user.click(screen.getByRole('radio', { name: new RegExp(t('copyMode')) }));
    await user.click(screen.getByRole('button', { name: t('shareDeck') }));
    await screen.findByText(t('sharingSaved'));
    await screen.findByText(new RegExp(`German · ${t('shareCopyStatus')}`));
    expect(fetchMock.mock.calls.some(([url, init]) => String(url) === '/api/v1/decks/11/shares' && init?.method === 'POST')).toBe(true);
  });

  it('shows only aggregates in the leaderboard and uses accessible section labels', async () => {
    fetchMock.mockImplementation(async (input) => {
      const url = String(input);
      if (url === '/api/v1/decks') return jsonResponse([]);
      if (url === '/api/v1/friends') return jsonResponse([]);
      if (url === '/api/v1/friends/requests') return jsonResponse([]);
      if (url === '/api/v1/social/settings') return jsonResponse({ visible: false, updated_at: '2026-11-09T12:00:00Z' });
      if (url === '/api/v1/friends/activity') return jsonResponse([]);
      if (url === '/api/v1/friends/leaderboard?period=week') return jsonResponse([{
        rank: 1, user_id: 7, name: 'Learner', review_count: 3, learned_count: 1, streak_days: 2,
      }]);
      if (url === '/api/v1/friends/shares') return jsonResponse([]);
      throw new Error(`Unexpected request: ${url}`);
    });
    renderPage();

    const reviews = await screen.findByText(t('weeklyReviews').replace('{count}', '3'));
    const leaderboard = screen.getByRole('list', { name: t('weeklyLeaderboard') });
    expect(reviews).toBeTruthy();
    expect(within(leaderboard).getByText(t('weeklyReviews').replace('{count}', '3'))).toBeTruthy();
    expect(within(leaderboard).getByText(t('weeklyLearned').replace('{count}', '1'))).toBeTruthy();
    expect(within(leaderboard).queryByText(/Wort|German deck|review ID/i)).toBeNull();
    expect(screen.getByRole('region', { name: t('socialActivity') })).toBeTruthy();
  });
});
