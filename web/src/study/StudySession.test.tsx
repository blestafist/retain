// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
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

function jsonResponse(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
}

function renderSession() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const rendered = render(<QueryClientProvider client={queryClient}><StudySession /></QueryClientProvider>);
  return { ...rendered, queryClient };
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

  it('keeps the same review ID and payload when retrying an uncertain request', async () => {
    const user = userEvent.setup();
    let attempts = 0;
    const requestBodies: string[] = [];
    const fetchMock = vi.fn<typeof fetch>((input, init) => {
      const url = String(input);
      if (url === '/api/v1/study/queue?deck_id=&limit=50') return Promise.resolve(jsonResponse([queueCard]));
      if (url === '/api/v1/decks') return Promise.resolve(jsonResponse([]));
      if (url === '/api/v1/study/review') {
        requestBodies.push(String(init?.body));
        attempts += 1;
        if (attempts === 1) return Promise.resolve(jsonResponse({ error: { code: 'temporarily_unavailable' } }, 503));
        return Promise.resolve(jsonResponse({ ...queueCard }));
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    renderSession();
    await screen.findByRole('heading', { name: 'hello' });
    await user.click(screen.getByRole('button', { name: t('showAnswer') }));
    await user.click(screen.getByRole('button', { name: new RegExp(t('good')) }));
    expect(await screen.findByRole('button', { name: t('retryReview') })).toBeTruthy();
    await user.click(screen.getByRole('button', { name: t('retryReview') }));
    expect(await screen.findByRole('heading', { name: t('studyComplete') })).toBeTruthy();

    expect(requestBodies).toHaveLength(2);
    expect(requestBodies[1]).toBe(requestBodies[0]);
  });

  it('allows a new rating after the server definitively rejects a review', async () => {
    const user = userEvent.setup();
    const requestBodies: Array<Record<string, unknown>> = [];
    let attempts = 0;
    const fetchMock = vi.fn<typeof fetch>((input, init) => {
      const url = String(input);
      if (url === '/api/v1/study/queue?deck_id=&limit=50') return Promise.resolve(jsonResponse([queueCard]));
      if (url === '/api/v1/decks') return Promise.resolve(jsonResponse([]));
      if (url === '/api/v1/study/review') {
        requestBodies.push(JSON.parse(String(init?.body)) as Record<string, unknown>);
        attempts += 1;
        if (attempts === 1) return Promise.resolve(jsonResponse({ error: { code: 'invalid_input', message: 'Invalid rating.' } }, 400));
        return Promise.resolve(jsonResponse({ ...queueCard }));
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    renderSession();
    await screen.findByRole('heading', { name: 'hello' });
    await user.click(screen.getByRole('button', { name: t('showAnswer') }));
    await user.click(screen.getByRole('button', { name: new RegExp(t('good')) }));
    expect(await screen.findByRole('alert')).toBeTruthy();
    expect(screen.queryByRole('button', { name: t('retryReview') })).toBeNull();
    await user.click(screen.getByRole('button', { name: new RegExp(t('again')) }));
    expect(await screen.findByRole('heading', { name: t('studyComplete') })).toBeTruthy();
    expect(requestBodies.map(({ rating }) => rating)).toEqual([3, 1]);
    expect(requestBodies[0]?.id).not.toBe(requestBodies[1]?.id);
  });

  it('prevents undo while a review request has an uncertain outcome', async () => {
    const user = userEvent.setup();
    const secondCard = { ...queueCard, id: 6, front: 'second', back: 'zweite' };
    let attempts = 0;
    const fetchMock = vi.fn<typeof fetch>((input) => {
      const url = String(input);
      if (url === '/api/v1/study/queue?deck_id=&limit=50') return Promise.resolve(jsonResponse([queueCard, secondCard]));
      if (url === '/api/v1/decks') return Promise.resolve(jsonResponse([]));
      if (url === '/api/v1/study/review') {
        attempts += 1;
        return Promise.resolve(attempts === 1
          ? jsonResponse({ ...queueCard })
          : jsonResponse({ error: { code: 'temporarily_unavailable' } }, 503));
      }
      if (url === '/api/v1/study/undo') return Promise.resolve(jsonResponse({ ...queueCard }));
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    renderSession();
    await screen.findByRole('heading', { name: 'hello' });
    await user.click(screen.getByRole('button', { name: t('showAnswer') }));
    await user.click(screen.getByRole('button', { name: new RegExp(t('good')) }));
    await screen.findByRole('heading', { name: 'second' });
    await user.click(screen.getByRole('button', { name: t('showAnswer') }));
    await user.click(screen.getByRole('button', { name: new RegExp(t('good')) }));
    const undoButton = screen.getByRole('button', { name: t('undo') }) as HTMLButtonElement;
    expect(undoButton.disabled).toBe(true);
    expect(screen.getByRole('button', { name: t('retryReview') })).toBeTruthy();
  });

  it('keeps the session queue stable when the server refetch omits a reviewed card', async () => {
    const user = userEvent.setup();
    const secondCard = { ...queueCard, id: 6, front: 'second', back: 'zweite' };
    const thirdCard = { ...queueCard, id: 7, front: 'third', back: 'dritte' };
    let queueRequests = 0;
    const fetchMock = vi.fn<typeof fetch>((input) => {
      const url = String(input);
      if (url === '/api/v1/study/queue?deck_id=&limit=50') {
        queueRequests += 1;
        return Promise.resolve(jsonResponse(queueRequests === 1 ? [queueCard, secondCard, thirdCard] : [secondCard, thirdCard]));
      }
      if (url === '/api/v1/decks') return Promise.resolve(jsonResponse([]));
      if (url === '/api/v1/study/review') return Promise.resolve(jsonResponse({ ...queueCard }));
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    const { queryClient } = renderSession();
    await screen.findByRole('heading', { name: 'hello' });
    await user.click(screen.getByRole('button', { name: t('showAnswer') }));
    await user.click(screen.getByRole('button', { name: new RegExp(t('good')) }));
    expect(await screen.findByRole('heading', { name: 'second' })).toBeTruthy();

    await act(async () => {
      await queryClient.refetchQueries({ queryKey: ['study-queue', ''] });
    });
    expect(screen.getByRole('heading', { name: 'second' })).toBeTruthy();
    expect(screen.queryByRole('heading', { name: 'third' })).toBeNull();
  });

  it('loads another queue batch instead of treating the first fifty cards as completion', async () => {
    const user = userEvent.setup();
    const firstBatch = Array.from({ length: 50 }, (_, index) => ({
      ...queueCard,
      id: index + 1,
      front: `card ${index + 1}`,
    }));
    const nextCard = { ...queueCard, id: 1000, front: 'card 51' };
    let queueRequests = 0;
    let nextQueueBody: unknown;
    const fetchMock = vi.fn<typeof fetch>((input, init) => {
      const url = String(input);
      if (url === '/api/v1/study/queue?deck_id=&limit=50') {
        queueRequests += 1;
        return Promise.resolve(jsonResponse(firstBatch));
      }
      if (url === '/api/v1/study/queue/next') {
        queueRequests += 1;
        nextQueueBody = JSON.parse(String(init?.body));
        return Promise.resolve(jsonResponse([nextCard]));
      }
      if (url === '/api/v1/decks') return Promise.resolve(jsonResponse([]));
      if (url === '/api/v1/study/review') return Promise.resolve(jsonResponse({ ...queueCard }));
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    renderSession();
    await screen.findByRole('heading', { name: 'card 1' });
    for (let index = 0; index < firstBatch.length; index += 1) {
      await user.click(screen.getByRole('button', { name: t('showAnswer') }));
      await user.click(screen.getByRole('button', { name: new RegExp(t('good')) }));
    }

    expect(screen.queryByRole('heading', { name: t('studyComplete') })).toBeNull();
    await user.click(screen.getByRole('button', { name: t('loadMoreStudyCards') }));
    expect(await screen.findByRole('heading', { name: 'card 51' })).toBeTruthy();
    expect(queueRequests).toBe(2);
    expect(nextQueueBody).toMatchObject({ deck_id: null, limit: 50, exclude_card_ids: firstBatch.map(({ id }) => id) });
  });

  it('rates a revealed card with Again when swiped left on touch', async () => {
    const user = userEvent.setup();
    let reviewBody: unknown;
    const fetchMock = vi.fn<typeof fetch>((input, init) => {
      const url = String(input);
      if (url === '/api/v1/study/queue?deck_id=&limit=50') return Promise.resolve(jsonResponse([queueCard]));
      if (url === '/api/v1/decks') return Promise.resolve(jsonResponse([]));
      if (url === '/api/v1/study/review') {
        reviewBody = JSON.parse(String(init?.body));
        return Promise.resolve(jsonResponse({ ...queueCard }));
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    renderSession();
    await screen.findByRole('heading', { name: 'hello' });
    await user.click(screen.getByRole('button', { name: t('showAnswer') }));
    const card = screen.getByRole('article', { name: t('studyCard') });
    fireEvent.pointerDown(card, { pointerId: 1, pointerType: 'touch', isPrimary: true, clientX: 200, clientY: 100 });
    fireEvent.pointerUp(card, { pointerId: 1, pointerType: 'touch', isPrimary: true, clientX: 100, clientY: 105 });

    await screen.findByRole('heading', { name: t('studyComplete') });
    expect(reviewBody).toMatchObject({ card_id: queueCard.id, rating: 1 });
  });
});
