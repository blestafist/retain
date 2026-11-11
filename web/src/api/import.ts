import { ApiError } from './client';
import type { DeckId, NativeLanguage, TargetLanguage } from './content';

export interface ImportCounts {
  new: number;
  changed: number;
  unchanged: number;
}

export interface ImportDeckCounts {
  new: number;
  changed: number;
  unchanged: number;
}

export interface ImportPreviewDeck {
  external_id?: string;
  name: string;
  status: 'new' | 'changed' | 'unchanged';
  counts: ImportCounts;
  warnings: string[];
  decks: ImportPreviewDeck[];
}

export interface ImportSummary {
  source: string;
  tree: ImportPreviewDeck[];
  counts: ImportCounts;
  deck_counts: ImportDeckCounts;
  warnings: string[];
}

export interface ImportPreviewResult extends ImportSummary {
  batch_id: string;
  adapter: string;
  status: 'previewed' | 'applied';
}

export interface ImportBatch {
  id: string;
  adapter: string;
  filename: string | null;
  sha256: string;
  status: string;
  summary: ImportSummary;
  created_at: string;
  applied_at: string | null;
}

export interface ApplyImportInput {
  lang_target: TargetLanguage;
  lang_native: NativeLanguage;
  parent_deck_id?: DeckId;
  on_missing: 'keep' | 'suspend';
}

export interface DeckExport {
  blob: Blob;
  filename: string;
  contentType: string;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function invalidResponseError(): ApiError {
  return new ApiError(200, 'invalid_response');
}

function decodeCounts(value: unknown): ImportCounts {
  if (!isRecord(value)
    || !Number.isFinite(value.new) || typeof value.new !== 'number'
    || !Number.isFinite(value.changed) || typeof value.changed !== 'number'
    || !Number.isFinite(value.unchanged) || typeof value.unchanged !== 'number') {
    throw invalidResponseError();
  }
  return { new: value.new, changed: value.changed, unchanged: value.unchanged };
}

function decodeStringList(value: unknown): string[] {
  if (!Array.isArray(value) || !value.every((item) => typeof item === 'string')) {
    throw invalidResponseError();
  }
  return value;
}

function decodePreviewDeck(value: unknown): ImportPreviewDeck {
  if (!isRecord(value) || typeof value.name !== 'string'
    || (value.status !== 'new' && value.status !== 'changed' && value.status !== 'unchanged')
    || !Array.isArray(value.decks)) {
    throw invalidResponseError();
  }
  if (value.external_id !== undefined && typeof value.external_id !== 'string') {
    throw invalidResponseError();
  }
  return {
    ...(typeof value.external_id === 'string' ? { external_id: value.external_id } : {}),
    name: value.name,
    status: value.status,
    counts: decodeCounts(value.counts),
    warnings: decodeStringList(value.warnings),
    decks: value.decks.map(decodePreviewDeck),
  };
}

function decodeSummary(value: unknown): ImportSummary {
  if (!isRecord(value) || typeof value.source !== 'string'
    || !Array.isArray(value.tree) || !isRecord(value.deck_counts)) {
    throw invalidResponseError();
  }
  return {
    source: value.source,
    tree: value.tree.map(decodePreviewDeck),
    counts: decodeCounts(value.counts),
    deck_counts: decodeCounts(value.deck_counts),
    warnings: decodeStringList(value.warnings),
  };
}

export function decodeImportPreview(value: unknown): ImportPreviewResult {
  if (!isRecord(value) || typeof value.batch_id !== 'string' || !value.batch_id
    || typeof value.adapter !== 'string'
    || (value.status !== 'previewed' && value.status !== 'applied')) {
    throw invalidResponseError();
  }
  return { ...decodeSummary(value), batch_id: value.batch_id, adapter: value.adapter, status: value.status };
}

export function decodeImportHistory(value: unknown): ImportBatch[] {
  if (!Array.isArray(value)) throw invalidResponseError();
  return value.map((item) => {
    if (!isRecord(item) || typeof item.id !== 'string' || typeof item.adapter !== 'string'
      || !(typeof item.filename === 'string' || item.filename === null)
      || typeof item.sha256 !== 'string' || typeof item.status !== 'string'
      || typeof item.created_at !== 'string' || !Number.isFinite(Date.parse(item.created_at))
      || !(typeof item.applied_at === 'string' || item.applied_at === null)
      || (typeof item.applied_at === 'string' && !Number.isFinite(Date.parse(item.applied_at)))) {
      throw invalidResponseError();
    }
    return {
      id: item.id,
      adapter: item.adapter,
      filename: item.filename,
      sha256: item.sha256,
      status: item.status,
      summary: decodeSummary(item.summary),
      created_at: item.created_at,
      applied_at: item.applied_at,
    };
  });
}

function parseResponseBody(text: string): unknown {
  if (!text) return undefined;
  try {
    return JSON.parse(text) as unknown;
  } catch {
    return text;
  }
}

function apiError(response: Response, body: unknown): ApiError {
  if (isRecord(body) && isRecord(body.error)) {
    return new ApiError(
      response.status,
      typeof body.error.code === 'string' ? body.error.code : 'request_failed',
      typeof body.error.message === 'string' ? body.error.message : undefined,
    );
  }
  return new ApiError(response.status, 'request_failed', response.statusText || undefined);
}

async function requestJson(path: string, init: RequestInit = {}): Promise<unknown> {
  const headers = new Headers(init.headers);
  headers.set('Accept', 'application/json');
  if (init.body !== undefined && !(init.body instanceof FormData)) {
    headers.set('Content-Type', 'application/json');
  }
  const response = await fetch(`/api/v1${path}`, {
    ...init,
    credentials: 'same-origin',
    headers,
  });
  const text = await response.text();
  const body = parseResponseBody(text);
  if (!response.ok) throw apiError(response, body);
  if (typeof body === 'string' || body === undefined) throw invalidResponseError();
  return body;
}

export async function previewImport(file: File, adapter?: string): Promise<ImportPreviewResult> {
  const form = new FormData();
  form.append('file', file, file.name);
  if (adapter) form.append('adapter', adapter);
  return decodeImportPreview(await requestJson('/import/preview', { method: 'POST', body: form }));
}

export async function applyImport(batchId: string, input: ApplyImportInput): Promise<ImportPreviewResult> {
  const body: Record<string, unknown> = {
    lang_target: input.lang_target,
    lang_native: input.lang_native,
    on_missing: input.on_missing,
  };
  if (input.parent_deck_id !== undefined) body.parent_deck_id = input.parent_deck_id;
  return decodeImportPreview(await requestJson(`/import/${encodeURIComponent(batchId)}/apply`, {
    method: 'POST',
    body: JSON.stringify(body),
  }));
}

export async function getImportHistory(): Promise<ImportBatch[]> {
  return decodeImportHistory(await requestJson('/import/batches'));
}

function exportFilename(contentDisposition: string | null, fallback: string): string {
  if (!contentDisposition) return fallback;
  const encoded = contentDisposition.match(/filename\*=UTF-8''([^;]+)/i)?.[1];
  if (encoded) {
    try {
      return decodeURIComponent(encoded.trim().replace(/^"|"$/g, '')) || fallback;
    } catch {
      // Fall through to the plain filename parameter.
    }
  }
  const plain = contentDisposition.match(/filename\s*=\s*(?:"([^"]+)"|([^;]+))/i);
  return (plain?.[1] ?? plain?.[2]?.trim()) || fallback;
}

export async function exportDeck(deckId: DeckId, format: 'retain' | 'csv'): Promise<DeckExport> {
  const response = await fetch(`/api/v1/decks/${encodeURIComponent(String(deckId))}/export?format=${format}`, {
    credentials: 'same-origin',
    headers: { Accept: format === 'csv' ? 'text/csv' : 'application/json' },
  });
  if (!response.ok) {
    const text = await response.text();
    throw apiError(response, parseResponseBody(text));
  }
  const contentType = response.headers.get('content-type') ?? 'application/octet-stream';
  return {
    blob: await response.blob(),
    filename: exportFilename(response.headers.get('content-disposition'), `deck.${format === 'retain' ? 'json' : 'csv'}`),
    contentType,
  };
}

export function downloadExport(file: DeckExport): void {
  const url = URL.createObjectURL(file.blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = file.filename;
  link.hidden = true;
  document.body.append(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(url);
}
