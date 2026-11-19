// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, cleanup, render, screen, waitFor, within } from '@testing-library/react';
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

function renderWorkspace(ownerId = 0) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const result = render(<QueryClientProvider client={queryClient}><DecksWorkspace ownerId={ownerId} /></QueryClientProvider>);
  return { ...result, queryClient };
}

beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  fetchMock.mockReset();
  vi.stubGlobal('fetch', fetchMock);
  vi.stubGlobal('confirm', vi.fn(() => true));
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('DecksWorkspace', () => {
  it('uses the selected deck as the primary heading and defers import and enrichment until requested', async () => {
    const user = userEvent.setup();
    localStorage.setItem('retain:deck-enrichment-jobs:v1:17', JSON.stringify([
      { jobId: 'saved-job', deckId: '43', deckName: 'Chapter 1' },
    ]));
    fetchMock.mockImplementation(async (input) => {
      const url = String(input);
      if (url === '/api/v1/decks') return jsonResponse([{
        id: 42, name: 'German course', parent_id: null, lang_target: 'de', lang_native: 'pl',
        counts: { notes: 2, new: 1, learning: 0, due: 1 }, children: [{
          id: 43, name: 'Chapter 1', parent_id: 42, children: [],
        }],
      }, { id: 44, name: 'English course', parent_id: null, lang_target: 'en', lang_native: 'ru', children: [] }]);
      if (url === '/api/v1/decks/42/notes') return jsonResponse([{ id: 501, term: 'Hallo', translation: 'Cześć' }]);
      if (url === '/api/v1/decks/43/notes') return jsonResponse([{ id: 502, term: 'Welt', translation: 'Świat' }]);
      if (url === '/api/v1/llm/status') return jsonResponse({ enabled: true });
      if (url === '/api/v1/jobs/saved-job') return jsonResponse({
        id: 'saved-job', kind: 'deck_enrich', status: 'done', progress: 1, total: 1, error: null,
        result: { items: [], partial: false, omitted_count: 0 },
      });
      if (url === '/api/v1/import/batches') return jsonResponse([]);
      throw new Error(`Unexpected request: ${url}`);
    });
    renderWorkspace(17);

    expect(await screen.findByRole('heading', { name: 'German course', level: 1 })).toBeTruthy();
    await screen.findByText('Hallo');
    expect(screen.getAllByRole('heading', { level: 1 })).toHaveLength(1);
    const deckList = screen.getByRole('list', { name: t('deckList') });
    expect(within(deckList).getAllByRole('button')).toHaveLength(3);
    expect(within(deckList).getByRole('button', { name: /German course/ }).getAttribute('aria-current')).toBe('true');
    expect(screen.queryByRole('dialog', { hidden: true })).toBeNull();
    expect(screen.queryByRole('form', { name: t('newDeck'), hidden: true })).toBeNull();
    expect(screen.queryByRole('form', { name: t('renameDeck'), hidden: true })).toBeNull();
    expect(screen.queryByRole('form', { name: t('addNote'), hidden: true })).toBeNull();
    expect(screen.queryByLabelText(t('chooseImportFile'))).toBeNull();
    expect(screen.queryByText(t('importHistory'))).toBeNull();
    expect(screen.queryByText(t('deckEnrichmentHint'))).toBeNull();
    expect(fetchMock.mock.calls.map(([url]) => String(url)).sort()).toEqual([
      '/api/v1/decks', '/api/v1/decks/42/notes', '/api/v1/llm/status',
    ]);

    await user.click(within(deckList).getByRole('button', { name: 'Chapter 1' }));
    expect(screen.getByRole('heading', { name: 'Chapter 1', level: 1 })).toBeTruthy();
    await screen.findByText('Welt');
    expect(screen.queryByText('Hallo')).toBeNull();
    expect(screen.getByText(`${t('german')} → ${t('polish')}`)).toBeTruthy();
    expect(within(deckList).getByRole('button', { name: 'Chapter 1' }).getAttribute('aria-current')).toBe('true');
    expect(within(deckList).getByRole('button', { name: /German course/ }).getAttribute('aria-current')).toBeNull();
    expect(screen.getByRole('link', { name: t('study') }).getAttribute('href')).toBe('/study?deck=43');
    expect(fetchMock.mock.calls.some(([url]) => String(url).includes('/jobs/'))).toBe(false);

    await user.click(screen.getByRole('button', { name: t('deckActions') }));
    await user.click(screen.getByRole('menuitem', { name: t('deckEnrichment') }));
    const enrichmentDialog = await screen.findByRole('dialog', { name: t('deckEnrichment') });
    expect(await within(enrichmentDialog).findByText(t('noEnrichmentProposals'))).toBeTruthy();
    expect(fetchMock.mock.calls.some(([url]) => String(url) === '/api/v1/jobs/saved-job')).toBe(true);
    expect(fetchMock.mock.calls.some(([url]) => String(url) === '/api/v1/import/batches')).toBe(false);
    expect(fetchMock.mock.calls.every(([, init]) => !init?.method || init.method === 'GET')).toBe(true);
    await user.click(within(enrichmentDialog).getByRole('button', { name: t('close') }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());

    await user.click(screen.getByRole('button', { name: t('importExport') }));
    const transferDialog = await screen.findByRole('dialog', { name: t('importExport') });
    expect(within(transferDialog).getByLabelText(t('chooseImportFile'))).toHaveProperty('type', 'file');
    expect(await within(transferDialog).findByText(t('noImportHistory'))).toBeTruthy();
    expect(within(transferDialog).getByRole('button', { name: t('exportRetain') })).toHaveProperty('disabled', false);
    expect(within(transferDialog).getByRole('button', { name: t('exportCsv') })).toHaveProperty('disabled', false);
    expect(fetchMock.mock.calls.filter(([url]) => String(url) === '/api/v1/import/batches')).toHaveLength(1);
  });

  it('opens the library first on mobile, loads words on demand, and returns focus on Back', async () => {
    vi.stubGlobal('matchMedia', () => ({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() }));
    vi.stubGlobal('scrollTo', vi.fn());
    fetchMock.mockImplementation(async (input) => {
      const url = String(input);
      if (url === '/api/v1/decks') return jsonResponse([{ id: 42, name: 'German course', parent_id: null, children: [] }]);
      if (url === '/api/v1/decks/42/notes') return jsonResponse([{ id: 5, term: 'Hallo', translation: 'Cześć' }]);
      if (url === '/api/v1/llm/status') return jsonResponse({ enabled: false });
      throw new Error(`Unexpected request: ${url}`);
    });
    const user = userEvent.setup();
    renderWorkspace();

    expect(await screen.findByRole('heading', { name: t('deckLibrary'), level: 1 })).toBeTruthy();
    expect(fetchMock.mock.calls.some(([url]) => String(url).endsWith('/notes'))).toBe(false);
    const deck = screen.getByRole('button', { name: 'German course' });
    await user.click(deck);

    const heading = await screen.findByRole('heading', { name: 'German course', level: 1 });
    expect(document.activeElement).toBe(heading);
    expect(screen.queryByRole('list', { name: t('deckList') })).toBeNull();
    expect(await screen.findByText('Hallo')).toBeTruthy();

    await user.click(screen.getByRole('button', { name: t('backToDecks') }));
    expect(screen.getByRole('heading', { name: t('deckLibrary'), level: 1 })).toBeTruthy();
    expect(document.activeElement).toBe(deck);
  });

  it('keeps import available without decks and confirms before discarding an upload or preview', async () => {
    const user = userEvent.setup();
    const confirm = vi.mocked(window.confirm).mockReturnValue(false);
    fetchMock.mockImplementation(async (input) => {
      const url = String(input);
      if (url === '/api/v1/decks' || url === '/api/v1/import/batches') return jsonResponse([]);
      if (url === '/api/v1/llm/status') return jsonResponse({ enabled: false });
      if (url === '/api/v1/import/preview') return jsonResponse({
        batch_id: 'first-import', adapter: 'csv', status: 'previewed', source: 'csv',
        counts: { new: 1, changed: 0, unchanged: 0 }, deck_counts: { new: 1, changed: 0, unchanged: 0 },
        warnings: [], tree: [{ name: 'Imported words', status: 'new', counts: { new: 1, changed: 0, unchanged: 0 }, warnings: [], decks: [] }],
      });
      throw new Error(`Unexpected request: ${url}`);
    });
    renderWorkspace();

    await screen.findByText(t('noDecks'));
    expect(screen.getByRole('heading', { name: t('deckLibrary'), level: 1 })).toBeTruthy();
    expect(screen.queryByLabelText(t('chooseImportFile'))).toBeNull();
    expect(fetchMock.mock.calls.some(([url]) => String(url) === '/api/v1/import/batches')).toBe(false);
    const importButton = screen.getByRole('button', { name: t('importExport') });
    await user.click(importButton);

    const dialog = screen.getByRole('dialog', { name: t('importExport') });
    expect(await within(dialog).findByText(t('noImportHistory'))).toBeTruthy();
    expect(within(dialog).getByRole('button', { name: t('exportRetain') })).toHaveProperty('disabled', true);
    expect(within(dialog).getByRole('button', { name: t('exportCsv') })).toHaveProperty('disabled', true);
    const file = new File(['term;translation\nHallo;Cześć'], 'first-words.csv', { type: 'text/csv' });
    const fileInput = within(dialog).getByLabelText(t('chooseImportFile')) as HTMLInputElement;
    await user.upload(fileInput, file);
    await user.keyboard('{Escape}');
    expect(confirm).toHaveBeenCalledExactlyOnceWith(t('discardTaskChanges'));
    expect(screen.getByRole('dialog', { name: t('importExport') })).toBe(dialog);
    expect(fileInput.files?.[0]).toBe(file);
    await user.click(within(dialog).getByRole('button', { name: t('previewImport') }));
    expect(await within(dialog).findByRole('heading', { name: t('importPreview') })).toBeTruthy();
    expect(within(dialog).getByRole('list', { name: t('importTree') }).textContent).toContain('Imported words');
    expect(within(dialog).getByLabelText(t('optionalParentDeck'))).toHaveProperty('value', '');
    expect(within(dialog).getByRole('button', { name: t('reviewBeforeApply') })).toBeTruthy();
    expect(fetchMock.mock.calls.some(([url]) => String(url).endsWith('/apply'))).toBe(false);

    await user.selectOptions(within(dialog).getByLabelText(t('missingImportItems')), 'suspend');
    await user.click(within(dialog).getByRole('button', { name: t('reviewBeforeApply') }));
    await user.keyboard('{Escape}');
    expect(confirm).toHaveBeenCalledTimes(2);
    expect(confirm).toHaveBeenLastCalledWith(t('discardTaskChanges'));
    expect(screen.getByRole('dialog', { name: t('importExport') })).toBe(dialog);
    expect(within(dialog).getByRole('heading', { name: t('importPreview') })).toBeTruthy();
    expect(within(dialog).getByRole('list', { name: t('importTree') }).textContent).toContain('Imported words');
    expect(fileInput.files?.[0]).toBe(file);
    expect(within(dialog).getByLabelText(t('missingImportItems'))).toHaveProperty('value', 'suspend');
    expect(within(dialog).getByRole('group', { name: t('confirmImportHeading') })).toBeTruthy();
    expect(dialog.contains(document.activeElement)).toBe(true);
    expect(fetchMock.mock.calls.filter(([url]) => String(url) === '/api/v1/import/preview')).toHaveLength(1);
    expect(fetchMock.mock.calls.some(([url]) => String(url).endsWith('/apply'))).toBe(false);

    confirm.mockReturnValue(true);
    await user.keyboard('{Escape}');
    expect(confirm).toHaveBeenCalledTimes(3);
    expect(confirm).toHaveBeenLastCalledWith(t('discardTaskChanges'));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    await waitFor(() => expect(document.activeElement).toBe(importButton));
    expect(screen.queryByLabelText(t('chooseImportFile'))).toBeNull();
    expect(screen.queryByText(t('importPreview'))).toBeNull();
    expect(fetchMock.mock.calls.some(([url]) => String(url).endsWith('/apply'))).toBe(false);
  });

  it('opens the add-word dialog with collapsed details and restores its button focus after Escape', async () => {
    const user = userEvent.setup();
    fetchMock.mockImplementation(async (input) => {
      const url = String(input);
      if (url === '/api/v1/decks') return jsonResponse([{
        id: 42, name: 'German course', parent_id: null, lang_target: 'de', lang_native: 'pl', children: [],
      }]);
      if (url === '/api/v1/decks/42/notes') return jsonResponse([]);
      if (url === '/api/v1/llm/status') return jsonResponse({ enabled: false });
      throw new Error(`Unexpected request: ${url}`);
    });
    renderWorkspace();

    await screen.findByRole('heading', { name: t('noNotes') });
    expect(screen.getByRole('button', { name: t('importDeck') })).toBeTruthy();
    const addButton = screen.getByRole('button', { name: t('addNote') });
    addButton.focus();
    await user.keyboard('{Enter}');

    const dialog = screen.getByRole('dialog', { name: t('addNote') });
    const form = within(dialog).getByRole('form', { name: t('addNote') });
    expect(within(form).getByRole('textbox', { name: t('term') })).toBeTruthy();
    expect(within(form).getByRole('textbox', { name: t('translation') })).toBeTruthy();
    await waitFor(() => expect(dialog.contains(document.activeElement)).toBe(true));
    const summary = within(form).getByText(t('moreWordFields'), { selector: 'summary' });
    expect(summary.closest('details')).toHaveProperty('open', false);
    expect(within(form).getByLabelText(t('transcription')).closest('details')).toBe(summary.closest('details'));
    await user.type(within(form).getByLabelText(t('term')), 'Unsaved word');
    await user.click(summary);
    expect(summary.closest('details')).toHaveProperty('open', true);
    expect(within(form).getByRole('textbox', { name: t('transcription') })).toBeTruthy();
    expect(within(form).getByRole('textbox', { name: t('wordNotes') })).toHaveProperty('name', 'notes');
    await user.keyboard('{Escape}');

    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    await waitFor(() => expect(document.activeElement).toBe(addButton));
    expect(screen.queryByRole('form', { name: t('addNote'), hidden: true })).toBeNull();
    expect(fetchMock.mock.calls.every(([, init]) => !init?.method || init.method === 'GET')).toBe(true);
    await user.keyboard('{Enter}');
    const reopenedDialog = screen.getByRole('dialog', { name: t('addNote') });
    expect(within(reopenedDialog).getByLabelText(t('term'))).toHaveProperty('value', '');
    expect(within(reopenedDialog).getByText(t('moreWordFields'), { selector: 'summary' }).closest('details')).toHaveProperty('open', false);
  });

  it('creates root and child decks through their dialog and dropdown paths', async () => {
    const user = userEvent.setup();
    const decks: Array<Record<string, unknown>> = [];
    fetchMock.mockImplementation(async (input, init) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (url === '/api/v1/llm/status') return jsonResponse({ enabled: false });
      if (url === '/api/v1/decks' && method === 'GET') return jsonResponse(decks);
      if (url === '/api/v1/decks' && method === 'POST') {
        const deck = { id: 42 + decks.length, ...JSON.parse(String(init?.body)) as Record<string, unknown>, children: [] };
        decks.push(deck);
        return jsonResponse(deck, 201);
      }
      if (/^\/api\/v1\/decks\/4[23]\/notes$/.test(url)) return jsonResponse([]);
      throw new Error(`Unexpected request: ${method} ${url}`);
    });
    renderWorkspace();

    const sidebar = await screen.findByRole('region', { name: t('deckLibrary') });
    expect(screen.queryByRole('form', { name: t('newDeck') })).toBeNull();
    await user.click(within(sidebar).getByRole('button', { name: t('newDeck') }));
    const rootDialog = screen.getByRole('dialog', { name: t('newDeck') });
    const rootForm = within(rootDialog).getByRole('form', { name: t('newDeck') });
    await user.type(within(rootForm).getByLabelText(t('deckName')), 'English course');
    await user.selectOptions(within(rootForm).getByLabelText(t('targetLanguage')), 'en');
    await user.selectOptions(within(rootForm).getByLabelText(t('nativeLanguage')), 'ru');
    await user.selectOptions(within(rootForm).getByRole('combobox', { name: new RegExp(t('studyDirection')) }), 'both');
    await user.click(within(rootForm).getByRole('button', { name: t('createDeck') }));

    await screen.findByRole('heading', { name: 'English course', level: 1 });
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    const rootCall = fetchMock.mock.calls.find(([url, init]) => String(url) === '/api/v1/decks' && init?.method === 'POST');
    expect(JSON.parse(String(rootCall?.[1]?.body))).toEqual({
      name: 'English course', parent_id: null, lang_target: 'en', lang_native: 'ru', reverse_cards: true, reverse_only: false,
    });

    await user.click(screen.getByRole('button', { name: t('deckActions') }));
    await user.click(screen.getByRole('menuitem', { name: t('addChildDeck') }));
    const childDialog = screen.getByRole('dialog', { name: t('addChildDeck') });
    const childForm = within(childDialog).getByRole('form', { name: t('addChildDeck') });
    expect(within(childForm).getByText('English course')).toBeTruthy();
    expect(within(childForm).getByText(`${t('english')} → ${t('russian')}`, { exact: false })).toBeTruthy();
    expect(within(childForm).queryByLabelText(t('targetLanguage'))).toBeNull();
    expect(within(childForm).queryByLabelText(t('nativeLanguage'))).toBeNull();
    expect(within(childForm).getByRole('combobox', { name: new RegExp(t('studyDirection')) })).toHaveProperty('value', 'reverse');
    await user.type(within(childForm).getByLabelText(t('deckName')), 'Chapter 1');
    await user.click(within(childForm).getByRole('button', { name: t('createDeck') }));

    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    const createCalls = fetchMock.mock.calls.filter(([url, init]) => String(url) === '/api/v1/decks' && init?.method === 'POST');
    expect(createCalls).toHaveLength(2);
    expect(JSON.parse(String(createCalls[1]?.[1]?.body))).toEqual({ name: 'Chapter 1', parent_id: 42, reverse_cards: true, reverse_only: true });
    const deckList = screen.getByRole('list', { name: t('deckList') });
    await user.click(within(deckList).getByRole('button', { name: 'Chapter 1' }));
    expect(screen.getByRole('heading', { name: 'Chapter 1', level: 1 })).toBeTruthy();
    expect(screen.getByText(`${t('english')} → ${t('russian')}`)).toBeTruthy();
  });

  it.each(['renameDeck', 'addChildDeck', 'importExport', 'deckEnrichment'] as const)(
    'restores the deck actions trigger after closing the %s dialog with Escape',
    async (action) => {
      const user = userEvent.setup();
      fetchMock.mockImplementation(async (input) => {
        const url = String(input);
        if (url === '/api/v1/decks') return jsonResponse([{
          id: 42, name: 'German course', parent_id: null, lang_target: 'de', lang_native: 'pl', children: [],
        }]);
        if (url === '/api/v1/decks/42/notes' || url === '/api/v1/import/batches') return jsonResponse([]);
        if (url === '/api/v1/llm/status') return jsonResponse({ enabled: true });
        throw new Error(`Unexpected request: ${url}`);
      });
      renderWorkspace(17);

      await screen.findByRole('heading', { name: 'German course', level: 1 });
      const trigger = screen.getByRole('button', { name: t('deckActions') });
      await user.click(trigger);
      await user.click(screen.getByRole('menuitem', { name: t(action) }));
      const dialog = screen.getByRole('dialog', { name: t(action) });
      await waitFor(() => expect(dialog.contains(document.activeElement)).toBe(true));
      await user.keyboard('{Escape}');

      await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
      await waitFor(() => expect(document.activeElement).toBe(trigger));
      expect(screen.queryByRole('menu')).toBeNull();
      expect(fetchMock.mock.calls.every(([, init]) => !init?.method || init.method === 'GET')).toBe(true);
    },
  );

  it.each(['addNote', 'renameDeck'] as const)(
    'closes the %s dialog when a refetch removes its deck instead of reopening it for the next deck',
    async (action) => {
      const user = userEvent.setup();
      let decks = [{
        id: 42, name: 'German course', parent_id: null, lang_target: 'de', lang_native: 'pl', children: [],
      }, {
        id: 43, name: 'English course', parent_id: null, lang_target: 'en', lang_native: 'ru', children: [],
      }];
      fetchMock.mockImplementation(async (input) => {
        const url = String(input);
        if (url === '/api/v1/decks') return jsonResponse(decks);
        if (url === '/api/v1/llm/status') return jsonResponse({ enabled: false });
        if (url === '/api/v1/decks/42/notes') return jsonResponse([]);
        if (url === '/api/v1/decks/43/notes') return jsonResponse([{ id: 503, term: 'Hello', translation: 'Привет' }]);
        throw new Error(`Unexpected request: ${url}`);
      });
      const { queryClient } = renderWorkspace();

      await screen.findByRole('heading', { name: 'German course', level: 1 });
      async function openTask() {
        if (action === 'addNote') {
          await user.click(screen.getByRole('button', { name: t('addNote') }));
        } else {
          await user.click(screen.getByRole('button', { name: t('deckActions') }));
          await user.click(screen.getByRole('menuitem', { name: t('renameDeck') }));
        }
        return screen.getByRole('dialog', { name: t(action) });
      }
      const dialog = await openTask();
      const inputLabel = t(action === 'addNote' ? 'term' : 'deckName');
      const input = within(dialog).getByLabelText(inputLabel);
      await user.clear(input);
      await user.type(input, 'Draft for removed deck');

      decks = decks.filter((deck) => deck.id !== 42);
      await act(async () => { await queryClient.refetchQueries({ queryKey: ['decks'] }); });

      await screen.findByRole('heading', { name: 'English course', level: 1 });
      await screen.findByText('Hello');
      expect(screen.queryByRole('dialog', { hidden: true })).toBeNull();
      expect(screen.queryByRole('form', { name: t(action), hidden: true })).toBeNull();
      expect(screen.queryByDisplayValue('Draft for removed deck')).toBeNull();
      expect(screen.queryByRole('heading', { name: 'German course', level: 1 })).toBeNull();
      expect(screen.getByRole('button', { name: 'English course' }).getAttribute('aria-current')).toBe('true');
      expect(fetchMock.mock.calls.filter(([url]) => String(url) === '/api/v1/decks')).toHaveLength(2);
      expect(fetchMock.mock.calls.every(([, init]) => !init?.method || init.method === 'GET')).toBe(true);

      const reopenedDialog = await openTask();
      expect(within(reopenedDialog).getByLabelText(inputLabel)).toHaveProperty('value', action === 'addNote' ? '' : 'English course');
    },
  );

  it('does not expose edit or delete controls for subscribed source decks', async () => {
    fetchMock.mockImplementation(async (input) => {
      const url = String(input);
      if (url === '/api/v1/decks') return jsonResponse([{
        id: 77, owner_id: 8, can_edit: false, name: 'A friend’s deck', parent_id: null,
        lang_target: 'de', lang_native: 'pl', reverse_cards: false, children: [],
      }]);
      if (url === '/api/v1/decks/77/notes') return jsonResponse([{ id: 770, term: 'Wort', translation: 'word' }]);
      if (url === '/api/v1/llm/status') return jsonResponse({ enabled: true });
      throw new Error(`Unexpected request: ${url}`);
    });
    renderWorkspace(17);

    await screen.findByRole('heading', { name: 'A friend’s deck', level: 1 });
    await screen.findByText(t('readOnlySubscription'));
    await screen.findByText('Wort');
    expect(screen.queryByRole('form', { name: t('renameDeck') })).toBeNull();
    expect(screen.queryByRole('button', { name: t('addNote') })).toBeNull();
    expect(screen.queryByRole('button', { name: t('deckActions') })).toBeNull();
    expect(screen.queryByRole('button', { name: t('addChildDeck') })).toBeNull();
    expect(screen.queryByRole('button', { name: t('enrichDeck') })).toBeNull();
    expect(screen.queryByRole('menuitem', { name: t('renameDeck'), hidden: true })).toBeNull();
    expect(screen.queryByRole('form', { name: t('addNote'), hidden: true })).toBeNull();
    expect(screen.queryByText(t('deckEnrichmentHint'))).toBeNull();
    expect(screen.queryByRole('dialog', { hidden: true })).toBeNull();
    expect(screen.queryByRole('button', { name: t('edit') })).toBeNull();
    expect(screen.queryByRole('button', { name: `${t('deleteNote')} Wort` })).toBeNull();
    expect(screen.queryByRole('button', { name: `${t('deleteDeck')} A friend’s deck` })).toBeNull();
  });

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
    expect(screen.queryByRole('form', { name: t('renameDeck') })).toBeNull();
    expect(screen.queryByLabelText(t('chooseImportFile'))).toBeNull();
    await user.click(screen.getByRole('button', { name: t('deckActions') }));
    await user.click(screen.getByRole('menuitem', { name: t('renameDeck') }));
    const renameForm = screen.getByRole('form', { name: t('renameDeck') });
    await user.clear(within(renameForm).getByLabelText(t('deckName')));
    await user.type(within(renameForm).getByLabelText(t('deckName')), 'German lessons');
    await user.click(within(renameForm).getByRole('button', { name: t('saveDeck') }));
    await screen.findByRole('heading', { name: 'German lessons' });

    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    await user.click(screen.getByRole('button', { name: t('addNote') }));
    const addForm = screen.getByRole('form', { name: t('addNote') });
    await user.type(within(addForm).getByLabelText(t('term')), 'Hallo');
    await user.click(await within(addForm).findByRole('button', { name: t('fillWord') }));
    await within(addForm).findByText(t('suggestionsNotSaved'));
    await user.click(within(addForm).getByRole('button', { name: t('useSuggestion').replace('{field}', t('translation')) }));
    await user.click(within(addForm).getByRole('button', { name: t('useSuggestion').replace('{field}', t('partOfSpeech')) }));
    expect(within(addForm).getByLabelText(t('translation'))).toHaveProperty('value', 'Cześć');
    await user.click(within(addForm).getByText(t('moreWordFields'), { selector: 'summary' }));
    await user.type(within(addForm).getByLabelText(t('tags'), { exact: false }), 'greeting, common');
    await user.click(within(addForm).getByRole('button', { name: t('addNote') }));
    expect(await screen.findByText('Hallo')).toBeTruthy();
    await waitFor(() => expect(fetchMock.mock.calls.some(([url, init]) => String(url).endsWith('/notes') && init?.method === 'POST')).toBe(true));
    const createCall = fetchMock.mock.calls.find(([url, init]) => String(url) === '/api/v1/decks/42/notes' && init?.method === 'POST');
    expect(JSON.parse(String(createCall?.[1]?.body))).toEqual({
      term: 'Hallo', translation: 'Cześć', tags: ['greeting', 'common'], fields: { pos: 'interjection' },
    });
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());

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
