import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  createDeck,
  createDeckNote,
  decodeDeckTree,
  decodeNotes,
  deleteDeck,
  deleteNote,
  getDeckNotes,
  getDecks,
  patchDeck,
  patchNote,
} from './content';

const fetchMock = vi.fn<typeof fetch>();

function response(body?: unknown, status = 200) {
  return new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
  });
}

beforeEach(() => {
  fetchMock.mockReset();
  vi.stubGlobal('fetch', fetchMock);
});

afterEach(() => vi.unstubAllGlobals());

describe('content response decoders', () => {
  it('normalizes nested decks and inherits parent references', () => {
    expect(decodeDeckTree({ decks: [{
      id: 1,
      name: 'German',
      lang_target: 'de',
      lang_native: 'pl',
      counts: { notes: 2, new: 3, learning: 0, due: 1 },
      decks: [{ id: 2, name: 'Chapter 1' }],
    }] })).toMatchObject([{
      id: 1,
      name: 'German',
      parentId: null,
      langTarget: 'de',
      langNative: 'pl',
      counts: { notes: 2, new: 3, due: 1 },
      children: [{ id: 2, name: 'Chapter 1', parentId: 1 }],
    }]);
  });

  it('builds a tree from flat rows and decodes optional word fields', () => {
    expect(decodeDeckTree([{ id: 2, parent_id: 1, name: 'Child' }, { id: 1, parent_id: null, name: 'Root' }])).toMatchObject([
      { id: 1, name: 'Root', children: [{ id: 2, parentId: 1, name: 'Child' }] },
    ]);
    expect(decodeNotes({ notes: [{ id: 3, term: 'Hallo', translation: 'Cześć', tags: ['greeting'] }] })).toEqual([
      { id: 3, term: 'Hallo', translation: 'Cześć', tags: ['greeting'] },
    ]);
    expect(() => decodeDeckTree({ decks: [{ name: 'Missing ID' }] })).toThrow();
    expect(() => decodeNotes({ notes: [{ id: 3, term: 'Missing translation' }] })).toThrow();
  });
});

it('uses the planned deck and word CRUD endpoints with same-origin credentials', async () => {
  fetchMock
    .mockResolvedValueOnce(response([]))
    .mockResolvedValueOnce(response({ id: 1 }, 201))
    .mockResolvedValueOnce(response({ id: 1 }))
    .mockResolvedValueOnce(response(undefined, 204))
    .mockResolvedValueOnce(response([]))
    .mockResolvedValueOnce(response({ id: 2 }, 201))
    .mockResolvedValueOnce(response({ id: 2 }))
    .mockResolvedValueOnce(response(undefined, 204));

  await getDecks();
  await createDeck({ name: 'German', parent_id: null, lang_target: 'de', lang_native: 'pl', reverse_cards: true });
  await patchDeck(1, { name: 'German course' });
  await deleteDeck(1);
  await getDeckNotes(1);
  await createDeckNote(1, { term: 'Hallo', translation: 'Cześć' });
  await patchNote(2, { translation: 'Hello' });
  await deleteNote(2);

  expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
    '/api/v1/decks', '/api/v1/decks', '/api/v1/decks/1', '/api/v1/decks/1',
    '/api/v1/decks/1/notes', '/api/v1/decks/1/notes', '/api/v1/notes/2', '/api/v1/notes/2',
  ]);
  expect(fetchMock.mock.calls.every(([, init]) => init?.credentials === 'same-origin')).toBe(true);
  expect(fetchMock.mock.calls[1]?.[1]?.body).toBe(JSON.stringify({
    name: 'German', parent_id: null, lang_target: 'de', lang_native: 'pl', reverse_cards: true,
  }));
  expect(fetchMock.mock.calls[6]?.[1]?.body).toBe(JSON.stringify({ translation: 'Hello' }));
});
