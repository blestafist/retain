// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  applyImport,
  decodeImportHistory,
  decodeImportPreview,
  downloadExport,
  exportDeck,
  getImportHistory,
  previewImport,
} from './import';

const fetchMock = vi.fn<typeof fetch>();

function response(body?: unknown, status = 200, headers?: HeadersInit) {
  return new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: body === undefined ? headers : { 'Content-Type': 'application/json', ...Object.fromEntries(new Headers(headers).entries()) },
  });
}

const summary = {
  source: 'school-wordsets',
  tree: [{
    external_id: 'class:1',
    name: 'German class',
    status: 'new',
    counts: { new: 2, changed: 0, unchanged: 0 },
    warnings: ['Expected 3 notes, found 2.'],
    decks: [{
      name: 'Chapter 1',
      status: 'new',
      counts: { new: 2, changed: 0, unchanged: 0 },
      warnings: [],
      decks: [],
    }],
  }],
  counts: { new: 2, changed: 0, unchanged: 0 },
  deck_counts: { new: 2, changed: 0, unchanged: 0 },
  warnings: ['Expected 3 notes, found 2.'],
};

const preview = {
  batch_id: 'batch-123',
  adapter: 'school-wordsets',
  status: 'previewed',
  ...summary,
};

beforeEach(() => {
  fetchMock.mockReset();
  vi.stubGlobal('fetch', fetchMock);
});

afterEach(() => vi.unstubAllGlobals());

describe('import response decoders', () => {
  it('decodes recursive preview summaries and history entries', () => {
    expect(decodeImportPreview(preview)).toMatchObject({
      batch_id: 'batch-123',
      tree: [{ name: 'German class', decks: [{ name: 'Chapter 1' }] }],
      warnings: ['Expected 3 notes, found 2.'],
    });
    expect(decodeImportHistory([{
      id: 'batch-123',
      adapter: 'school-wordsets',
      filename: 'class.json',
      sha256: 'abc',
      status: 'applied',
      summary,
      created_at: '2026-01-01T12:00:00Z',
      applied_at: '2026-01-01T12:01:00Z',
    }])).toMatchObject([{ id: 'batch-123', filename: 'class.json', status: 'applied' }]);
  });

  it('rejects malformed preview counts, deck trees, and history timestamps', () => {
    expect(() => decodeImportPreview({ ...preview, counts: { new: '2', changed: 0, unchanged: 0 } })).toThrow();
    expect(() => decodeImportPreview({ ...preview, tree: [{ name: 'Missing status' }] })).toThrow();
    expect(() => decodeImportHistory([{ id: 'batch', created_at: 'not-a-date' }])).toThrow();
  });
});

describe('import and export API', () => {
  it('uploads a multipart file with its filename and same-origin credentials', async () => {
    fetchMock.mockResolvedValue(response(preview));
    const file = new File(['{"format":"retain/v1"}'], 'vocabulary.json', { type: 'application/json' });

    await previewImport(file, 'csv');

    const [url, init] = fetchMock.mock.calls[0] ?? [];
    expect(url).toBe('/api/v1/import/preview');
    expect(init?.credentials).toBe('same-origin');
    expect(init?.method).toBe('POST');
    expect(new Headers(init?.headers).has('Content-Type')).toBe(false);
    expect(init?.body).toBeInstanceOf(FormData);
    expect((init?.body as FormData).get('file')).toMatchObject({ name: 'vocabulary.json' });
    expect((init?.body as FormData).get('adapter')).toBe('csv');
  });

  it('posts apply choices, omits an unset parent, and loads recent batches', async () => {
    fetchMock
      .mockResolvedValueOnce(response({ ...preview, status: 'applied' }))
      .mockResolvedValueOnce(response({ ...preview, status: 'applied' }))
      .mockResolvedValueOnce(response([]));

    await applyImport('batch/id', { lang_target: 'de', lang_native: 'pl', on_missing: 'keep' });
    await applyImport('batch-123', { lang_target: 'en', lang_native: 'ru', parent_deck_id: 42, on_missing: 'suspend' });
    await expect(getImportHistory()).resolves.toEqual([]);

    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      '/api/v1/import/batch%2Fid/apply',
      '/api/v1/import/batch-123/apply',
      '/api/v1/import/batches',
    ]);
    expect(JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body))).toEqual({
      lang_target: 'de', lang_native: 'pl', on_missing: 'keep',
    });
    expect(JSON.parse(String(fetchMock.mock.calls[1]?.[1]?.body))).toEqual({
      lang_target: 'en', lang_native: 'ru', parent_deck_id: 42, on_missing: 'suspend',
    });
  });

  it('downloads an export blob and decodes RFC 5987 filenames', async () => {
    fetchMock.mockResolvedValue(new Response('term;translation\nHallo;Cześć', {
      headers: {
        'Content-Type': 'text/csv; charset=utf-8',
        'Content-Disposition': "attachment; filename*=UTF-8''German%20words.csv",
      },
    }));

    const file = await exportDeck('deck/7', 'csv');

    expect(file.filename).toBe('German words.csv');
    expect(await file.blob.text()).toContain('Hallo;Cześć');
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/decks/deck%2F7/export?format=csv', expect.objectContaining({
      credentials: 'same-origin',
      headers: { Accept: 'text/csv' },
    }));
  });

  it('requests a retain/v1 export with a JSON filename fallback', async () => {
    fetchMock.mockResolvedValue(new Response('{"format":"retain/v1"}', {
      headers: { 'Content-Type': 'application/json' },
    }));

    const file = await exportDeck(9, 'retain');

    expect(file.filename).toBe('deck.json');
    expect(file.contentType).toBe('application/json');
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/decks/9/export?format=retain', expect.objectContaining({
      credentials: 'same-origin',
      headers: { Accept: 'application/json' },
    }));
  });

  it('uses a safe fallback filename and starts a browser download', () => {
    const createObjectURL = vi.fn(() => 'blob:export-test');
    const revokeObjectURL = vi.fn();
    Object.defineProperty(URL, 'createObjectURL', { configurable: true, value: createObjectURL });
    Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, value: revokeObjectURL });
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function mockClick(this: HTMLAnchorElement) {
      expect(this.download).toBe('deck.json');
      expect(this.href).toBe('blob:export-test');
    });

    downloadExport({ blob: new Blob(['{}']), filename: 'deck.json', contentType: 'application/json' });

    expect(createObjectURL).toHaveBeenCalledOnce();
    expect(click).toHaveBeenCalledOnce();
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:export-test');
  });
});
