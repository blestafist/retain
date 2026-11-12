import { ApiError } from './client';
import type { DeckId } from './content';

export type StudyRating = 1 | 2 | 3 | 4;
export type StudyMode = 'flip';

export interface StudyCard {
  id: number;
  note_id: number;
  deck_id?: number;
  direction: string;
  state: number;
  due: string;
  stability?: number;
  difficulty?: number;
  elapsed_days: number;
  scheduled_days: number;
  step: number;
  reps: number;
  lapses: number;
  last_review?: string;
}

export interface StudyPreview {
  state: number;
  due: string;
  scheduled_days: number;
  interval: string;
}

export interface QueueCard extends StudyCard {
  front: string;
  back: string;
  previews: Record<StudyRating, StudyPreview>;
}

export interface ReviewInput {
  id: string;
  card_id: number;
  rating: StudyRating;
  mode: StudyMode;
  reviewed_at: string;
  duration_ms: number;
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

function isDateString(value: unknown): value is string {
  return typeof value === 'string' && value.length > 0 && Number.isFinite(Date.parse(value));
}

function invalidResponseError(status: number): ApiError {
  return new ApiError(status, 'invalid_response');
}

function decodeCard(value: unknown, status = 200): StudyCard {
  if (!isRecord(value)
    || !isPositiveInteger(value.id)
    || !isPositiveInteger(value.note_id)
    || (value.deck_id !== undefined && !isPositiveInteger(value.deck_id))
    || typeof value.direction !== 'string'
    || !isNonNegativeInteger(value.state)
    || value.state > 3
    || !isDateString(value.due)
    || !isNonNegativeInteger(value.elapsed_days)
    || !isNonNegativeInteger(value.scheduled_days)
    || !isNonNegativeInteger(value.step)
    || !isNonNegativeInteger(value.reps)
    || !isNonNegativeInteger(value.lapses)
    || (value.stability !== undefined && typeof value.stability !== 'number')
    || (value.difficulty !== undefined && typeof value.difficulty !== 'number')
    || (value.last_review !== undefined && !isDateString(value.last_review))) {
    throw invalidResponseError(status);
  }
  return {
    id: value.id,
    note_id: value.note_id,
    ...(value.deck_id === undefined ? {} : { deck_id: value.deck_id as number }),
    direction: value.direction,
    state: value.state,
    due: value.due,
    ...(value.stability === undefined ? {} : { stability: value.stability as number }),
    ...(value.difficulty === undefined ? {} : { difficulty: value.difficulty as number }),
    elapsed_days: value.elapsed_days,
    scheduled_days: value.scheduled_days,
    step: value.step,
    reps: value.reps,
    lapses: value.lapses,
    ...(value.last_review === undefined ? {} : { last_review: value.last_review as string }),
  };
}

function decodePreview(value: unknown, status: number): StudyPreview {
  if (!isRecord(value)
    || !isNonNegativeInteger(value.state)
    || value.state > 3
    || !isDateString(value.due)
    || !isNonNegativeInteger(value.scheduled_days)
    || typeof value.interval !== 'string') {
    throw invalidResponseError(status);
  }
  return {
    state: value.state,
    due: value.due,
    scheduled_days: value.scheduled_days,
    interval: value.interval,
  };
}

export function decodeQueueCards(value: unknown, status = 200): QueueCard[] {
  if (!Array.isArray(value)) throw invalidResponseError(status);
  return value.map((item) => {
    if (!isRecord(item) || typeof item.front !== 'string' || typeof item.back !== 'string' || !isRecord(item.previews)) {
      throw invalidResponseError(status);
    }
    const card = decodeCard(item, status);
    const previews = {} as Record<StudyRating, StudyPreview>;
    for (const rating of [1, 2, 3, 4] as const) {
      previews[rating] = decodePreview(item.previews[String(rating)], status);
    }
    return { ...card, front: item.front, back: item.back, previews };
  });
}

export function decodeStudyCard(value: unknown, status = 200): StudyCard {
  return decodeCard(value, status);
}

export function createUUIDv7(
  now = Date.now(),
  cryptoSource: Pick<Crypto, 'getRandomValues'> = globalThis.crypto,
): string {
  const timestamp = Math.floor(now);
  if (!Number.isSafeInteger(timestamp) || timestamp < 0 || timestamp > 0xffffffffffff) {
    throw new RangeError('UUIDv7 timestamp must fit in 48 bits.');
  }
  if (!cryptoSource || typeof cryptoSource.getRandomValues !== 'function') {
    throw new Error('Cryptographic randomness is unavailable.');
  }
  const bytes = cryptoSource.getRandomValues(new Uint8Array(16));
  let remaining = BigInt(timestamp);
  for (let index = 5; index >= 0; index -= 1) {
    bytes[index] = Number(remaining & 0xffn);
    remaining >>= 8n;
  }
  bytes[6] = (bytes[6]! & 0x0f) | 0x70;
  bytes[8] = (bytes[8]! & 0x3f) | 0x80;
  const hex = Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('');
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
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
  if (isRecord(body) && isRecord(body.error)) {
    return new ApiError(
      status,
      typeof body.error.code === 'string' ? body.error.code : 'request_failed',
      typeof body.error.message === 'string' ? body.error.message : undefined,
    );
  }
  return new ApiError(status, 'request_failed', statusText || undefined);
}

async function request(path: string, options: { method?: string; body?: unknown } = {}): Promise<unknown> {
  const headers = new Headers({ Accept: 'application/json' });
  if (options.body !== undefined) headers.set('Content-Type', 'application/json');
  const response = await fetch(`/api/v1${path}`, {
    method: options.method,
    credentials: 'same-origin',
    headers,
    body: options.body === undefined ? undefined : JSON.stringify(options.body),
  });
  const text = await response.text();
  const body = parseBody(text);
  if (!response.ok) throw errorFromResponse(response.status, response.statusText, body);
  if (!text || typeof body === 'string') throw invalidResponseError(response.status);
  return body;
}

export async function getStudyQueue({ deckId = null, limit = 50 }: {
  deckId?: DeckId | null;
  limit?: number;
} = {}): Promise<QueueCard[]> {
  const params = new URLSearchParams();
  params.set('deck_id', deckId === null ? '' : String(deckId));
  params.set('limit', String(limit));
  return decodeQueueCards(await request(`/study/queue?${params.toString()}`));
}

export async function submitStudyReview(input: ReviewInput): Promise<StudyCard> {
  return decodeStudyCard(await request('/study/review', { method: 'POST', body: input }));
}

export async function undoStudyReview(reviewId: string): Promise<StudyCard> {
  return decodeStudyCard(await request('/study/undo', {
    method: 'POST',
    body: { review_id: reviewId },
  }));
}
