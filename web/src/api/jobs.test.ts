import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { decodeJobStatus, getJobStatus } from './jobs';

const fetchMock = vi.fn<typeof fetch>();

function response(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

beforeEach(() => {
  fetchMock.mockReset();
  vi.stubGlobal('fetch', fetchMock);
});

afterEach(() => vi.unstubAllGlobals());

describe('job status API', () => {
  it('decodes an in-progress job without exposing a result before completion', () => {
    expect(decodeJobStatus({
      id: 'job-1',
      kind: 'deck_enrich',
      status: 'running',
      progress: 12,
      total: 30,
      error: null,
    })).toEqual({
      id: 'job-1',
      kind: 'deck_enrich',
      status: 'running',
      progress: 12,
      total: 30,
      error: null,
    });
  });

  it('decodes completed partial results', () => {
    const result = {
      items: [{
        note_id: 7,
        term: 'Katze',
        missing_fields: ['transcription', 'fields.article'],
        suggestions: { transcription: '/katsə/', fields: { article: 'die' } },
      }],
      partial: true,
      omitted_count: 2,
    };

    expect(decodeJobStatus({
      id: 'job-1',
      kind: 'deck_enrich',
      status: 'done',
      progress: 5,
      total: 5,
      error: null,
      result,
    }).result).toEqual(result);
  });

  it('fetches an owner-scoped job status from its encoded path', async () => {
    fetchMock.mockResolvedValueOnce(response({
      id: 'job/one',
      kind: 'deck_enrich',
      status: 'queued',
      progress: 0,
      total: 1,
      error: null,
    }));

    await expect(getJobStatus('job/one')).resolves.toMatchObject({ status: 'queued', progress: 0, total: 1 });
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/jobs/job%2Fone', {
      credentials: 'same-origin',
      headers: { Accept: 'application/json' },
    });
  });

  it('rejects malformed states and failed requests', async () => {
    expect(() => decodeJobStatus({
      id: 'job-1', kind: 'deck_enrich', status: 'running', progress: 2, total: 1, error: null,
    })).toThrow();

    fetchMock.mockResolvedValueOnce(response({ error: { code: 'not_found', message: 'Resource not found.' } }, 404));
    await expect(getJobStatus('missing')).rejects.toMatchObject({ code: 'not_found', status: 404 });
  });
});
