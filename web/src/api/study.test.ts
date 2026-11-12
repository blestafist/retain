import { afterEach, describe, expect, it, vi } from 'vitest';
import { createUUIDv7, getStudyQueue, submitStudyReview, undoStudyReview } from './study';

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

  it('posts a review and undo through the core routes', async () => {
    const fetchMock = vi.fn<typeof fetch>()
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
  });

  it('creates canonical UUIDv7 identifiers', () => {
    const id = createUUIDv7(1_735_689_600_000, { getRandomValues: (bytes) => bytes });
    expect(id).toMatch(/^01941f29-7c00-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
  });
});
