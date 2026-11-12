// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { DeckEnrichmentPanel } from './DeckEnrichmentPanel';
import type { JobStatus } from '../api/jobs';
import { t } from '../i18n/en';

const fetchMock = vi.fn<typeof fetch>();
const suggestions = {
  items: [{
    note_id: 9,
    term: 'Hallo',
    missing_fields: ['example'],
    suggestions: { example: 'Hallo Welt.', fields: {} },
  }],
  partial: false,
  omitted_count: 0,
};

function completedJob(id: string, phrase = 'Hallo Welt.'): JobStatus {
  return {
    id,
    kind: 'deck_enrich',
    status: 'done',
    progress: 1,
    total: 1,
    error: null,
    result: {
      ...suggestions,
      items: [{ ...suggestions.items[0], suggestions: { example: phrase, fields: {} } }],
    },
  };
}

function renderPanel(ownerId: number) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <DeckEnrichmentPanel ownerId={ownerId} deckId={70} deckName="German" enabled />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  localStorage.clear();
  fetchMock.mockReset();
  vi.stubGlobal('fetch', fetchMock);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('DeckEnrichmentPanel', () => {
  it('retains a completed job and suggestions after unmount and remount for the same owner', async () => {
    fetchMock.mockImplementation(async (input, init) => {
      const url = String(input);
      if (url === '/api/v1/decks/70/enrich' && init?.method === 'POST') return Response.json({ job_id: 'job-42' }, { status: 202 });
      if (url === '/api/v1/jobs/job-42') return Response.json(completedJob('job-42'));
      throw new Error(`Unexpected request: ${init?.method ?? 'GET'} ${url}`);
    });

    const firstMount = renderPanel(17);
    fireEvent.click(screen.getByRole('button', { name: t('enrichDeck') }));
    expect(await screen.findByText('Hallo Welt.')).toBeTruthy();
    expect(JSON.parse(localStorage.getItem('retain:deck-enrichment-jobs:v1:17') ?? '[]')).toEqual([
      { jobId: 'job-42', deckId: '70', deckName: 'German' },
    ]);

    firstMount.unmount();
    renderPanel(17);

    expect(await screen.findByText('Hallo Welt.')).toBeTruthy();
    expect(screen.getByText(/Deck enrichment is complete/)).toBeTruthy();
  });

  it("does not show the previous owner's tracked jobs or suggestions after an owner switch", async () => {
    localStorage.setItem('retain:deck-enrichment-jobs:v1:17', JSON.stringify([
      { jobId: 'owner-17-job', deckId: '70', deckName: 'Private German' },
    ]));
    fetchMock.mockImplementation(async (input) => {
      if (String(input) === '/api/v1/jobs/owner-17-job') {
        return Response.json(completedJob('owner-17-job', 'Private suggestion'));
      }
      throw new Error(`Unexpected request: ${String(input)}`);
    });

    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    const panel = (ownerId: number) => (
      <QueryClientProvider client={queryClient}>
        <DeckEnrichmentPanel ownerId={ownerId} deckId={70} deckName="German" enabled />
      </QueryClientProvider>
    );
    const view = render(panel(17));
    expect(await screen.findByText('Private suggestion')).toBeTruthy();

    view.rerender(panel(18));

    expect(screen.queryByText('Private suggestion')).toBeNull();
    expect(screen.queryByRole('combobox', { name: t('enrichmentProgressLabel') })).toBeNull();
  });
});
