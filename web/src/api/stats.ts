import { ApiError } from './client';

export interface StatsToday {
  reviews: number;
  new_cards: number;
  review_cards: number;
  recalled_reviews: number;
  retention: number;
  duration_ms: number;
}

export interface StatsCardTotals {
  total: number;
  new: number;
  learning: number;
  review: number;
  relearning: number;
  due: number;
  young: number;
  mature: number;
  leeches: number;
}

export interface StatsReviewTotals {
  total: number;
  total_duration_ms: number;
  average_duration_ms: number;
}

export interface StatsOverview {
  timezone: string;
  day_start_hour: number;
  streak_days: number;
  today: StatsToday;
  cards: StatsCardTotals;
  reviews: StatsReviewTotals;
  known_right_now: number;
  desired_retention: number;
}

export interface StatsDueDay {
  date: string;
  cards: number;
}

export interface StatsForecast {
  days: number;
  due: StatsDueDay[];
}

export interface StatsRetentionPoint {
  period: string;
  review_cards: number;
  recalled_reviews: number;
  actual_retention: number;
}

export interface StatsRetention {
  period: 'week' | 'month' | 'year';
  desired_retention: number;
  points: StatsRetentionPoint[];
}

export interface StatsActivityDay {
  date: string;
  reviews: number;
  duration_ms: number;
}

export interface StatsVocabularyGrowth {
  month: string;
  language: string;
  learned: number;
}

export interface StatsHeatmap {
  year: number;
  days: StatsActivityDay[];
  vocabulary_growth: StatsVocabularyGrowth[];
}

export interface StatsCurveReview {
  reviewed_at: string;
  rating: number;
  state_before: number;
  retrievability: number;
  stability_after: number;
  difficulty_after: number;
  scheduled_days: number;
}

export interface StatsCurvePoint {
  at: string;
  retrievability: number;
}

export interface StatsCardCurve {
  card_id: number;
  term: string;
  direction: string;
  state: number;
  stability?: number;
  difficulty?: number;
  last_review?: string;
  retrievability: number;
  reviews: StatsCurveReview[];
  curve: StatsCurvePoint[];
}

export interface StatsStateDistribution {
  state: number;
  total: number;
  young: number;
  mature: number;
}

export interface StatsHistogramBin {
  bucket: string;
  count: number;
}

export interface StatsLeech {
  card_id: number;
  term: string;
  direction: string;
  lapses: number;
}

export interface StatsDeckMedian {
  deck_id: number;
  deck_name: string;
  median_stability: number;
}

export interface StatsDistribution {
  states: StatsStateDistribution[];
  stability: StatsHistogramBin[];
  difficulty: StatsHistogramBin[];
  leeches: StatsLeech[];
  deck_median_stability: StatsDeckMedian[];
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isNonNegativeInteger(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
}

function isPositiveInteger(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value > 0;
}

function isFiniteNumber(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value);
}

function isProbability(value: unknown): value is number {
  return isFiniteNumber(value) && value >= 0 && value <= 1;
}

function isISODate(value: unknown): value is string {
  if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}$/.test(value)) return false;
  const date = new Date(`${value}T00:00:00.000Z`);
  return Number.isFinite(date.getTime()) && date.toISOString().slice(0, 10) === value;
}

function isRFC3339(value: unknown): value is string {
  if (typeof value !== 'string') return false;
  const parts = /^(\d{4}-\d{2}-\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d+)?(Z|([+-])(\d{2}):(\d{2}))$/.exec(value);
  if (!parts || !isISODate(parts[1])) return false;
  const hour = Number(parts[2]);
  const minute = Number(parts[3]);
  const second = Number(parts[4]);
  const offsetHour = parts[7] === undefined ? 0 : Number(parts[7]);
  const offsetMinute = parts[8] === undefined ? 0 : Number(parts[8]);
  return hour < 24 && minute < 60 && second < 60 && offsetHour < 24 && offsetMinute < 60 && Number.isFinite(Date.parse(value));
}

function invalidResponse(status = 200): ApiError {
  return new ApiError(status, 'invalid_response');
}

function decodeNonNegativeFields<T extends string>(value: Record<string, unknown>, keys: readonly T[], status: number): Record<T, number> {
  const result = {} as Record<T, number>;
  for (const key of keys) {
    if (!isNonNegativeInteger(value[key])) throw invalidResponse(status);
    result[key] = value[key];
  }
  return result;
}

export function decodeStatsOverview(value: unknown, status = 200): StatsOverview {
  if (!isRecord(value)
    || typeof value.timezone !== 'string' || value.timezone.trim() === ''
    || !isNonNegativeInteger(value.day_start_hour) || value.day_start_hour > 23
    || !isNonNegativeInteger(value.streak_days)
    || !isRecord(value.today) || !isRecord(value.cards) || !isRecord(value.reviews)
    || !isFiniteNumber(value.known_right_now) || value.known_right_now < 0
    || !isFiniteNumber(value.desired_retention) || value.desired_retention <= 0 || value.desired_retention > 1) {
    throw invalidResponse(status);
  }

  const todayCounts = decodeNonNegativeFields(value.today, ['reviews', 'new_cards', 'review_cards', 'recalled_reviews', 'duration_ms'] as const, status);
  const cardCounts = decodeNonNegativeFields(value.cards, ['total', 'new', 'learning', 'review', 'relearning', 'due', 'young', 'mature', 'leeches'] as const, status);
  const reviewCounts = decodeNonNegativeFields(value.reviews, ['total', 'total_duration_ms', 'average_duration_ms'] as const, status);
  if (!isProbability(value.today.retention) || todayCounts.recalled_reviews > todayCounts.review_cards) throw invalidResponse(status);

  return {
    timezone: value.timezone,
    day_start_hour: value.day_start_hour,
    streak_days: value.streak_days,
    today: { ...todayCounts, retention: value.today.retention },
    cards: cardCounts,
    reviews: reviewCounts,
    known_right_now: value.known_right_now,
    desired_retention: value.desired_retention,
  };
}

export function decodeStatsForecast(value: unknown, status = 200): StatsForecast {
  if (!isRecord(value) || !isPositiveInteger(value.days) || value.days > 90 || !Array.isArray(value.due) || value.due.length !== value.days) {
    throw invalidResponse(status);
  }
  const due = value.due.map((item) => {
    if (!isRecord(item) || !isISODate(item.date) || !isNonNegativeInteger(item.cards)) throw invalidResponse(status);
    return { date: item.date, cards: item.cards };
  });
  if (new Set(due.map((item) => item.date)).size !== due.length) throw invalidResponse(status);
  return { days: value.days, due };
}

function isRetentionPeriod(value: unknown): value is StatsRetention['period'] {
  return value === 'week' || value === 'month' || value === 'year';
}

function isPeriodLabel(value: unknown, period: StatsRetention['period']): value is string {
  if (typeof value !== 'string') return false;
  if (period === 'week') return /^\d{4}-W(0[1-9]|[1-4]\d|5[0-3])$/.test(value);
  if (period === 'month') return /^\d{4}-(0[1-9]|1[0-2])$/.test(value);
  return /^\d{4}$/.test(value);
}

export function decodeStatsRetention(value: unknown, status = 200): StatsRetention {
  if (!isRecord(value) || !isRetentionPeriod(value.period)
    || !isProbability(value.desired_retention) || value.desired_retention === 0 || !Array.isArray(value.points)) {
    throw invalidResponse(status);
  }
  const period = value.period;
  const points = value.points.map((item) => {
    if (!isRecord(item) || !isPeriodLabel(item.period, period)
      || !isNonNegativeInteger(item.review_cards) || !isNonNegativeInteger(item.recalled_reviews)
      || item.recalled_reviews > item.review_cards || !isProbability(item.actual_retention)) {
      throw invalidResponse(status);
    }
    return {
      period: item.period,
      review_cards: item.review_cards,
      recalled_reviews: item.recalled_reviews,
      actual_retention: item.actual_retention,
    };
  });
  if (new Set(points.map((point) => point.period)).size !== points.length) throw invalidResponse(status);
  return { period, desired_retention: value.desired_retention, points };
}

export function decodeStatsHeatmap(value: unknown, status = 200): StatsHeatmap {
  if (!isRecord(value) || !isPositiveInteger(value.year) || value.year < 1970 || value.year > 9999
    || !Array.isArray(value.days) || !Array.isArray(value.vocabulary_growth)) {
    throw invalidResponse(status);
  }
  const days = value.days.map((item) => {
    if (!isRecord(item) || !isISODate(item.date) || !item.date.startsWith(`${value.year}-`)
      || !isNonNegativeInteger(item.reviews) || !isNonNegativeInteger(item.duration_ms)) {
      throw invalidResponse(status);
    }
    return { date: item.date, reviews: item.reviews, duration_ms: item.duration_ms };
  });
  const vocabulary_growth = value.vocabulary_growth.map((item) => {
    if (!isRecord(item) || typeof item.month !== 'string' || !/^\d{4}-(0[1-9]|1[0-2])$/.test(item.month)
      || !item.month.startsWith(`${value.year}-`) || typeof item.language !== 'string' || item.language.trim() === ''
      || !isNonNegativeInteger(item.learned)) {
      throw invalidResponse(status);
    }
    return { month: item.month, language: item.language, learned: item.learned };
  });
  if (new Set(days.map((day) => day.date)).size !== days.length) throw invalidResponse(status);
  return { year: value.year, days, vocabulary_growth };
}

export function decodeStatsCardCurve(value: unknown, status = 200): StatsCardCurve {
  if (!isRecord(value) || !isPositiveInteger(value.card_id) || typeof value.term !== 'string'
    || value.term.trim() === '' || typeof value.direction !== 'string' || value.direction.trim() === ''
    || !isNonNegativeInteger(value.state) || value.state > 3
    || (value.stability !== undefined && (!isFiniteNumber(value.stability) || value.stability < 0))
    || (value.difficulty !== undefined && (!isFiniteNumber(value.difficulty) || value.difficulty < 0 || value.difficulty > 10))
    || (value.last_review !== undefined && !isRFC3339(value.last_review))
    || !isProbability(value.retrievability) || !Array.isArray(value.reviews) || !Array.isArray(value.curve)) {
    throw invalidResponse(status);
  }
  const reviews = value.reviews.map((item) => {
    if (!isRecord(item) || !isRFC3339(item.reviewed_at)
      || !isPositiveInteger(item.rating) || item.rating > 4
      || !isNonNegativeInteger(item.state_before) || item.state_before > 3
      || !isProbability(item.retrievability)
      || !isFiniteNumber(item.stability_after) || item.stability_after < 0
      || !isFiniteNumber(item.difficulty_after) || item.difficulty_after < 0 || item.difficulty_after > 10
      || !isNonNegativeInteger(item.scheduled_days)) {
      throw invalidResponse(status);
    }
    return {
      reviewed_at: item.reviewed_at,
      rating: item.rating,
      state_before: item.state_before,
      retrievability: item.retrievability,
      stability_after: item.stability_after,
      difficulty_after: item.difficulty_after,
      scheduled_days: item.scheduled_days,
    };
  });
  const curve = value.curve.map((item) => {
    if (!isRecord(item) || !isRFC3339(item.at) || !isProbability(item.retrievability)) throw invalidResponse(status);
    return { at: item.at, retrievability: item.retrievability };
  });
  return {
    card_id: value.card_id,
    term: value.term,
    direction: value.direction,
    state: value.state,
    ...(value.stability === undefined ? {} : { stability: value.stability }),
    ...(value.difficulty === undefined ? {} : { difficulty: value.difficulty }),
    ...(value.last_review === undefined ? {} : { last_review: value.last_review }),
    retrievability: value.retrievability,
    reviews,
    curve,
  };
}

export function decodeStatsDistribution(value: unknown, status = 200): StatsDistribution {
  if (!isRecord(value) || !Array.isArray(value.states) || !Array.isArray(value.stability)
    || !Array.isArray(value.difficulty) || !Array.isArray(value.leeches) || !Array.isArray(value.deck_median_stability)) {
    throw invalidResponse(status);
  }
  const states = value.states.map((item) => {
    if (!isRecord(item) || !isNonNegativeInteger(item.state) || item.state > 3
      || !isNonNegativeInteger(item.total) || !isNonNegativeInteger(item.young) || !isNonNegativeInteger(item.mature)
      || item.young + item.mature > item.total) {
      throw invalidResponse(status);
    }
    return { state: item.state, total: item.total, young: item.young, mature: item.mature };
  });
  if (new Set(states.map((item) => item.state)).size !== states.length) throw invalidResponse(status);

  const decodeBins = (items: unknown[]): StatsHistogramBin[] => items.map((item) => {
    if (!isRecord(item) || typeof item.bucket !== 'string' || item.bucket.trim() === '' || !isNonNegativeInteger(item.count)) {
      throw invalidResponse(status);
    }
    return { bucket: item.bucket, count: item.count };
  });
  const leeches = value.leeches.map((item) => {
    if (!isRecord(item) || !isPositiveInteger(item.card_id) || typeof item.term !== 'string' || item.term.trim() === ''
      || typeof item.direction !== 'string' || item.direction.trim() === '' || !isNonNegativeInteger(item.lapses)) {
      throw invalidResponse(status);
    }
    return { card_id: item.card_id, term: item.term, direction: item.direction, lapses: item.lapses };
  });
  const deck_median_stability = value.deck_median_stability.map((item) => {
    if (!isRecord(item) || !isPositiveInteger(item.deck_id) || typeof item.deck_name !== 'string'
      || !isFiniteNumber(item.median_stability) || item.median_stability < 0) {
      throw invalidResponse(status);
    }
    return { deck_id: item.deck_id, deck_name: item.deck_name, median_stability: item.median_stability };
  });
  return {
    states,
    stability: decodeBins(value.stability),
    difficulty: decodeBins(value.difficulty),
    leeches,
    deck_median_stability,
  };
}

function parseBody(text: string): unknown {
  if (!text) return undefined;
  try {
    return JSON.parse(text) as unknown;
  } catch {
    return text;
  }
}

function errorFromResponse(status: number, statusText: string, body: unknown): ApiError {
  const error = isRecord(body) && isRecord(body.error) ? body.error : undefined;
  return new ApiError(
    status,
    typeof error?.code === 'string' ? error.code : 'request_failed',
    typeof error?.message === 'string' ? error.message : statusText || undefined,
  );
}

async function request(path: string): Promise<{ value: unknown; status: number }> {
  const response = await fetch(`/api/v1/stats/${path}`, {
    credentials: 'same-origin',
    headers: { Accept: 'application/json' },
  });
  const text = await response.text();
  const body = parseBody(text);
  if (!response.ok) throw errorFromResponse(response.status, response.statusText, body);
  const mediaType = response.headers.get('content-type')?.split(';', 1)[0]?.trim().toLowerCase();
  const hasJSONContentType = mediaType === 'application/json'
    || (mediaType?.startsWith('application/') === true && mediaType.endsWith('+json'));
  if (!text || typeof body === 'string' || !hasJSONContentType) throw invalidResponse(response.status);
  return { value: body, status: response.status };
}

export async function getStatsOverview(): Promise<StatsOverview> {
  const result = await request('overview');
  return decodeStatsOverview(result.value, result.status);
}

export async function getStatsForecast(days = 30): Promise<StatsForecast> {
  if (!Number.isSafeInteger(days) || days < 1 || days > 90) throw new RangeError('Forecast days must be from 1 to 90.');
  const result = await request(`forecast?days=${days}`);
  const decoded = decodeStatsForecast(result.value, result.status);
  if (decoded.days !== days) throw invalidResponse(result.status);
  return decoded;
}

export async function getStatsRetention(period: StatsRetention['period'] = 'week'): Promise<StatsRetention> {
  const result = await request(`retention?period=${period}`);
  const decoded = decodeStatsRetention(result.value, result.status);
  if (decoded.period !== period) throw invalidResponse(result.status);
  return decoded;
}

export async function getStatsHeatmap(year = new Date().getFullYear()): Promise<StatsHeatmap> {
  if (!Number.isSafeInteger(year) || year < 1970 || year > 9999) throw new RangeError('Heatmap year must be valid.');
  const result = await request(`heatmap?year=${year}`);
  const decoded = decodeStatsHeatmap(result.value, result.status);
  if (decoded.year !== year) throw invalidResponse(result.status);
  return decoded;
}

export async function getStatsCardCurve(cardId: number): Promise<StatsCardCurve> {
  if (!isPositiveInteger(cardId)) throw new RangeError('Card ID must be a positive integer.');
  const result = await request(`cards/${cardId}/curve`);
  const decoded = decodeStatsCardCurve(result.value, result.status);
  if (decoded.card_id !== cardId) throw invalidResponse(result.status);
  return decoded;
}

export async function getStatsDistribution(): Promise<StatsDistribution> {
  const result = await request('distribution');
  return decodeStatsDistribution(result.value, result.status);
}
