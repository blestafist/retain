// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ImportPanel } from './ImportPanel';
import type { DeckNode } from '../api/content';
import { t } from '../i18n/en';

const fetchMock = vi.fn<typeof fetch>();
const summary = {
  source: 'school-wordsets',
  tree: [{
    name: 'Chapter 1',
    status: 'new',
    counts: { new: 2, changed: 0, unchanged: 0 },
    warnings: [],
    decks: [],
  }],
  counts: { new: 2, changed: 0, unchanged: 0 },
  deck_counts: { new: 1, changed: 0, unchanged: 0 },
  warnings: ['Expected 3 notes, found 2.'],
};
const previewResponse = {
  batch_id: 'batch-1', adapter: 'school-wordsets', status: 'previewed', ...summary,
};
const decks: DeckNode[] = [{
  id: 42,
  name: 'German course',
  parentId: null,
  langTarget: 'de',
  langNative: 'pl',
  reverseCards: false,
  wrongArticleRating: 2,
  children: [{ id: 43, name: 'Chapter A', parentId: 42, reverseCards: false, wrongArticleRating: 2, children: [] }],
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

    expect(await screen.findByRole('heading', { name: t('importPreview') })).toBeTruthy();
    expect(screen.getByText('Expected 3 notes, found 2.')).toBeTruthy();
    expect((screen.getByLabelText(t('targetLanguage')) as HTMLSelectElement).value).toBe('de');
    expect((screen.getByLabelText(t('nativeLanguage')) as HTMLSelectElement).value).toBe('pl');

    await user.selectOptions(screen.getByLabelText(t('optionalParentDeck')), '43');
    await user.selectOptions(screen.getByLabelText(t('missingImportItems')), 'suspend');
    await user.click(screen.getByRole('button', { name: t('reviewBeforeApply') }));
    expect(screen.getByRole('group', { name: t('confirmImportHeading') })).toBeTruthy();
    await user.click(screen.getByRole('button', { name: t('confirmApplyImport') }));

    expect(await screen.findByText(t('importApplied'))).toBeTruthy();
    const applyCall = fetchMock.mock.calls.find(([url]) => url === '/api/v1/import/batch-1/apply');
    expect(JSON.parse(String(applyCall?.[1]?.body))).toEqual({
      lang_target: 'de', lang_native: 'pl', parent_deck_id: 43, on_missing: 'suspend',
    });
    await waitFor(() => expect(historyCalls).toBeGreaterThan(1));
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
