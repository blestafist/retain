import { ApiError } from './client';

export interface LlmStatus {
  enabled: boolean;
}

export interface AskAboutNoteRequest {
  note_id: number;
  question: string;
}

export interface EnrichWordRequest {
  deck_id: number;
  word: string;
}

export interface CheckAnswerSynonymResult {
  is_synonym: boolean;
  explanation: string;
}

export interface MnemonicResult {
  mnemonics: string[];
}

export interface EnrichedWord {
  translation: string;
  alternatives: string[];
  transcription: string;
  example: string;
  example_translation: string;
  fields: {
    pos: string;
    article: string;
    plural: string;
    verb_forms: string[];
    cefr: string;
  };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isStringArray(value: unknown): value is string[] {
  return Array.isArray(value) && value.every((item) => typeof item === 'string');
}

function decodeStatus(value: unknown): LlmStatus {
  if (!isRecord(value) || typeof value.enabled !== 'boolean') {
    throw new ApiError(200, 'invalid_response');
  }
  return { enabled: value.enabled };
}

function decodeSynonymResult(value: unknown): CheckAnswerSynonymResult {
  if (!isRecord(value) || typeof value.is_synonym !== 'boolean' || typeof value.explanation !== 'string') {
    throw new ApiError(200, 'invalid_response');
  }
  return { is_synonym: value.is_synonym, explanation: value.explanation };
}

function decodeMnemonicResult(value: unknown): MnemonicResult {
  if (!isRecord(value) || !isStringArray(value.mnemonics) || value.mnemonics.length < 2 || value.mnemonics.length > 3) {
    throw new ApiError(200, 'invalid_response');
  }
  return { mnemonics: value.mnemonics };
}

function decodeEnrichedWord(value: unknown): EnrichedWord {
  if (!isRecord(value) || !isRecord(value.fields)) {
    throw new ApiError(200, 'invalid_response');
  }
  const { fields } = value;
  if (typeof value.translation !== 'string'
    || !isStringArray(value.alternatives)
    || typeof value.transcription !== 'string'
    || typeof value.example !== 'string'
    || typeof value.example_translation !== 'string'
    || typeof fields.pos !== 'string'
    || typeof fields.article !== 'string'
    || typeof fields.plural !== 'string'
    || !isStringArray(fields.verb_forms)
    || typeof fields.cefr !== 'string') {
    throw new ApiError(200, 'invalid_response');
  }
  return {
    translation: value.translation,
    alternatives: value.alternatives,
    transcription: value.transcription,
    example: value.example,
    example_translation: value.example_translation,
    fields: {
      pos: fields.pos,
      article: fields.article,
      plural: fields.plural,
      verb_forms: fields.verb_forms,
      cefr: fields.cefr,
    },
  };
}

async function request<T>(
  path: string,
  decode: (value: unknown) => T,
  options: { method?: string; body?: unknown } = {},
): Promise<T> {
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
  if (typeof body === 'string' || body === undefined) throw new ApiError(response.status, 'invalid_response');
  return decode(body);
}

export function getLlmStatus(): Promise<LlmStatus> {
  return request('/llm/status', decodeStatus);
}

export function checkAnswerSynonym(cardId: number, answer: string): Promise<CheckAnswerSynonymResult> {
  return request('/llm/check-answer', decodeSynonymResult, {
    method: 'POST',
    body: { card_id: cardId, answer },
  });
}

export function suggestMnemonics(noteId: number): Promise<MnemonicResult> {
  return request('/llm/mnemonic', decodeMnemonicResult, { method: 'POST', body: { note_id: noteId } });
}

function decodeNoteAlternatives(value: unknown, noteId: number): string[] {
  let collection: unknown = value;
  if (isRecord(value)) {
    collection = Array.isArray(value.notes) ? value.notes
      : Array.isArray(value.items) ? value.items
        : Array.isArray(value.results) ? value.results
          : undefined;
  }
  if (!Array.isArray(collection)) throw new ApiError(200, 'invalid_response');
  const note = collection.find((item) => isRecord(item) && item.id === noteId);
  if (!isRecord(note)) throw new ApiError(404, 'not_found');
  if (note.alternatives !== undefined && (!Array.isArray(note.alternatives)
    || !note.alternatives.every((alternative) => typeof alternative === 'string'))) {
    throw new ApiError(200, 'invalid_response');
  }
  return (note.alternatives as string[] | undefined) ?? [];
}

/** Adds one approved answer without replacing the note's existing alternatives. */
export async function addNoteAlternative(deckId: number, noteId: number, answer: string): Promise<void> {
  const alternatives = await request(
    `/decks/${encodeURIComponent(String(deckId))}/notes`,
    (value) => decodeNoteAlternatives(value, noteId),
  );
  const acceptedAnswer = answer.trim();
  const merged = alternatives.includes(acceptedAnswer) ? alternatives : [...alternatives, acceptedAnswer];
  await request(`/notes/${encodeURIComponent(String(noteId))}`, (value) => value, {
    method: 'PATCH',
    body: { alternatives: merged },
  });
}

function apiErrorFromValue(value: unknown, status: number): ApiError {
  if (isRecord(value) && isRecord(value.error)) {
    return new ApiError(
      status,
      typeof value.error.code === 'string' && value.error.code ? value.error.code : 'request_failed',
      typeof value.error.message === 'string' && value.error.message ? value.error.message : undefined,
    );
  }
  return new ApiError(status, 'request_failed');
}

function parseJson(value: string): unknown {
  try {
    return JSON.parse(value) as unknown;
  } catch {
    throw new ApiError(502, 'invalid_response');
  }
}

function abortException(): DOMException {
  return new DOMException('The operation was aborted.', 'AbortError');
}

function readWithAbort(
  reader: ReadableStreamDefaultReader<Uint8Array>,
  signal?: AbortSignal,
): Promise<ReadableStreamReadResult<Uint8Array>> {
  if (!signal) return reader.read();
  if (signal.aborted) return Promise.reject(abortException());

  return new Promise((resolve, reject) => {
    const onAbort = () => {
      void reader.cancel().catch(() => undefined);
      reject(abortException());
    };
    signal.addEventListener('abort', onAbort, { once: true });
    reader.read().then(
      (result) => {
        signal.removeEventListener('abort', onAbort);
        resolve(result);
      },
      (error: unknown) => {
        signal.removeEventListener('abort', onAbort);
        reject(error);
      },
    );
  });
}

function decodeAskEvent(event: string, data: string, status: number, onToken: (text: string) => void): boolean {
  if (event === 'token') {
    const value: unknown = parseJson(data);
    if (!isRecord(value) || typeof value.text !== 'string') {
      throw new ApiError(status, 'invalid_response');
    }
    onToken(value.text);
    return false;
  }
  if (event === 'done') {
    const value: unknown = parseJson(data);
    if (!isRecord(value)) throw new ApiError(status, 'invalid_response');
    return true;
  }
  if (event === 'error') {
    throw apiErrorFromValue(parseJson(data), status);
  }
  return false;
}

async function consumeAskStream(
  body: ReadableStream<Uint8Array> | null,
  status: number,
  onToken: (text: string) => void,
  signal?: AbortSignal,
): Promise<void> {
  if (!body) throw new ApiError(status, 'invalid_response');
  const reader = body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';
  let event = 'message';
  let data: string[] = [];
  let completed = false;

  const processLine = (line: string): boolean => {
    if (line === '') {
      const terminal = data.length === 0
        ? false
        : decodeAskEvent(event, data.join('\n'), status, onToken);
      event = 'message';
      data = [];
      if (terminal) completed = true;
      return terminal;
    }
    if (line.startsWith(':')) return false;
    const separator = line.indexOf(':');
    const field = separator === -1 ? line : line.slice(0, separator);
    let value = separator === -1 ? '' : line.slice(separator + 1);
    if (value.startsWith(' ')) value = value.slice(1);
    if (field === 'event') event = value;
    else if (field === 'data') data.push(value);
    return false;
  };

  const processLines = (final: boolean): boolean => {
    while (true) {
      const lf = buffer.indexOf('\n');
      const cr = buffer.indexOf('\r');
      const end = lf === -1 ? cr : cr === -1 ? lf : Math.min(lf, cr);
      if (end === -1 || (buffer[end] === '\r' && end === buffer.length - 1 && !final)) return false;

      const line = buffer.slice(0, end);
      const width = buffer[end] === '\r' && buffer[end + 1] === '\n' ? 2 : 1;
      buffer = buffer.slice(end + width);
      if (processLine(line)) return true;
    }
  };

  try {
    while (!completed) {
      const chunk = await readWithAbort(reader, signal);
      if (chunk.done) {
        buffer += decoder.decode();
        if (processLines(true)) break;
        if (buffer) {
          processLine(buffer);
          buffer = '';
        }
        if (!completed) processLine('');
        break;
      }
      buffer += decoder.decode(chunk.value, { stream: true });
      processLines(false);
    }
    if (!completed) throw new ApiError(status, 'incomplete_stream');
  } finally {
    await reader.cancel().catch(() => undefined);
    reader.releaseLock();
  }
}

/** Streams an answer for a note. Token callbacks can receive any UTF-8 text fragment. */
export async function askAboutNote(
  input: AskAboutNoteRequest,
  onToken: (text: string) => void,
  signal?: AbortSignal,
): Promise<void> {
  const response = await fetch('/api/v1/llm/ask', {
    method: 'POST',
    credentials: 'same-origin',
    headers: new Headers({ Accept: 'text/event-stream', 'Content-Type': 'application/json' }),
    body: JSON.stringify(input),
    signal,
  });

  if (!response.ok) {
    let body: unknown;
    try {
      body = JSON.parse(await response.text()) as unknown;
    } catch {
      body = undefined;
    }
    throw apiErrorFromValue(body, response.status);
  }
  await consumeAskStream(response.body, response.status, onToken, signal);
}

export function enrichWord(input: EnrichWordRequest): Promise<EnrichedWord> {
  return request('/llm/enrich', decodeEnrichedWord, { method: 'POST', body: input });
}
