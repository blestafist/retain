import { ApiError } from './client';

export type JobState = 'queued' | 'running' | 'done' | 'failed';

export interface DeckEnrichmentItem {
  note_id: number | string;
  term: string;
  missing_fields: string[];
  suggestions: Record<string, unknown>;
}

export interface DeckEnrichmentResult {
  items: DeckEnrichmentItem[];
  partial: boolean;
  omitted_count: number;
}

export interface JobStatus {
  id: string;
  kind: 'deck_enrich';
  status: JobState;
  progress: number;
  total: number;
  error: string | null;
  result?: DeckEnrichmentResult;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function invalidResponseError(): ApiError {
  return new ApiError(200, 'invalid_response');
}

function isNonNegativeInteger(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
}

function decodeEnrichmentResult(value: unknown): DeckEnrichmentResult {
  if (!isRecord(value) || !Array.isArray(value.items) || typeof value.partial !== 'boolean'
    || !isNonNegativeInteger(value.omitted_count)
    || value.partial !== (value.omitted_count > 0)) {
    throw invalidResponseError();
  }

  const items = value.items.map((item): DeckEnrichmentItem => {
    if (!isRecord(item)
      || !(typeof item.note_id === 'string' || (typeof item.note_id === 'number' && Number.isFinite(item.note_id)))
      || typeof item.term !== 'string'
      || !Array.isArray(item.missing_fields)
      || !item.missing_fields.every((field) => typeof field === 'string')
      || !isRecord(item.suggestions)) {
      throw invalidResponseError();
    }
    return {
      note_id: item.note_id,
      term: item.term,
      missing_fields: item.missing_fields,
      suggestions: item.suggestions,
    };
  });
  return { items, partial: value.partial, omitted_count: value.omitted_count };
}

export function decodeJobStatus(value: unknown): JobStatus {
  if (!isRecord(value)
    || typeof value.id !== 'string' || value.id.length === 0
    || value.kind !== 'deck_enrich'
    || (value.status !== 'queued' && value.status !== 'running' && value.status !== 'done' && value.status !== 'failed')
    || !isNonNegativeInteger(value.progress)
    || !isNonNegativeInteger(value.total)
    || value.progress > value.total
    || !(typeof value.error === 'string' || value.error === null)) {
    throw invalidResponseError();
  }

  if (value.status === 'done') {
    if (!('result' in value)) throw invalidResponseError();
    return {
      id: value.id,
      kind: 'deck_enrich',
      status: value.status,
      progress: value.progress,
      total: value.total,
      error: value.error,
      result: decodeEnrichmentResult(value.result),
    };
  }

  if ('result' in value) throw invalidResponseError();
  return {
    id: value.id,
    kind: 'deck_enrich',
    status: value.status,
    progress: value.progress,
    total: value.total,
    error: value.error,
  };
}

export async function getJobStatus(id: string): Promise<JobStatus> {
  const response = await fetch(`/api/v1/jobs/${encodeURIComponent(id)}`, {
    credentials: 'same-origin',
    headers: { Accept: 'application/json' },
  });
  const text = await response.text();
  let body: unknown;
  if (text) {
    try {
      body = JSON.parse(text) as unknown;
    } catch {
      body = undefined;
    }
  }

  if (!response.ok) {
    if (isRecord(body) && isRecord(body.error)) {
      throw new ApiError(
        response.status,
        typeof body.error.code === 'string' ? body.error.code : 'request_failed',
        typeof body.error.message === 'string' ? body.error.message : undefined,
      );
    }
    throw new ApiError(response.status, 'request_failed');
  }
  return decodeJobStatus(body);
}
