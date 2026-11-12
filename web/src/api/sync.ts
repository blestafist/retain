import { ApiError } from './client';
import { decodeStudyCard, type ReviewInput, type StudyCard } from './study';

export interface SyncNote {
  id: number;
  sync_seq?: number;
  deck_id: number;
  term: string;
  translation: string;
  transcription: string | null;
  example: string | null;
  example_translation: string | null;
  notes: string | null;
  tags: string[];
  alternatives: string[];
  fields: Record<string, unknown>;
  updated_at: string;
  deleted: boolean;
}

export interface SyncCard extends StudyCard {
  user_id: number;
  suspended: boolean;
  is_leech: boolean;
  deleted: boolean;
}

export interface SyncResponse {
  applied: string[];
  rejected: Array<{ id: string; code: string }>;
  changed_cards: SyncCard[];
  changed_notes: SyncNote[];
  cursor: string;
  has_more: boolean;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isPositiveInteger(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value > 0;
}

function isDateString(value: unknown): value is string {
  return typeof value === 'string' && Number.isFinite(Date.parse(value));
}

function invalidResponse(status: number): ApiError {
  return new ApiError(status, 'invalid_response');
}

function decodeCard(value: unknown, status: number): SyncCard {
  if (!isRecord(value) || !isPositiveInteger(value.id) || typeof value.deleted !== 'boolean') {
    throw invalidResponse(status);
  }
  if (value.sync_seq !== undefined && (!Number.isSafeInteger(value.sync_seq) || typeof value.sync_seq !== 'number' || value.sync_seq < 0)) {
    throw invalidResponse(status);
  }
  if (value.deleted) {
    return { id: value.id, sync_seq: value.sync_seq as number | undefined, deleted: true } as SyncCard;
  }
  const card = decodeStudyCard(value, status);
  if (!isPositiveInteger(value.user_id)
    || typeof value.direction !== 'string'
    || (value.direction !== 'forward' && value.direction !== 'reverse')
    || typeof value.suspended !== 'boolean'
    || typeof value.is_leech !== 'boolean') {
    throw invalidResponse(status);
  }
  return {
    ...card,
    user_id: value.user_id,
    sync_seq: value.sync_seq as number | undefined,
    direction: value.direction,
    suspended: value.suspended,
    is_leech: value.is_leech,
    deleted: false,
  };
}

function decodeNote(value: unknown, status: number): SyncNote {
  if (!isRecord(value) || !isPositiveInteger(value.id) || typeof value.deleted !== 'boolean') {
    throw invalidResponse(status);
  }
  if (value.deleted) {
    if (value.sync_seq !== undefined && (!Number.isSafeInteger(value.sync_seq) || typeof value.sync_seq !== 'number' || value.sync_seq < 0)) {
      throw invalidResponse(status);
    }
    return { id: value.id, sync_seq: value.sync_seq as number | undefined, deleted: true } as SyncNote;
  }
  if (!isPositiveInteger(value.deck_id)
    || typeof value.term !== 'string'
    || typeof value.translation !== 'string'
    || !(typeof value.transcription === 'string' || value.transcription === null)
    || !(typeof value.example === 'string' || value.example === null)
    || !(typeof value.example_translation === 'string' || value.example_translation === null)
    || !(typeof value.notes === 'string' || value.notes === null)
    || !Array.isArray(value.tags) || value.tags.some((tag) => typeof tag !== 'string')
    || !Array.isArray(value.alternatives) || value.alternatives.some((item) => typeof item !== 'string')
    || !isRecord(value.fields)
    || !isDateString(value.updated_at)) {
    throw invalidResponse(status);
  }
  if (value.sync_seq !== undefined && (!Number.isSafeInteger(value.sync_seq) || typeof value.sync_seq !== 'number' || value.sync_seq < 0)) {
    throw invalidResponse(status);
  }
  return {
    id: value.id,
    sync_seq: value.sync_seq as number | undefined,
    deck_id: value.deck_id,
    term: value.term,
    translation: value.translation,
    transcription: value.transcription,
    example: value.example,
    example_translation: value.example_translation,
    notes: value.notes,
    tags: value.tags as string[],
    alternatives: value.alternatives as string[],
    fields: value.fields,
    updated_at: value.updated_at,
    deleted: false,
  };
}

export function decodeSyncResponse(value: unknown, status = 200): SyncResponse {
  if (!isRecord(value)
    || !Array.isArray(value.applied) || value.applied.some((id) => typeof id !== 'string')
    || !Array.isArray(value.rejected)
    || value.rejected.some((item) => !isRecord(item) || typeof item.id !== 'string' || typeof item.code !== 'string')
    || !Array.isArray(value.changed_cards)
    || !Array.isArray(value.changed_notes)
    || typeof value.cursor !== 'string' || value.cursor.length === 0
    || typeof value.has_more !== 'boolean') {
    throw invalidResponse(status);
  }
  return {
    applied: value.applied as string[],
    rejected: value.rejected as Array<{ id: string; code: string }>,
    changed_cards: value.changed_cards.map((card) => decodeCard(card, status)),
    changed_notes: value.changed_notes.map((note) => decodeNote(note, status)),
    cursor: value.cursor,
    has_more: value.has_more,
  };
}

export async function postSync(since: string, reviews: ReviewInput[]): Promise<SyncResponse> {
  const response = await fetch('/api/v1/sync', {
    method: 'POST',
    credentials: 'same-origin',
    headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
    body: JSON.stringify({ since, reviews }),
  });
  const text = await response.text();
  let body: unknown;
  try {
    body = text ? JSON.parse(text) as unknown : undefined;
  } catch {
    body = undefined;
  }
  if (!response.ok) {
    const envelope = isRecord(body) && isRecord(body.error) ? body.error : null;
    throw new ApiError(
      response.status,
      envelope && typeof envelope.code === 'string' ? envelope.code : 'request_failed',
      envelope && typeof envelope.message === 'string' ? envelope.message : response.statusText,
    );
  }
  return decodeSyncResponse(body, response.status);
}
