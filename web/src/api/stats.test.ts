import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  decodeStatsCardCurve,
  decodeStatsDistribution,
  decodeStatsForecast,
  decodeStatsHeatmap,
  decodeStatsOverview,
  decodeStatsRetention,
  getStatsCardCurve,
  getStatsDistribution,
  getStatsForecast,
  getStatsHeatmap,
  getStatsOverview,
  getStatsRetention,
} from './stats';

const fetchMock = vi.fn<typeof fetch>();

function response(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function overview() {
  return {
    timezone: 'Europe/Warsaw',
    day_start_hour: 4,
    streak_days: 7,
    today: {
      reviews: 12,
      new_cards: 2,
      review_cards: 10,
      recalled_reviews: 9,
      retention: 0.9,
      duration_ms: 48000,
    },
    cards: {
      total: 80,
      new: 10,
      learning: 8,
      review: 50,
      relearning: 2,
      due: 11,
      young: 20,
      mature: 30,
      leeches: 1,
    },
    reviews: { total: 420, total_duration_ms: 1200000, average_duration_ms: 2857 },
    known_right_now: 41.25,
    desired_retention: 0.9,
  };
}

function curve() {
  return {
    card_id: 42,
    term: 'die Katze',
    direction: 'forward',
    state: 2,
    stability: 12.5,
    difficulty: 4.2,
    last_review: '2026-11-09T12:00:00Z',
    retrievability: 0.94,
    reviews: [{
      reviewed_at: '2026-11-09T12:00:00Z',
      rating: 3,
      state_before: 2,
      retrievability: 0.89,
      stability_after: 12.5,
      difficulty_after: 4.2,
      scheduled_days: 12,
    }],
    curve: [{ at: '2026-11-09T12:00:00Z', retrievability: 1 }],
  };
}

function distribution() {
  return {
    states: [
      { state: 0, total: 10, young: 0, mature: 0 },
      { state: 2, total: 50, young: 20, mature: 30 },
    ],
    stability: [{ bucket: '0–1', count: 8 }],
    difficulty: [{ bucket: '1–2', count: 6 }],
    leeches: [{ card_id: 42, term: 'die Katze', direction: 'forward', lapses: 8 }],
    deck_median_stability: [{ deck_id: 7, deck_name: 'Chapter 1', median_stability: 9.5 }],
  };
}

beforeEach(() => {
  fetchMock.mockReset();
  vi.stubGlobal('fetch', fetchMock);
});

afterEach(() => vi.unstubAllGlobals());

describe('stats API decoders', () => {
  it('decodes overview values without coercion and allows known-card estimates above one', () => {
    expect(decodeStatsOverview(overview())).toMatchObject({
      today: { reviews: 12, retention: 0.9 },
      cards: { due: 11 },
      known_right_now: 41.25,
    });
    expect(() => decodeStatsOverview({ ...overview(), today: { ...overview().today, reviews: '12' } })).toThrow();
    expect(() => decodeStatsOverview({ ...overview(), today: { ...overview().today, retention: 1.1 } })).toThrow();
    expect(() => decodeStatsOverview({ ...overview(), cards: { ...overview().cards, due: -1 } })).toThrow();
  });

  it('requires a complete forecast with unique calendar dates', () => {
    const forecast = { days: 2, due: [{ date: '2026-11-11', cards: 4 }, { date: '2026-11-12', cards: 2 }] };
    expect(decodeStatsForecast(forecast).due).toHaveLength(2);
    expect(() => decodeStatsForecast({ ...forecast, due: forecast.due.slice(0, 1) })).toThrow();
    expect(() => decodeStatsForecast({ ...forecast, due: [{ date: '2026-02-30', cards: 1 }, forecast.due[1]] })).toThrow();
  });

  it('validates retention period labels and recall counts', () => {
    const retention = {
      period: 'week',
      desired_retention: 0.9,
      points: [{ period: '2026-W45', review_cards: 10, recalled_reviews: 9, actual_retention: 0.9 }],
    };
    expect(decodeStatsRetention(retention).points[0]?.actual_retention).toBe(0.9);
    expect(() => decodeStatsRetention({ ...retention, points: [{ ...retention.points[0], period: '2026-45' }] })).toThrow();
    expect(() => decodeStatsRetention({ ...retention, points: [{ ...retention.points[0], recalled_reviews: 11 }] })).toThrow();
  });

  it('validates annual heatmap days and monthly vocabulary growth', () => {
    const heatmap = {
      year: 2026,
      days: [{ date: '2026-01-01', reviews: 3, duration_ms: 9000 }],
      vocabulary_growth: [{ month: '2026-01', language: 'de', learned: 2 }],
    };
    expect(decodeStatsHeatmap(heatmap).days[0]?.reviews).toBe(3);
    expect(() => decodeStatsHeatmap({ ...heatmap, days: [{ ...heatmap.days[0], date: '2025-12-31' }] })).toThrow();
    expect(() => decodeStatsHeatmap({ ...heatmap, vocabulary_growth: [{ ...heatmap.vocabulary_growth[0], learned: 1.5 }] })).toThrow();
  });

  it('validates card curve probabilities, dates, and FSRS values', () => {
    expect(decodeStatsCardCurve(curve()).curve[0]?.retrievability).toBe(1);
    expect(() => decodeStatsCardCurve({ ...curve(), retrievability: Number.NaN })).toThrow();
    expect(() => decodeStatsCardCurve({ ...curve(), reviews: [{ ...curve().reviews[0], rating: 5 }] })).toThrow();
  });

  it('validates distribution states, histograms, leeches, and deck medians', () => {
    expect(decodeStatsDistribution(distribution()).deck_median_stability[0]?.median_stability).toBe(9.5);
    expect(() => decodeStatsDistribution({ ...distribution(), states: [...distribution().states, distribution().states[0]] })).toThrow();
    expect(() => decodeStatsDistribution({ ...distribution(), deck_median_stability: [{ ...distribution().deck_median_stability[0], median_stability: -1 }] })).toThrow();
    expect(() => decodeStatsDistribution({ ...distribution(), stability: [{ bucket: ' ', count: 3 }] })).toThrow();
  });
});

describe('stats requests', () => {
  it('calls every endpoint with the documented query and strictly decodes responses', async () => {
    fetchMock
      .mockResolvedValueOnce(response(overview()))
      .mockResolvedValueOnce(response({ days: 30, due: Array.from({ length: 30 }, (_, index) => ({ date: new Date(Date.UTC(2026, 10, index + 1)).toISOString().slice(0, 10), cards: index })) }))
      .mockResolvedValueOnce(response({ period: 'week', desired_retention: 0.9, points: [] }))
      .mockResolvedValueOnce(response({ year: 2026, days: [], vocabulary_growth: [] }))
      .mockResolvedValueOnce(response(curve()))
      .mockResolvedValueOnce(response(distribution()));

    await getStatsOverview();
    await getStatsForecast();
    await getStatsRetention();
    await getStatsHeatmap(2026);
    await getStatsCardCurve(42);
    await getStatsDistribution();

    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      '/api/v1/stats/overview',
      '/api/v1/stats/forecast?days=30',
      '/api/v1/stats/retention?period=week',
      '/api/v1/stats/heatmap?year=2026',
      '/api/v1/stats/cards/42/curve',
      '/api/v1/stats/distribution',
    ]);
    expect(fetchMock.mock.calls.every(([, init]) => init?.credentials === 'same-origin')).toBe(true);
  });

  it('rejects invalid request arguments without making a request', async () => {
    await expect(getStatsForecast(0)).rejects.toThrow(RangeError);
    await expect(getStatsHeatmap(1969)).rejects.toThrow(RangeError);
    await expect(getStatsCardCurve(0)).rejects.toThrow(RangeError);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('preserves API errors and rejects malformed JSON responses', async () => {
    fetchMock.mockResolvedValueOnce(response({ error: { code: 'unauthorized', message: 'Sign in required.' } }, 401));
    await expect(getStatsOverview()).rejects.toMatchObject({ code: 'unauthorized', status: 401 });

    fetchMock.mockResolvedValueOnce(new Response('not json', { status: 200, headers: { 'Content-Type': 'application/json' } }));
    await expect(getStatsOverview()).rejects.toMatchObject({ code: 'invalid_response' });
  });
});
