import { afterEach, describe, expect, it, vi } from 'vitest';
import { checkStudyAnswer, createUUIDv7, decodeChoiceOptions, getNextStudyQueue, getStudyChoiceOptions, getStudyQueue, submitStudyReview, undoStudyReview } from './study';

const card = {
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
};

function queueCard() {
  return {
    ...card,
    front: 'hello',
    back: 'hallo',
    previews: Object.fromEntries([1, 2, 3, 4].map((rating) => [rating, {
      state: 1,
      due: '2025-01-01T00:01:00Z',
      scheduled_days: 0,
      interval: '1m0s',
    }])),
  };
}

afterEach(() => vi.unstubAllGlobals());

describe('study API', () => {
  it('loads a validated queue with all four interval previews', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify([queueCard()]), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }));
    vi.stubGlobal('fetch', fetchMock);

    const queue = await getStudyQueue({ deckId: 2, limit: 10 });

    expect(queue[0]?.front).toBe('hello');
    expect(queue[0]?.previews[4]?.interval).toBe('1m0s');
    expect(String(fetchMock.mock.calls[0]?.[0])).toContain('/study/queue?deck_id=2&limit=10');
  });

  it('requests a subsequent queue batch while excluding cards from the active session', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify([queueCard()]), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }));
    vi.stubGlobal('fetch', fetchMock);

    const queue = await getNextStudyQueue({ deckId: '2', limit: 10, excludeCardIds: [5, 6] });

    expect(queue).toHaveLength(1);
    expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/v1/study/queue/next');
    expect(fetchMock.mock.calls[0]?.[1]?.method).toBe('POST');
    expect(JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body))).toEqual({
      deck_id: 2,
      limit: 10,
      exclude_card_ids: [5, 6],
    });
  });

  it('posts a review and undo through the core routes', async () => {
    const fetchMock = vi.fn<typeof fetch>()
      .mockResolvedValueOnce(new Response(JSON.stringify(card), { status: 200, headers: { 'Content-Type': 'application/json' } }))
      .mockResolvedValueOnce(new Response(JSON.stringify(card), { status: 200, headers: { 'Content-Type': 'application/json' } }))
      .mockResolvedValueOnce(new Response(JSON.stringify(card), { status: 200, headers: { 'Content-Type': 'application/json' } }));
    vi.stubGlobal('fetch', fetchMock);
    const review = {
      id: '018f0f3e-7b00-7000-8000-000000000031',
      card_id: 5,
      rating: 3 as const,
      mode: 'flip' as const,
      reviewed_at: '2025-01-01T00:00:00Z',
      duration_ms: 900,
    };

    await submitStudyReview(review);
    await undoStudyReview(review.id);

    expect(JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body))).toEqual(review);
    expect(fetchMock.mock.calls[1]?.[0]).toBe('/api/v1/study/undo');
    expect(JSON.parse(String(fetchMock.mock.calls[1]?.[1]?.body))).toEqual({ review_id: review.id });

    await submitStudyReview({ ...review, id: '018f0f3e-7b00-7000-8000-000000000032', mode: 'typing' });
    expect(JSON.parse(String(fetchMock.mock.calls[2]?.[1]?.body))).toMatchObject({ mode: 'typing' });
  });

  it('requests validated choice options and checks typed or selected answers', async () => {
    const choices = ['Hund', 'Katze', 'Vogel', 'Fisch'];
    const fetchMock = vi.fn<typeof fetch>()
      .mockResolvedValueOnce(new Response(JSON.stringify({ choices }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ match: 'article', suggested_rating: 2 }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ match: 'wrong', suggested_rating: 1 }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
    vi.stubGlobal('fetch', fetchMock);

    await expect(getStudyChoiceOptions(5)).resolves.toEqual({ choices });
    await expect(checkStudyAnswer(5, 'die Katze')).resolves.toEqual({ match: 'article', suggested_rating: 2 });
    await expect(checkStudyAnswer(5, 'Katze', 'choice')).resolves.toEqual({ match: 'wrong', suggested_rating: 1 });

    expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/v1/study/choice-options');
    expect(fetchMock.mock.calls[0]?.[1]?.body).toBe(JSON.stringify({ card_id: 5 }));
    expect(fetchMock.mock.calls[1]?.[1]?.body).toBe(JSON.stringify({ card_id: 5, answer: 'die Katze' }));
    expect(fetchMock.mock.calls[2]?.[1]?.body).toBe(JSON.stringify({ card_id: 5, answer: 'Katze', mode: 'choice' }));
    expect(() => decodeChoiceOptions({ choices: ['one', 'two', 'three'] })).toThrow();
    expect(() => decodeChoiceOptions({ choices: ['same', 'same', 'third', 'fourth'] })).toThrow();
  });

  it('creates canonical UUIDv7 identifiers', () => {
    const id = createUUIDv7(1_735_689_600_000, { getRandomValues: (bytes) => bytes });
    expect(id).toMatch(/^01941f29-7c00-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
  });
});
