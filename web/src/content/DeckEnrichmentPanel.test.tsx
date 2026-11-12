// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
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
  sessionStorage.clear();
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
    const trackedJobs = JSON.parse(localStorage.getItem('retain:deck-enrichment-jobs:v1:17') ?? '[]') as Array<Record<string, unknown>>;
    expect(trackedJobs).toHaveLength(1);
    expect(trackedJobs[0]).toMatchObject({ jobId: 'job-42', deckId: '70', deckName: 'German' });
    expect(typeof trackedJobs[0].createdAt).toBe('number');

    firstMount.unmount();
    renderPanel(17);

    expect(await screen.findByText('Hallo Welt.')).toBeTruthy();
    expect(screen.getByText(/Deck enrichment is complete/)).toBeTruthy();
  });

  it('merges jobs started in separate tabs and syncs them from owner-scoped storage events', async () => {
    fetchMock.mockImplementation(async (input, init) => {
      const url = String(input);
      if (url === '/api/v1/decks/70/enrich' && init?.method === 'POST') return Response.json({ job_id: 'tab-one-job' }, { status: 202 });
      if (url === '/api/v1/decks/71/enrich' && init?.method === 'POST') return Response.json({ job_id: 'tab-two-job' }, { status: 202 });
      if (url === '/api/v1/jobs/tab-one-job' || url === '/api/v1/jobs/tab-two-job') {
        const id = url.split('/').at(-1) ?? '';
        return Response.json({ ...completedJob(id), status: 'queued', result: null });
      }
      throw new Error(`Unexpected request: ${init?.method ?? 'GET'} ${url}`);
    });

    const tabOne = renderPanel(17);
    const tabTwo = render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}>
        <DeckEnrichmentPanel ownerId={17} deckId={71} deckName="English" enabled />
      </QueryClientProvider>,
    );
    fireEvent.click(within(tabOne.container).getByRole('button', { name: t('enrichDeck') }));
    fireEvent.click(within(tabTwo.container).getByRole('button', { name: t('enrichDeck') }));

    await waitFor(() => {
      const jobs = JSON.parse(localStorage.getItem('retain:deck-enrichment-jobs:v1:17') ?? '[]') as Array<Record<string, unknown>>;
      expect(jobs.map((job) => job.jobId).sort()).toEqual(['tab-one-job', 'tab-two-job']);
      expect(jobs.every((job) => typeof job.createdAt === 'number')).toBe(true);
    });

    const event = new StorageEvent('storage', {
      key: 'retain:deck-enrichment-jobs:v1:17',
      newValue: localStorage.getItem('retain:deck-enrichment-jobs:v1:17'),
      storageArea: localStorage,
    });
    window.dispatchEvent(event);

    for (const tab of [tabOne, tabTwo]) {
      await waitFor(() => {
        const options = within(tab.container).getAllByRole('option').map((option) => option.getAttribute('value'));
        expect(options).toContain('tab-one-job');
        expect(options).toContain('tab-two-job');
      });
    }
    window.dispatchEvent(new StorageEvent('storage', {
      key: 'retain:deck-enrichment-jobs:v1:18',
      newValue: JSON.stringify([{ jobId: 'other-owner-job', deckId: '90', deckName: 'Private' }]),
      storageArea: localStorage,
    }));
    expect(within(tabOne.container).queryByRole('option', { name: /other-owner-job/ })).toBeNull();
    expect(within(tabTwo.container).queryByRole('option', { name: /other-owner-job/ })).toBeNull();
  });

  it('selects and displays a completed job first received through a storage event', async () => {
    const jobId = 'storage-event-job';
    fetchMock.mockImplementation(async (input) => {
      if (String(input) === `/api/v1/jobs/${jobId}`) {
        return Response.json(completedJob(jobId, 'Storage suggestion'));
      }
      throw new Error(`Unexpected request: ${String(input)}`);
    });

    renderPanel(17);
    const key = 'retain:deck-enrichment-jobs:v1:17';
    const newValue = JSON.stringify([{ jobId, deckId: '70', deckName: 'German' }]);
    localStorage.setItem(key, newValue);
    window.dispatchEvent(new StorageEvent('storage', { key, newValue, storageArea: localStorage }));

    expect(await screen.findByText('Storage suggestion')).toBeTruthy();
    expect(await screen.findByText(/Deck enrichment is complete/)).toBeTruthy();
    expect((screen.getByRole('combobox') as HTMLSelectElement).value).toBe(jobId);
  });

  it('canonicalizes jobs by their creation time and stops rewriting once storage events converge', async () => {
    const key = 'retain:deck-enrichment-jobs:v1:17';
    const olderJob = { jobId: 'random-id-z', deckId: '70', deckName: 'German', createdAt: 100 };
    const newerJob = { jobId: 'random-id-a', deckId: '71', deckName: 'English', createdAt: 200 };
    const reversed = [newerJob, olderJob];
    const canonical = [olderJob, newerJob];
    fetchMock.mockImplementation(async (input) => {
      const url = String(input);
      if (url.startsWith('/api/v1/jobs/')) {
        const id = url.split('/').at(-1) ?? '';
        return Response.json({ ...completedJob(id), status: 'queued', result: null });
      }
      throw new Error(`Unexpected request: ${url}`);
    });

    const tabOne = renderPanel(17);
    const tabTwo = render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}>
        <DeckEnrichmentPanel ownerId={17} deckId={71} deckName="English" enabled />
      </QueryClientProvider>,
    );
    const setItem = vi.spyOn(Storage.prototype, 'setItem');
    localStorage.setItem(key, JSON.stringify(reversed));
    setItem.mockClear();

    const dispatchStorageEvent = (jobs: typeof reversed) => {
      window.dispatchEvent(new StorageEvent('storage', {
        key,
        newValue: JSON.stringify(jobs),
        storageArea: localStorage,
      }));
    };
    dispatchStorageEvent(reversed);

    await waitFor(() => {
      expect(localStorage.getItem(key)).toBe(JSON.stringify(canonical));
      for (const tab of [tabOne, tabTwo]) {
        expect((within(tab.container).getByRole('combobox') as HTMLSelectElement).value).toBe(newerJob.jobId);
      }
    });
    expect(setItem.mock.calls.filter(([calledKey]) => calledKey === key)).toHaveLength(1);

    dispatchStorageEvent(reversed);
    dispatchStorageEvent(canonical);
    await waitFor(() => {
      expect(setItem.mock.calls.filter(([calledKey]) => calledKey === key)).toHaveLength(1);
    });
  });

  it('selects a newly received job even when the tab already has a selected job', async () => {
    const key = 'retain:deck-enrichment-jobs:v1:17';
    const oldJob = { jobId: 'random-old-job', deckId: '70', deckName: 'German', createdAt: 100 };
    const newJob = { jobId: 'random-new-job', deckId: '71', deckName: 'English', createdAt: 200 };
    localStorage.setItem(key, JSON.stringify([oldJob]));
    fetchMock.mockImplementation(async (input) => {
      const url = String(input);
      if (url === `/api/v1/jobs/${oldJob.jobId}`) return Response.json({ ...completedJob(oldJob.jobId), status: 'queued', result: null });
      if (url === `/api/v1/jobs/${newJob.jobId}`) return Response.json(completedJob(newJob.jobId, 'New tab suggestion'));
      throw new Error(`Unexpected request: ${url}`);
    });

    renderPanel(17);
    await waitFor(() => expect((screen.getByRole('combobox') as HTMLSelectElement).value).toBe(oldJob.jobId));

    const newValue = JSON.stringify([oldJob, newJob]);
    localStorage.setItem(key, newValue);
    window.dispatchEvent(new StorageEvent('storage', { key, newValue, storageArea: localStorage }));

    expect(await screen.findByText('New tab suggestion')).toBeTruthy();
    expect((screen.getByRole('combobox') as HTMLSelectElement).value).toBe(newJob.jobId);
  });

  it('keeps the newest random-ID job when the tracked history exceeds its limit', async () => {
    const key = 'retain:deck-enrichment-jobs:v1:17';
    const existingJobs = Array.from({ length: 20 }, (_, index) => ({
      jobId: `random-job-${index}`,
      deckId: '70',
      deckName: 'German',
      createdAt: index + 1,
    }));
    const newestJob = { jobId: 'random-newest-job', deckId: '70', deckName: 'German', createdAt: 21 };
    localStorage.setItem(key, JSON.stringify(existingJobs));
    fetchMock.mockImplementation(async (input) => {
      const url = String(input);
      if (url.startsWith('/api/v1/jobs/')) {
        const id = url.split('/').at(-1) ?? '';
        return Response.json({ ...completedJob(id), status: 'queued', result: null });
      }
      throw new Error(`Unexpected request: ${url}`);
    });

    renderPanel(17);
    const newValue = JSON.stringify([...existingJobs, newestJob]);
    localStorage.setItem(key, newValue);
    window.dispatchEvent(new StorageEvent('storage', { key, newValue, storageArea: localStorage }));

    await waitFor(() => {
      const options = screen.getAllByRole('option').map((option) => (option as HTMLOptionElement).value);
      expect(options).toHaveLength(20);
      expect(options).toContain(newestJob.jobId);
      expect(options).not.toContain(existingJobs[0].jobId);
      expect((screen.getByRole('combobox') as HTMLSelectElement).value).toBe(newestJob.jobId);
    });
  });

  it('restores and migrates a legacy owner-scoped session job', async () => {
    sessionStorage.setItem('retain:deck-enrichment-jobs:v1:17', JSON.stringify([
      { jobId: 'legacy-job', deckId: '70', deckName: 'German' },
    ]));
    fetchMock.mockImplementation(async (input) => {
      if (String(input) === '/api/v1/jobs/legacy-job') return Response.json(completedJob('legacy-job'));
      throw new Error(`Unexpected request: ${String(input)}`);
    });

    renderPanel(17);

    expect(await screen.findByText('Hallo Welt.')).toBeTruthy();
    await waitFor(() => {
      expect(JSON.parse(localStorage.getItem('retain:deck-enrichment-jobs:v1:17') ?? '[]')).toEqual([
        { jobId: 'legacy-job', deckId: '70', deckName: 'German' },
      ]);
      expect(sessionStorage.getItem('retain:deck-enrichment-jobs:v1:17')).toBeNull();
    });
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
