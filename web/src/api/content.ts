import { ApiError } from './client';

export type DeckId = number | string;
export type TargetLanguage = 'de' | 'en';
export type NativeLanguage = 'pl' | 'ru';

export interface DeckNode {
  id: DeckId;
  name: string;
  parentId: DeckId | null;
  langTarget?: TargetLanguage;
  langNative?: NativeLanguage;
  reverseCards: boolean;
  wrongArticleRating: 1 | 2;
  counts?: { notes: number; new: number; learning: number; due: number };
  children: DeckNode[];
}

export interface Note {
  id: DeckId;
  term: string;
  translation: string;
  transcription?: string;
  example?: string;
  exampleTranslation?: string;
  notes?: string;
  tags?: string[];
  alternatives?: string[];
  fields?: Record<string, unknown>;
}

export interface CreateDeckRequest {
  name: string;
  parent_id: DeckId | null;
  lang_target?: TargetLanguage;
  lang_native?: NativeLanguage;
  reverse_cards?: boolean;
  wrong_article_rating?: 1 | 2;
}

export type PatchDeckRequest = Partial<CreateDeckRequest>;

export interface CreateNoteRequest {
  term: string;
  translation: string;
  transcription?: string;
  example?: string;
  example_translation?: string;
  notes?: string;
  tags?: string[];
  alternatives?: string[];
  fields?: Record<string, unknown>;
}

export interface PatchNoteRequest {
  term?: string;
  translation?: string;
  transcription?: string | null;
  example?: string | null;
  example_translation?: string | null;
  notes?: string | null;
  tags?: string[];
  alternatives?: string[];
  fields?: Record<string, unknown>;
  external_id?: string | null;
}

export interface ApplyEnrichmentRequest {
  transcription?: string;
  example?: string;
  example_translation?: string;
  alternatives?: string[];
  fields?: Record<string, unknown>;
}

export interface ApplyEnrichmentResponse {
  note: Note;
  applied_fields: string[];
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isDeckId(value: unknown): value is DeckId {
  return typeof value === 'string' || (typeof value === 'number' && Number.isFinite(value));
}

function invalidResponseError(): ApiError {
  return new ApiError(200, 'invalid_response');
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
  let body: unknown;
  if (text) {
    try {
      body = JSON.parse(text) as unknown;
    } catch {
      body = text;
    }
  }

  if (!response.ok) {
    if (isRecord(body) && isRecord(body.error)) {
      throw new ApiError(
        response.status,
        typeof body.error.code === 'string' ? body.error.code : 'request_failed',
        typeof body.error.message === 'string' ? body.error.message : response.statusText,
      );
    }
    throw new ApiError(response.status, 'request_failed', response.statusText || undefined);
  }
  if (response.status === 204 || !text) return undefined;
  if (typeof body === 'string') throw invalidResponseError();
  return body;
}

function getCollection(value: unknown, keys: string[]): unknown[] | undefined {
  if (Array.isArray(value)) return value;
  if (!isRecord(value)) return undefined;
  for (const key of keys) {
    if (Array.isArray(value[key])) return value[key] as unknown[];
  }
  for (const key of ['data', 'result']) {
    if (value[key] !== value) {
      const nested = getCollection(value[key], keys);
      if (nested) return nested;
    }
  }
  return undefined;
}

function optionalTargetLanguage(value: unknown): TargetLanguage | undefined {
  return value === 'de' || value === 'en' ? value : undefined;
}

function optionalNativeLanguage(value: unknown): NativeLanguage | undefined {
  return value === 'pl' || value === 'ru' ? value : undefined;
}

/** Accepts either a nested deck tree or a flat list carrying parent_id. */
export function decodeDeckTree(value: unknown): DeckNode[] {
  const collection = getCollection(value, ['decks', 'tree', 'items', 'results']);
  if (!collection) throw invalidResponseError();

  type FlatDeck = Omit<DeckNode, 'children'>;
  const flat: FlatDeck[] = [];

  function visit(items: unknown[], inheritedParent: DeckId | null) {
    for (const item of items) {
      if (!isRecord(item) || !isDeckId(item.id) || typeof item.name !== 'string') {
        throw invalidResponseError();
      }
      const suppliedParent = item.parent_id ?? item.parentId;
      const parentId = isDeckId(suppliedParent)
        ? suppliedParent
        : inheritedParent;
      const deck: FlatDeck = {
        id: item.id,
        name: item.name,
        parentId,
        langTarget: optionalTargetLanguage(item.lang_target ?? item.langTarget),
        langNative: optionalNativeLanguage(item.lang_native ?? item.langNative),
        reverseCards: typeof item.reverse_cards === 'boolean' ? item.reverse_cards : false,
        wrongArticleRating: item.wrong_article_rating === 1 ? 1 : 2,
        ...(isRecord(item.counts) ? {
          counts: {
            notes: typeof item.counts.notes === 'number' ? item.counts.notes : 0,
            new: typeof item.counts.new === 'number' ? item.counts.new : 0,
            learning: typeof item.counts.learning === 'number' ? item.counts.learning : 0,
            due: typeof item.counts.due === 'number' ? item.counts.due : 0,
          },
        } : {}),
      };
      flat.push(deck);
      const children = Array.isArray(item.children)
        ? item.children
        : Array.isArray(item.decks)
          ? item.decks
          : [];
      visit(children, deck.id);
    }
  }

  visit(collection, null);

  const byId = new Map<string, DeckNode>();
  for (const deck of flat) {
    byId.set(String(deck.id), { ...deck, children: [] });
  }
  const roots: DeckNode[] = [];
  for (const deck of byId.values()) {
    const parent = deck.parentId === null ? undefined : byId.get(String(deck.parentId));
    if (parent && parent.id !== deck.id) parent.children.push(deck);
    else roots.push(deck);
  }
  return roots;
}

export function decodeNotes(value: unknown): Note[] {
  const collection = getCollection(value, ['notes', 'items', 'results']);
  if (!collection) throw invalidResponseError();
  return collection.map(decodeNote);
}

function decodeNote(item: unknown): Note {
  if (!isRecord(item) || !isDeckId(item.id)
    || typeof item.term !== 'string' || typeof item.translation !== 'string') {
    throw invalidResponseError();
  }
  return {
    id: item.id,
    term: item.term,
    translation: item.translation,
    ...(typeof item.transcription === 'string' ? { transcription: item.transcription } : {}),
    ...(typeof item.example === 'string' ? { example: item.example } : {}),
    ...(typeof item.example_translation === 'string' ? { exampleTranslation: item.example_translation } : {}),
    ...(typeof item.notes === 'string' ? { notes: item.notes } : {}),
    ...(Array.isArray(item.tags) && item.tags.every((tag) => typeof tag === 'string')
      ? { tags: item.tags as string[] }
      : {}),
    ...(Array.isArray(item.alternatives) && item.alternatives.every((alternative) => typeof alternative === 'string')
      ? { alternatives: item.alternatives as string[] }
      : {}),
    ...(isRecord(item.fields) ? { fields: item.fields } : {}),
  };
}

export async function getDecks(): Promise<DeckNode[]> {
  return decodeDeckTree(await request('/decks'));
}

export function createDeck(input: CreateDeckRequest): Promise<unknown> {
  return request('/decks', { method: 'POST', body: input });
}

export function patchDeck(id: DeckId, input: PatchDeckRequest): Promise<unknown> {
  return request(`/decks/${encodeURIComponent(String(id))}`, { method: 'PATCH', body: input });
}

export async function deleteDeck(id: DeckId): Promise<void> {
  await request(`/decks/${encodeURIComponent(String(id))}`, { method: 'DELETE' });
}

export async function getDeckNotes(id: DeckId): Promise<Note[]> {
  return decodeNotes(await request(`/decks/${encodeURIComponent(String(id))}/notes`));
}

export function createDeckNote(id: DeckId, input: CreateNoteRequest): Promise<unknown> {
  return request(`/decks/${encodeURIComponent(String(id))}/notes`, { method: 'POST', body: input });
}

export function patchNote(id: DeckId, input: PatchNoteRequest): Promise<unknown> {
  return request(`/notes/${encodeURIComponent(String(id))}`, { method: 'PATCH', body: input });
}

export async function applyNoteEnrichment(id: DeckId, input: ApplyEnrichmentRequest): Promise<ApplyEnrichmentResponse> {
  const value = await request(`/notes/${encodeURIComponent(String(id))}/apply-enrichment`, { method: 'POST', body: input });
  if (!isRecord(value) || !Array.isArray(value.applied_fields)
    || !value.applied_fields.every((field) => typeof field === 'string')) {
    throw invalidResponseError();
  }
  return { note: decodeNote(value.note), applied_fields: value.applied_fields as string[] };
}

export async function startDeckEnrichment(id: DeckId): Promise<{ job_id: string }> {
  const value = await request(`/decks/${encodeURIComponent(String(id))}/enrich`, { method: 'POST' });
  if (!isRecord(value) || typeof value.job_id !== 'string' || value.job_id.length === 0) {
    throw invalidResponseError();
  }
  return { job_id: value.job_id };
}

export async function deleteNote(id: DeckId): Promise<void> {
  await request(`/notes/${encodeURIComponent(String(id))}`, { method: 'DELETE' });
}
