import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { fsrs } from 'ts-fsrs';
import {
  getRetrievability,
  previewCard,
  reviewCard,
  type CardState,
  type FsrsCard,
  type FsrsSettings,
} from './scheduler';

interface GoldenParameters {
  request_retention?: number;
  maximum_interval?: number;
  enable_fuzz?: boolean;
  weights?: number[];
  learning_steps?: string[];
  relearning_steps?: string[];
}

interface GoldenCard {
  state: string;
  due?: string;
  stability?: number;
  difficulty?: number;
  elapsed_days?: number;
  scheduled_days?: number;
  step?: number;
  reps?: number;
  lapses?: number;
  last_review?: string;
}

interface GoldenReview {
  at: string;
  rating: 1 | 2 | 3 | 4;
  expected: GoldenCard;
  review_log: {
    state: string;
    scheduled_days: number;
    elapsed_days: number;
    reviewed_at: string;
  };
}

interface GoldenSequence {
  name: string;
  parameters?: GoldenParameters;
  initial: GoldenCard;
  reviews: GoldenReview[];
}

interface GoldenFile {
  format: string;
  parameters: GoldenParameters;
  sequences: GoldenSequence[];
}

const golden = JSON.parse(
  readFileSync(new URL('../../../testdata/fsrs_golden.json', import.meta.url), 'utf8'),
) as GoldenFile;
const stateNames = ['new', 'learning', 'review', 'relearning'] as const;

describe('shared FSRS golden vectors', () => {
  it('pins the shared file to FSRS-5, 19 weights, and no fuzz', () => {
    expect(golden.format).toBe('retain/fsrs-golden/v1');
    expect(golden.parameters.weights).toHaveLength(19);
    expect(golden.parameters.weights).toEqual([...fsrs().parameters.w]);
    expect(golden.parameters.enable_fuzz).toBe(false);
  });

  for (const sequence of golden.sequences) {
    it(sequence.name, () => {
      const settings = mergeSettings(golden.parameters, sequence.parameters);
      const card = cardFromGolden(sequence.initial);

      for (const [index, review] of sequence.reviews.entries()) {
        const result = reviewCard(card, review.rating, new Date(review.at), settings);
        assertCard(result.card, review.expected, `${sequence.name} review ${index}`);
        expect(result.log.rating).toBe(review.rating);
        expect(stateNames[result.log.state]).toBe(review.review_log.state);
        expect(result.log.scheduled_days).toBe(review.review_log.scheduled_days);
        expect(result.log.elapsed_days).toBe(review.review_log.elapsed_days);
        expect(result.log.reviewed_at.getTime()).toBe(new Date(review.review_log.reviewed_at).getTime());
        Object.assign(card, result.card);
      }
    });
  }
});

describe('client scheduler contract', () => {
  it('previews each rating through the same deterministic review path', () => {
    const settings = mergeSettings(golden.parameters);
    const card: FsrsCard = {
      due: new Date('2025-01-02T03:04:05Z'),
      stability: 20,
      difficulty: 5,
      elapsed_days: 30,
      scheduled_days: 30,
      step: 0,
      reps: 8,
      lapses: 1,
      state: 2,
      last_review: new Date('2024-12-03T03:04:05Z'),
    };
    const now = new Date('2025-01-02T03:04:05Z');
    const previews = previewCard(card, now, settings);

    for (const rating of [1, 2, 3, 4] as const) {
      expect(previews[rating]).toEqual(reviewCard(card, rating, now, settings));
    }
  });

  it('uses the FSRS forgetting curve and handles cards without recall history', () => {
    const settings = mergeSettings(golden.parameters);
    const reviewed: FsrsCard = {
      ...cardFromGolden({
        state: 'review',
        due: '2025-01-06T00:00:00Z',
        stability: 5,
        difficulty: 5,
        last_review: '2025-01-01T00:00:00Z',
      }),
    };
    expect(getRetrievability(reviewed, new Date('2025-01-06T00:00:00Z'), settings)).toBeCloseTo(0.9, 6);
    expect(getRetrievability(cardFromGolden({ state: 'new' }), new Date(), settings)).toBe(0);
  });

  it('rejects an invalid stored learning-step index rather than silently rescheduling it', () => {
    const settings = mergeSettings(golden.parameters);
    const card = cardFromGolden({
      state: 'learning',
      due: '2025-01-02T03:14:05Z',
      stability: 3.173,
      difficulty: 5.2,
      step: 99,
      last_review: '2025-01-02T03:04:05Z',
    });
    expect(() => reviewCard(card, 3, new Date('2025-01-02T03:14:05Z'), settings)).toThrow(RangeError);
  });

  it('rejects an invalid rating at the client boundary', () => {
    expect(() => reviewCard(cardFromGolden({ state: 'new' }), 5 as 1, new Date(), mergeSettings(golden.parameters)))
      .toThrow(RangeError);
  });
});

function mergeSettings(base: GoldenParameters, override?: GoldenParameters): FsrsSettings {
  const parameters = { ...base, ...override };
  return {
    request_retention: parameters.request_retention ?? 0.9,
    maximum_interval: parameters.maximum_interval ?? 36500,
    learning_steps: (parameters.learning_steps ?? ['1m', '10m']).join(' '),
    relearning_steps: (parameters.relearning_steps ?? ['10m']).join(' '),
    fsrs_weights: parameters.weights ?? null,
  };
}

function cardFromGolden(value: GoldenCard): FsrsCard {
  const state = stateNames.indexOf(value.state as (typeof stateNames)[number]);
  if (state < 0) throw new Error(`Unknown golden state: ${value.state}`);
  return {
    due: value.due ? new Date(value.due) : new Date(0),
    stability: value.stability ?? 0,
    difficulty: value.difficulty ?? 0,
    elapsed_days: value.elapsed_days ?? 0,
    scheduled_days: value.scheduled_days ?? 0,
    step: value.step ?? 0,
    reps: value.reps ?? 0,
    lapses: value.lapses ?? 0,
    state: state as CardState,
    last_review: value.last_review ? new Date(value.last_review) : null,
  };
}

function assertCard(got: FsrsCard, want: GoldenCard, context: string): void {
  expect(stateNames[got.state], `${context} state`).toBe(want.state);
  expect(got.due.getTime(), `${context} due`).toBe(new Date(want.due ?? 0).getTime());
  expect(got.stability, `${context} stability`).toBeCloseTo(want.stability ?? 0, 6);
  expect(got.difficulty, `${context} difficulty`).toBeCloseTo(want.difficulty ?? 0, 6);
  expect(got.elapsed_days, `${context} elapsed days`).toBe(want.elapsed_days ?? 0);
  expect(got.scheduled_days, `${context} scheduled days`).toBe(want.scheduled_days ?? 0);
  expect(got.step, `${context} step`).toBe(want.step ?? 0);
  expect(got.reps, `${context} reps`).toBe(want.reps ?? 0);
  expect(got.lapses, `${context} lapses`).toBe(want.lapses ?? 0);
  const lastReview = want.last_review ? new Date(want.last_review).getTime() : null;
  expect(got.last_review?.getTime() ?? null, `${context} last review`).toBe(lastReview);
}
