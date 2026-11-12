// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
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
      if (url === '/api/v1/llm/status') return Promise.resolve(jsonResponse({ enabled: false }));
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
      if (url === '/api/v1/llm/status') return Promise.resolve(jsonResponse({ enabled: false }));
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
      if (url === '/api/v1/llm/status') return Promise.resolve(jsonResponse({ enabled: false }));
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
      if (url === '/api/v1/llm/status') return Promise.resolve(jsonResponse({ enabled: false }));
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
      if (url === '/api/v1/llm/status') return Promise.resolve(jsonResponse({ enabled: false }));
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

  it('keeps session completion visible if a background queue refetch fails', async () => {
    const user = userEvent.setup();
    let queueRequests = 0;
    const fetchMock = vi.fn<typeof fetch>((input) => {
      const url = String(input);
      if (url === '/api/v1/llm/status') return Promise.resolve(jsonResponse({ enabled: false }));
      if (url === '/api/v1/study/queue?deck_id=&limit=50') {
        queueRequests += 1;
        return Promise.resolve(queueRequests === 1
          ? jsonResponse([queueCard])
          : jsonResponse({ error: { code: 'temporarily_unavailable' } }, 503));
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
    expect(await screen.findByRole('heading', { name: t('studyComplete') })).toBeTruthy();

    await act(async () => {
      await queryClient.refetchQueries({ queryKey: ['study-queue', ''] });
    });
    expect(screen.getByRole('heading', { name: t('studyComplete') })).toBeTruthy();
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
      if (url === '/api/v1/llm/status') return Promise.resolve(jsonResponse({ enabled: false }));
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
      if (url === '/api/v1/llm/status') return Promise.resolve(jsonResponse({ enabled: false }));
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

  it('checks typed answers, exposes the character bar and suggestion, and logs the selected mode', async () => {
    const user = userEvent.setup();
    const fetchMock = vi.fn<typeof fetch>((input) => {
      const url = String(input);
      if (url === '/api/v1/llm/status') return Promise.resolve(jsonResponse({ enabled: false }));
      if (url === '/api/v1/study/queue?deck_id=&limit=50') return Promise.resolve(jsonResponse([queueCard]));
      if (url === '/api/v1/decks') return Promise.resolve(jsonResponse([{ id: 2, name: 'German', lang_target: 'de', lang_native: 'pl' }]));
      if (url === '/api/v1/study/check-answer') return Promise.resolve(jsonResponse({ match: 'exact', suggested_rating: 3 }));
      if (url === '/api/v1/study/review') return Promise.resolve(jsonResponse({ ...queueCard, state: 1 }));
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    renderSession();
    await screen.findByRole('heading', { name: 'hello' });
    await user.click(screen.getByRole('button', { name: t('typingMode') }));
    const input = screen.getByRole('textbox', { name: t('typeYourAnswer') });
    await user.type(input, 'cze');
    await user.click(screen.getByRole('button', { name: t('insertCharacter').replace('{character}', 'ą') }));
    expect(input).toHaveProperty('value', 'czeą');
    await user.clear(input);
    await user.type(input, 'hallo');
    await user.click(screen.getByRole('button', { name: t('checkAnswer') }));

    expect(await screen.findByText(t('answerExact'))).toBeTruthy();
    expect(screen.getByText(t('suggestedRating').replace('{rating}', t('good')))).toBeTruthy();
    const ratingButtons = within(screen.getByRole('group', { name: t('ratingGroup') })).getAllByRole('button');
    expect(ratingButtons.every((button) => !button.hasAttribute('aria-pressed'))).toBe(true);
    const checkRequest = fetchMock.mock.calls.find(([url]) => url === '/api/v1/study/check-answer');
    expect(JSON.parse(String(checkRequest?.[1]?.body))).toEqual({ card_id: 5, answer: 'hallo' });

    await user.click(screen.getByRole('button', { name: /Hard/ }));
    await screen.findByRole('heading', { name: t('studyComplete') });
    const reviewRequest = fetchMock.mock.calls.find(([url]) => url === '/api/v1/study/review');
    expect(JSON.parse(String(reviewRequest?.[1]?.body))).toMatchObject({ card_id: 5, rating: 2, mode: 'typing' });
  });

  it('offers synonym checking for a rejected typed answer when LLM is enabled', async () => {
    const user = userEvent.setup();
    const fetchMock = vi.fn<typeof fetch>((input) => {
      const url = String(input);
      if (url === '/api/v1/llm/status') return Promise.resolve(jsonResponse({ enabled: true }));
      if (url === '/api/v1/study/queue?deck_id=&limit=50') return Promise.resolve(jsonResponse([queueCard]));
      if (url === '/api/v1/decks') return Promise.resolve(jsonResponse([{ id: 2, name: 'German', lang_target: 'de', lang_native: 'pl' }]));
      if (url === '/api/v1/study/check-answer') return Promise.resolve(jsonResponse({ match: 'wrong', suggested_rating: 1 }));
      if (url === '/api/v1/llm/check-answer') return Promise.resolve(jsonResponse({ is_synonym: true, explanation: 'A natural equivalent.' }));
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    renderSession();
    await screen.findByRole('heading', { name: 'hello' });
    await user.click(screen.getByRole('button', { name: t('typingMode') }));
    await user.type(screen.getByRole('textbox', { name: t('typeYourAnswer') }), 'bonjour');
    await user.click(screen.getByRole('button', { name: t('checkAnswer') }));
    expect(await screen.findByText(t('answerWrong'))).toBeTruthy();
    await user.click(await screen.findByRole('button', { name: t('checkSynonym') }));
    expect(await screen.findByText(t('synonymYes'))).toBeTruthy();
    expect(JSON.parse(String(fetchMock.mock.calls.find(([url]) => url === '/api/v1/llm/check-answer')?.[1]?.body)))
      .toEqual({ card_id: 5, answer: 'bonjour' });
  });

  it('loads server choices, checks the selection, and records choice mode', async () => {
    const user = userEvent.setup();
    const fetchMock = vi.fn<typeof fetch>((input) => {
      const url = String(input);
      if (url === '/api/v1/llm/status') return Promise.resolve(jsonResponse({ enabled: false }));
      if (url === '/api/v1/study/queue?deck_id=&limit=50') return Promise.resolve(jsonResponse([queueCard]));
      if (url === '/api/v1/decks') return Promise.resolve(jsonResponse([]));
      if (url === '/api/v1/study/choice-options') return Promise.resolve(jsonResponse({ choices: ['hallo', 'ciao', 'bonjour', 'adios'] }));
      if (url === '/api/v1/study/check-answer') return Promise.resolve(jsonResponse({ match: 'exact', suggested_rating: 3 }));
      if (url === '/api/v1/study/review') return Promise.resolve(jsonResponse({ ...queueCard, state: 1 }));
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    renderSession();
    await screen.findByRole('heading', { name: 'hello' });
    await user.click(screen.getByRole('button', { name: t('choiceMode') }));
    await user.click(await screen.findByRole('button', { name: 'hallo' }));

    expect(await screen.findByText(t('answerExact'))).toBeTruthy();
    expect(JSON.parse(String(fetchMock.mock.calls.find(([url]) => url === '/api/v1/study/choice-options')?.[1]?.body)))
      .toEqual({ card_id: 5 });
    expect(JSON.parse(String(fetchMock.mock.calls.find(([url]) => url === '/api/v1/study/check-answer')?.[1]?.body)))
      .toEqual({ card_id: 5, answer: 'hallo', mode: 'choice' });
    await user.click(screen.getByRole('button', { name: /Hard/ }));
    await screen.findByRole('heading', { name: t('studyComplete') });
    expect(JSON.parse(String(fetchMock.mock.calls.find(([url]) => url === '/api/v1/study/review')?.[1]?.body)))
      .toMatchObject({ card_id: 5, rating: 2, mode: 'choice' });
  });

  it('ignores a pending typed-answer result after undo changes the current card', async () => {
    const user = userEvent.setup();
    let resolveCheck!: (value: Response) => void;
    const nextCard = { ...queueCard, id: 6, note_id: 9, front: 'world', back: 'Welt' };
    const fetchMock = vi.fn<typeof fetch>((input) => {
      const url = String(input);
      if (url === '/api/v1/llm/status') return Promise.resolve(jsonResponse({ enabled: false }));
      if (url === '/api/v1/study/queue?deck_id=&limit=50') return Promise.resolve(jsonResponse([queueCard, nextCard]));
      if (url === '/api/v1/decks') return Promise.resolve(jsonResponse([]));
      if (url === '/api/v1/study/check-answer') return new Promise((resolve) => { resolveCheck = resolve; });
      if (url === '/api/v1/study/review' || url === '/api/v1/study/undo') return Promise.resolve(jsonResponse({ ...queueCard, state: 1 }));
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    renderSession();
    await screen.findByRole('heading', { name: 'hello' });
    await user.click(screen.getByRole('button', { name: t('showAnswer') }));
    await user.click(screen.getByRole('button', { name: /Good/ }));
    await screen.findByRole('heading', { name: 'world' });
    await user.click(screen.getByRole('button', { name: t('typingMode') }));
    await user.type(screen.getByRole('textbox', { name: t('typeYourAnswer') }), 'Welt');
    await user.click(screen.getByRole('button', { name: t('checkAnswer') }));
    await waitFor(() => expect(resolveCheck).toBeTypeOf('function'));
    await user.click(screen.getByRole('button', { name: t('undo') }));
    await screen.findByRole('heading', { name: 'hello' });

    await act(async () => resolveCheck(jsonResponse({ match: 'exact', suggested_rating: 3 })));
    expect(screen.queryByText(t('answerExact'))).toBeNull();
    expect(screen.queryByText('hallo')).toBeNull();
    expect(screen.getByRole('textbox', { name: t('typeYourAnswer') })).toBeTruthy();
  });
});
