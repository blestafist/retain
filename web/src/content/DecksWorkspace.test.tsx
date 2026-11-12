// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { DecksWorkspace } from './DecksWorkspace';
import { t } from '../i18n/en';

const fetchMock = vi.fn<typeof fetch>();

function jsonResponse(body?: unknown, status = 200) {
  return new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
  });
}

function renderWorkspace() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(<QueryClientProvider client={queryClient}><DecksWorkspace /></QueryClientProvider>);
}

beforeEach(() => {
  fetchMock.mockReset();
  vi.stubGlobal('fetch', fetchMock);
  vi.stubGlobal('confirm', vi.fn(() => true));
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('DecksWorkspace', () => {
  it('uses explicit AI suggestions when adding words and manages deck content', async () => {
    const user = userEvent.setup();
    let deckName = 'German course';
    let notes: Array<Record<string, unknown>> = [];
    fetchMock.mockImplementation(async (input, init) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (url === '/api/v1/llm/status' && method === 'GET') return jsonResponse({ enabled: true });
      if (url === '/api/v1/llm/enrich' && method === 'POST') return jsonResponse({
        translation: 'Cześć', alternatives: ['Witaj'], transcription: 'ˈhalo', example: 'Hallo!',
        example_translation: 'Cześć!', fields: { pos: 'interjection', article: '', plural: '', verb_forms: [], cefr: '' },
      });
      if (url === '/api/v1/decks' && method === 'GET') {
        return jsonResponse([{
          id: 42, name: deckName, parent_id: null, lang_target: 'de', lang_native: 'pl', reverse_cards: false,
          counts: { notes: notes.length, new: notes.length, learning: 0, due: 0 }, children: [],
        }]);
      }
      if (url === '/api/v1/decks/42/notes' && method === 'GET') return jsonResponse(notes);
      if (url === '/api/v1/import/batches' && method === 'GET') return jsonResponse([]);
      if (url === '/api/v1/decks/42/notes' && method === 'POST') {
        const inputBody = JSON.parse(String(init?.body)) as Record<string, unknown>;
        notes = [{ id: 501, ...inputBody }];
        return jsonResponse(notes[0], 201);
      }
      if (url === '/api/v1/notes/501' && method === 'PATCH') {
        const inputBody = JSON.parse(String(init?.body)) as Record<string, unknown>;
        notes = [{ ...notes[0], ...inputBody }];
        return jsonResponse(notes[0]);
      }
      if (url === '/api/v1/notes/501' && method === 'DELETE') {
        notes = [];
        return jsonResponse(undefined, 204);
      }
      if (url === '/api/v1/decks/42' && method === 'PATCH') {
        deckName = (JSON.parse(String(init?.body)) as { name: string }).name;
        return jsonResponse({ id: 42, name: deckName });
      }
      throw new Error(`Unexpected request: ${method} ${url}`);
    });
    renderWorkspace();

    await screen.findByRole('heading', { name: 'German course' });
    const renameForm = screen.getByRole('form', { name: t('renameDeck') });
    await user.clear(within(renameForm).getByLabelText(t('deckName')));
    await user.type(within(renameForm).getByLabelText(t('deckName')), 'German lessons');
    await user.click(within(renameForm).getByRole('button', { name: t('saveDeck') }));
    await screen.findByRole('heading', { name: 'German lessons' });

    await user.click(screen.getByText(t('addNote'), { selector: 'summary' }));
    const addForm = screen.getByRole('form', { name: t('addNote') });
    await user.type(within(addForm).getByLabelText(t('term')), 'Hallo');
    await user.click(await within(addForm).findByRole('button', { name: t('fillWord') }));
    await within(addForm).findByText(t('suggestionsNotSaved'));
    await user.click(within(addForm).getByRole('button', { name: t('useSuggestion').replace('{field}', t('translation')) }));
    await user.click(within(addForm).getByRole('button', { name: t('useSuggestion').replace('{field}', t('partOfSpeech')) }));
    expect(within(addForm).getByLabelText(t('translation'))).toHaveProperty('value', 'Cześć');
    await user.type(within(addForm).getByLabelText(t('tags'), { exact: false }), 'greeting, common');
    await user.click(within(addForm).getByRole('button', { name: t('addNote') }));
    expect(await screen.findByText('Hallo')).toBeTruthy();
    await waitFor(() => expect(fetchMock.mock.calls.some(([url, init]) => String(url).endsWith('/notes') && init?.method === 'POST')).toBe(true));
    const createCall = fetchMock.mock.calls.find(([url, init]) => String(url) === '/api/v1/decks/42/notes' && init?.method === 'POST');
    expect(JSON.parse(String(createCall?.[1]?.body))).toEqual({
      term: 'Hallo', translation: 'Cześć', tags: ['greeting', 'common'], fields: { pos: 'interjection' },
    });

    const noteRow = screen.getByText('Hallo').closest('li');
    expect(noteRow).not.toBeNull();
    await user.click(within(noteRow as HTMLElement).getByRole('button', { name: t('edit') }));
    const editForm = screen.getByRole('form', { name: t('editNote') });
    await user.clear(within(editForm).getByLabelText(t('translation')));
    await user.type(within(editForm).getByLabelText(t('translation')), 'Hello');
    await user.click(within(editForm).getByRole('button', { name: t('saveNote') }));
    expect(await screen.findByText('Hello')).toBeTruthy();
    const updateCall = fetchMock.mock.calls.find(([url, init]) => String(url) === '/api/v1/notes/501' && init?.method === 'PATCH');
    expect(JSON.parse(String(updateCall?.[1]?.body))).toMatchObject({ translation: 'Hello', tags: ['greeting', 'common'] });

    await user.click(screen.getByRole('button', { name: `${t('deleteNote')} Hallo` }));
    await waitFor(() => expect(screen.queryByText('Hallo')).toBeNull());
    expect(fetchMock.mock.calls.some(([url, init]) => String(url) === '/api/v1/notes/501' && init?.method === 'DELETE')).toBe(true);

    const requestedURLs = fetchMock.mock.calls.map(([url]) => String(url));
    expect(requestedURLs).toContain('/api/v1/llm/status');
    expect(requestedURLs).toContain('/api/v1/llm/enrich');
    expect(requestedURLs.some((url) => /\/(study|stats|settings|jobs|admin|sync)(\/|\?|$)/.test(url))).toBe(false);
  });
});
