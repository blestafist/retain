import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { addNoteAlternative, askAboutNote, checkAnswerSynonym, enrichWord, getLlmStatus } from './llm';

const fetchMock = vi.fn<typeof fetch>();

function response(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function streamResponse(text: string, status = 200) {
  const bytes = new TextEncoder().encode(text);
  return new Response(new ReadableStream<Uint8Array>({
    start(controller) {
      // Byte-wise chunks also split UTF-8 characters, CRLF pairs, and SSE fields.
      for (const byte of bytes) controller.enqueue(new Uint8Array([byte]));
      controller.close();
    },
  }), {
    status,
    headers: { 'Content-Type': 'text/event-stream' },
  });
}

beforeEach(() => {
  fetchMock.mockReset();
  vi.stubGlobal('fetch', fetchMock);
});

afterEach(() => vi.unstubAllGlobals());

describe('LLM API', () => {
  it('reads whether LLM features are enabled', async () => {
    fetchMock.mockResolvedValue(response({ enabled: false }));

    await expect(getLlmStatus()).resolves.toEqual({ enabled: false });
    const [url, options] = fetchMock.mock.calls[0] ?? [];
    expect(url).toBe('/api/v1/llm/status');
    expect(options?.credentials).toBe('same-origin');
    expect(new Headers(options?.headers).get('Accept')).toBe('application/json');
  });

  it('checks a typed answer using only the approved card and answer input', async () => {
    fetchMock.mockResolvedValue(response({ is_synonym: true, explanation: 'A valid regional synonym.' }));

    await expect(checkAnswerSynonym(42, 'kitty')).resolves.toEqual({
      is_synonym: true,
      explanation: 'A valid regional synonym.',
    });
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/llm/check-answer', expect.objectContaining({
      method: 'POST',
      credentials: 'same-origin',
      body: JSON.stringify({ card_id: 42, answer: 'kitty' }),
    }));
  });

  it('rejects malformed synonym verdicts and safe API errors', async () => {
    fetchMock.mockResolvedValueOnce(response({ is_synonym: 'yes', explanation: '' }));
    await expect(checkAnswerSynonym(42, 'kitty')).rejects.toMatchObject({ code: 'invalid_response' });

    fetchMock.mockResolvedValueOnce(response({ error: { code: 'feature_disabled', message: 'Synonym check is disabled.' } }, 503));
    await expect(checkAnswerSynonym(42, 'kitty')).rejects.toMatchObject({
      code: 'feature_disabled',
      serverMessage: 'Synonym check is disabled.',
    });
  });

  it('merges an explicitly accepted answer into existing alternatives before patching the note', async () => {
    fetchMock
      .mockResolvedValueOnce(response([{ id: 12, alternatives: ['kitty', 'feline'] }]))
      .mockResolvedValueOnce(response({ id: 12 }));

    await addNoteAlternative(7, 12, 'catling');

    expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/v1/decks/7/notes');
    expect(fetchMock.mock.calls[1]?.[0]).toBe('/api/v1/notes/12');
    expect(fetchMock.mock.calls[1]?.[1]).toMatchObject({
      method: 'PATCH',
      body: JSON.stringify({ alternatives: ['kitty', 'feline', 'catling'] }),
    });
  });

  it('does not duplicate an already accepted answer', async () => {
    fetchMock
      .mockResolvedValueOnce(response([{ id: 12, alternatives: ['kitty'] }]))
      .mockResolvedValueOnce(response({ id: 12 }));

    await addNoteAlternative(7, 12, 'kitty');
    expect(JSON.parse(String(fetchMock.mock.calls[1]?.[1]?.body))).toEqual({ alternatives: ['kitty'] });
  });

  it('sends the selected deck and word to enrich', async () => {
    const enriched = {
      translation: 'cat',
      alternatives: ['kitty'],
      transcription: '/kæt/',
      example: 'Die Katze schläft.',
      example_translation: 'The cat is sleeping.',
      fields: { pos: 'noun', article: 'die', plural: 'Katzen', verb_forms: [], cefr: 'A1' },
    };
    fetchMock.mockResolvedValue(response(enriched));

    await expect(enrichWord({ deck_id: 52, word: 'Katze' })).resolves.toEqual(enriched);
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/llm/enrich', expect.objectContaining({
      method: 'POST',
      credentials: 'same-origin',
      body: JSON.stringify({ deck_id: 52, word: 'Katze' }),
    }));
  });

  it('rejects malformed suggestions', async () => {
    fetchMock.mockResolvedValue(response({ enabled: true }));
    await expect(enrichWord({ deck_id: 1, word: 'Wort' })).rejects.toMatchObject({ code: 'invalid_response' });
  });

  it('streams tokens across arbitrary chunk boundaries and ignores heartbeats', async () => {
    fetchMock.mockResolvedValue(streamResponse(
      ': keep-alive\r\n\r\nevent: token\r\ndata: {"text":"Grü"}\r\n\r\nevent: token\ndata: {"text":"ße 🌱"}\n\n'
        + 'event: done\ndata: {}\n\n: keep-alive\n\n',
    ));
    const tokens: string[] = [];

    await askAboutNote({ note_id: 12, question: 'Why this word?' }, (text) => tokens.push(text));

    expect(tokens).toEqual(['Grü', 'ße 🌱']);
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/llm/ask', expect.objectContaining({
      method: 'POST',
      credentials: 'same-origin',
      body: JSON.stringify({ note_id: 12, question: 'Why this word?' }),
    }));
    expect(new Headers(fetchMock.mock.calls[0]?.[1]?.headers).get('Accept')).toBe('text/event-stream');
  });

  it('rejects a streamed safe API error', async () => {
    fetchMock.mockResolvedValue(streamResponse('event: error\ndata: {"error":{"code":"llm_unavailable","message":"Please try again later."}}\n\n'));
    const onToken = vi.fn();

    await expect(askAboutNote({ note_id: 12, question: 'Question' }, onToken))
      .rejects.toMatchObject({ code: 'llm_unavailable', serverMessage: 'Please try again later.' });
    expect(onToken).not.toHaveBeenCalled();
  });

  it('rejects if the stream ends without a done event', async () => {
    fetchMock.mockResolvedValue(streamResponse('event: token\ndata: {"text":"partial"}\n\n'));

    await expect(askAboutNote({ note_id: 12, question: 'Question' }, vi.fn()))
      .rejects.toMatchObject({ code: 'incomplete_stream' });
  });

  it('cancels an active stream when its signal is aborted', async () => {
    const cancel = vi.fn();
    fetchMock.mockResolvedValue(new Response(new ReadableStream<Uint8Array>({ cancel }), {
      headers: { 'Content-Type': 'text/event-stream' },
    }));
    const controller = new AbortController();
    const result = askAboutNote({ note_id: 12, question: 'Question' }, vi.fn(), controller.signal);
    controller.abort();

    await expect(result).rejects.toMatchObject({ name: 'AbortError' });
    expect(cancel).toHaveBeenCalled();
    expect(fetchMock.mock.calls[0]?.[1]?.signal).toBe(controller.signal);
  });
});
