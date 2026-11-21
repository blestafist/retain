// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ImportPanel } from './ImportPanel';
import type { DeckNode } from '../api/content';
import { t } from '../i18n/en';

const fetchMock = vi.fn<typeof fetch>();
const summary = {
  source: 'school-wordsets',
  tree: [{
    name: 'Course',
    status: 'new',
    counts: { new: 2, changed: 1, unchanged: 4 },
    warnings: [],
    decks: [{
      name: 'Chapter 1', status: 'changed', counts: { new: 2, changed: 1, unchanged: 4 },
      warnings: ['Expected 8 notes, found 7.'], decks: [],
    }],
  }],
  counts: { new: 2, changed: 1, unchanged: 4 },
  deck_counts: { new: 1, changed: 1, unchanged: 0 },
  warnings: ['Expected 8 notes, found 7.', 'Another warning without a deck.'],
};
const previewResponse = {
  batch_id: 'batch-1', adapter: 'school-wordsets', status: 'previewed', ...summary,
};
const decks: DeckNode[] = [{
  id: 42,
  name: 'German course',
  canEdit: true,
  parentId: null,
  langTarget: 'de',
  langNative: 'pl',
  reverseCards: false, reverseOnly: false,
  wrongArticleRating: 2,
  children: [
    { id: 43, name: 'Chapter A', parentId: 42, canEdit: true, reverseCards: false, reverseOnly: false, wrongArticleRating: 2, children: [] },
    { id: 44, name: 'Shared chapter', parentId: 42, canEdit: false, reverseCards: false, reverseOnly: false, wrongArticleRating: 2, children: [] },
    { id: 45, name: 'Unknown permissions', parentId: 42, reverseCards: false, reverseOnly: false, wrongArticleRating: 2, children: [] },
  ],
}];

function jsonResponse(body: unknown) {
  return new Response(JSON.stringify(body), { headers: { 'Content-Type': 'application/json' } });
}

function renderPanel(queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })) {
  const result = render(
    <QueryClientProvider client={queryClient}>
      <ImportPanel decks={decks} selectedDeck={decks[0]} />
    </QueryClientProvider>,
  );
  return { ...result, queryClient };
}

beforeEach(() => {
  fetchMock.mockReset();
  vi.stubGlobal('fetch', fetchMock);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('ImportPanel', () => {
  it('previews an upload, requires confirmation, and applies the selected import options', async () => {
    const user = userEvent.setup();
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    queryClient.setQueryData(['decks'], decks);
    queryClient.setQueryData(['deck-notes', '42'], []);
    let historyCalls = 0;
    fetchMock.mockImplementation(async (input, init) => {
      const url = String(input);
      if (url === '/api/v1/import/batches') {
        historyCalls += 1;
        return jsonResponse(historyCalls === 1 ? [] : [{
          id: 'batch-1', adapter: 'school-wordsets', filename: 'words.csv', sha256: 'abc', status: 'applied',
          summary, created_at: '2026-01-01T12:00:00Z', applied_at: '2026-01-01T12:01:00Z',
        }]);
      }
      if (url === '/api/v1/import/preview') return jsonResponse(previewResponse);
      if (url === '/api/v1/import/batch-1/apply') return jsonResponse({ ...previewResponse, status: 'applied' });
      throw new Error(`Unexpected request: ${url} (${String(init?.method)})`);
    });
    renderPanel(queryClient);

    const file = new File(['term;translation\nHallo;Cześć'], 'words.csv', { type: 'text/csv' });
    await user.upload(screen.getByLabelText(t('chooseImportFile')), file);
    await user.click(screen.getByRole('button', { name: t('previewImport') }));

    const previewHeading = await screen.findByRole('heading', { name: t('importPreview') });
    expect(document.activeElement).toBe(previewHeading);
    expect(screen.getByText('2 decks')).toBeTruthy();
    expect(screen.getByText('7 words')).toBeTruthy();
    expect(screen.getByText('2 new · 1 changed · 4 unchanged')).toBeTruthy();
    const warnings = within(screen.getByRole('region', { name: t('importWarnings') }));
    expect(warnings.getAllByRole('listitem').map((item) => item.textContent)).toEqual([
      'Chapter 1: Expected 8 notes, found 7.', 'Another warning without a deck.',
    ]);
    expect((screen.getByLabelText(t('targetLanguage')) as HTMLSelectElement).value).toBe('de');
    expect((screen.getByLabelText(t('nativeLanguage')) as HTMLSelectElement).value).toBe('pl');

    const destination = screen.getByLabelText(t('optionalParentDeck'));
    expect(within(destination).getAllByRole('option').map((option) => option.textContent)).toEqual([
      t('noParentDeck'), 'German course', 'German course › Chapter A',
    ]);
    await user.selectOptions(destination, '43');
    await user.click(screen.getByText(t('importReimportOptions')));
    await user.selectOptions(screen.getByLabelText(t('missingImportItems')), 'suspend');
    await user.click(screen.getByRole('button', { name: t('reviewBeforeApply') }));
    expect(screen.getByRole('group', { name: t('confirmImportHeading') })).toBeTruthy();
    expect(document.activeElement).toBe(screen.getByRole('heading', { name: t('confirmImportHeading') }));
    expect(screen.getByText(`${t('confirmImportDescription')} ${t('confirmSuspendMissingItems')}`)).toBeTruthy();
    expect(fetchMock.mock.calls.some(([url]) => String(url).endsWith('/apply'))).toBe(false);
    await user.click(screen.getByRole('button', { name: t('confirmApplyImport') }));

    expect(await screen.findByText(t('importApplied'))).toBeTruthy();
    const applyCall = fetchMock.mock.calls.find(([url]) => url === '/api/v1/import/batch-1/apply');
    expect(JSON.parse(String(applyCall?.[1]?.body))).toEqual({
      lang_target: 'de', lang_native: 'pl', parent_deck_id: 43, on_missing: 'suspend',
    });
    await waitFor(() => expect(historyCalls).toBeGreaterThan(1));
    await user.click(screen.getByText(t('importHistory')));
    expect(await screen.findByText('words.csv')).toBeTruthy();
    expect(queryClient.getQueryState(['decks'])?.isInvalidated).toBe(true);
    expect(queryClient.getQueryState(['deck-notes', '42'])?.isInvalidated).toBe(true);
    const previewCall = fetchMock.mock.calls.find(([url]) => url === '/api/v1/import/preview');
    expect((previewCall?.[1]?.body as FormData).get('file')).toMatchObject({ name: 'words.csv' });
  });

  it('exports the selected deck as CSV and starts a named download', async () => {
    const createObjectURL = vi.fn(() => 'blob:csv-export');
    const revokeObjectURL = vi.fn();
    Object.defineProperty(URL, 'createObjectURL', { configurable: true, value: createObjectURL });
    Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, value: revokeObjectURL });
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function mockClick(this: HTMLAnchorElement) {
      expect(this.download).toBe('German.csv');
      expect(this.href).toBe('blob:csv-export');
    });
    fetchMock.mockImplementation(async (input) => {
      if (String(input) === '/api/v1/import/batches') return jsonResponse([]);
      if (String(input) === '/api/v1/decks/42/export?format=csv') {
        return new Response('term;translation', {
          headers: { 'Content-Type': 'text/csv', 'Content-Disposition': 'attachment; filename="German.csv"' },
        });
      }
      throw new Error(`Unexpected request: ${String(input)}`);
    });
    const user = userEvent.setup();
    renderPanel();

    await user.click(screen.getByRole('button', { name: t('exportCsv') }));

    expect(click).toHaveBeenCalledOnce();
    expect(createObjectURL).toHaveBeenCalledOnce();
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:csv-export');
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/decks/42/export?format=csv', expect.objectContaining({ credentials: 'same-origin' }));
  });

  it('does not apply to top level if the selected destination stops being writable', async () => {
    fetchMock.mockImplementation(async (input) => jsonResponse(String(input).endsWith('/preview') ? previewResponse : []));
    const user = userEvent.setup();
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const panel = (choices: DeckNode[]) => (
      <QueryClientProvider client={queryClient}><ImportPanel decks={choices} /></QueryClientProvider>
    );
    const { rerender } = render(panel(decks));
    await user.upload(screen.getByLabelText(t('chooseImportFile')), new File(['a;b'], 'words.csv'));
    await user.click(screen.getByRole('button', { name: t('previewImport') }));
    const destination = await screen.findByLabelText(t('optionalParentDeck'));
    await user.selectOptions(destination, '42');
    await user.click(screen.getByRole('button', { name: t('reviewBeforeApply') }));

    rerender(panel([{ ...decks[0], canEdit: false }]));
    expect(screen.getByRole('alert').textContent).toBe(t('importChooseAnotherDestination'));
    const apply = screen.getByRole('button', { name: t('confirmApplyImport') });
    expect(apply).toHaveProperty('disabled', true);
    await user.click(apply);
    expect(fetchMock.mock.calls.some(([url]) => String(url).endsWith('/apply'))).toBe(false);
    await user.click(screen.getByRole('button', { name: t('cancel') }));
    await user.selectOptions(destination, '');
    expect(screen.getByRole('button', { name: t('reviewBeforeApply') })).toHaveProperty('disabled', false);
  });

  it('keeps a larger deck breakdown collapsed without hiding the import totals or settings', async () => {
    const preview = {
      ...previewResponse,
      tree: Array.from({ length: 5 }, (_, index) => ({
        name: `Chapter ${index + 1}`, status: 'new', counts: { new: 1, changed: 0, unchanged: 0 }, warnings: [], decks: [],
      })),
      deck_counts: { new: 5, changed: 0, unchanged: 0 },
      counts: { new: 5, changed: 0, unchanged: 0 },
      warnings: [],
    };
    fetchMock.mockImplementation(async (input) => jsonResponse(String(input).endsWith('/preview') ? preview : []));
    const user = userEvent.setup();
    renderPanel();
    await user.upload(screen.getByLabelText(t('chooseImportFile')), new File(['a;b'], 'words.csv'));
    await user.click(screen.getByRole('button', { name: t('previewImport') }));

    const deckPreview = await screen.findByText(t('importTree'));
    const details = deckPreview.closest('details');
    expect(details).toHaveProperty('open', false);
    expect(screen.getByText('5 decks')).toBeTruthy();
    expect(screen.getByText('5 words')).toBeTruthy();
    expect(screen.getByLabelText(t('optionalParentDeck'))).toBeTruthy();
    expect(screen.queryByRole('region', { name: t('importWarnings') })).toBeNull();
    await user.tab();
    expect(document.activeElement).toBe(deckPreview);
    await user.click(deckPreview);
    expect(details).toHaveProperty('open', true);
    expect(within(screen.getByRole('list', { name: t('importTree') })).getAllByRole('listitem')).toHaveLength(5);
  });

  it('explains invalid file types and keeps export disabled without a selected deck', async () => {
    fetchMock.mockResolvedValue(jsonResponse([]));
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <ImportPanel decks={[]} />
      </QueryClientProvider>,
    );

    const input = screen.getByLabelText(t('chooseImportFile')) as HTMLInputElement;
    Object.defineProperty(input, 'files', {
      configurable: true,
      value: [new File(['bad'], 'notes.txt', { type: 'text/plain' })],
    });
    fireEvent.change(input);
    expect(screen.getByRole('alert')).toHaveProperty('textContent', t('importFileTypeError'));
    expect(screen.getByRole('button', { name: t('exportCsv') })).toHaveProperty('disabled', true);
  });
});
