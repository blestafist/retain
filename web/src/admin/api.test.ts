import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { getAdminLlmModels, getAdminLlmUsage, setAdminLlmModel } from './api';

const fetchMock = vi.fn<typeof fetch>();

function response(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

beforeEach(() => {
  fetchMock.mockReset();
  vi.stubGlobal('fetch', fetchMock);
});

afterEach(() => vi.unstubAllGlobals());

describe('admin LLM API', () => {
  it('reads model availability and selected model', async () => {
    const models = { models: ['model-a', 'model-b'], selected_model: 'model-a', available: true, warning: null };
    fetchMock.mockResolvedValue(response(models));

    await expect(getAdminLlmModels()).resolves.toEqual(models);
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/admin/llm/models', expect.objectContaining({
      credentials: 'same-origin',
      method: undefined,
    }));
  });

  it('switches to the requested listed model through the model endpoint', async () => {
    fetchMock.mockResolvedValue(response({ selected_model: 'model-b' }));

    await expect(setAdminLlmModel('model-b')).resolves.toEqual({ selected_model: 'model-b' });
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/admin/llm/model', expect.objectContaining({
      method: 'PUT',
      body: JSON.stringify({ model: 'model-b' }),
    }));
  });

  it('surfaces model switch API errors', async () => {
    fetchMock.mockResolvedValue(response({ error: { code: 'model_unavailable', message: 'Choose an available model.' } }, 503));
    await expect(setAdminLlmModel('missing')).rejects.toMatchObject({
      status: 503,
      code: 'model_unavailable',
      serverMessage: 'Choose an available model.',
    });
  });

  it('reads lifetime usage by user and model', async () => {
    const usage = { users: [{
      user_id: 3,
      name: 'Morgan',
      email: 'morgan@example.com',
      models: [{ model: 'model-a', kind: 'ask', requests: 5, cached_requests: 2, prompt_tokens: 30, completion_tokens: 12 }],
    }] };
    fetchMock.mockResolvedValue(response(usage));

    await expect(getAdminLlmUsage()).resolves.toEqual(usage);
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/admin/llm-usage', expect.anything());
  });

  it('rejects malformed data instead of rendering it', async () => {
    fetchMock.mockResolvedValue(response({ users: [{ user_id: 3, models: [] }] }));
    await expect(getAdminLlmUsage()).rejects.toMatchObject({ code: 'invalid_response' });
  });
});
