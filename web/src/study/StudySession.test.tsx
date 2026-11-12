// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { StudySession } from './StudySession';
import { t } from '../i18n/en';

const queueCard = {
  id: 5,
  note_id: 8,
  deck_id: 2,
  direction: 'forward',
  state: 0,
  due: '2025-01-01T00:00:00Z',
  elapsed_days: 0,
  scheduled_days: 0,
  step: 0,
  reps: 0,
  lapses: 0,
  front: 'hello',
  back: 'hallo',
  previews: Object.fromEntries([1, 2, 3, 4].map((rating) => [rating, {
    state: 1,
    due: '2025-01-01T00:01:00Z',
    scheduled_days: 0,
    interval: '1m0s',
  }])),
};

function jsonResponse(value: unknown) {
  return new Response(JSON.stringify(value), { status: 200, headers: { 'Content-Type': 'application/json' } });
}

function renderSession() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={queryClient}><StudySession /></QueryClientProvider>);
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('study session', () => {
  it('reveals, rates, completes, and undoes the latest review', async () => {
    const user = userEvent.setup();
    const fetchMock = vi.fn<typeof fetch>((input) => {
      const url = String(input);
      if (url === '/api/v1/study/queue?deck_id=&limit=50') return Promise.resolve(jsonResponse([queueCard]));
      if (url === '/api/v1/decks') return Promise.resolve(jsonResponse([]));
      if (url === '/api/v1/study/review' || url === '/api/v1/study/undo') return Promise.resolve(jsonResponse({ ...queueCard }));
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    renderSession();
    expect(await screen.findByRole('heading', { name: 'hello' })).toBeTruthy();
    await user.click(screen.getByRole('button', { name: t('showAnswer') }));
    expect(screen.getByText('hallo')).toBeTruthy();
    await user.click(screen.getByRole('button', { name: new RegExp(t('good')) }));
    expect(await screen.findByRole('heading', { name: t('studyComplete') })).toBeTruthy();

    await user.click(screen.getByRole('button', { name: t('undo') }));
    expect(await screen.findByRole('heading', { name: 'hello' })).toBeTruthy();
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toContain('/api/v1/study/undo');
  });
});
