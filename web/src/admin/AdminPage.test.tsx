// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { t } from '../i18n/en';
import { AdminPage } from './AdminPage';

const fetchMock = vi.fn<typeof fetch>();

function response(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

function setupFetch(options: { enabled?: boolean; available?: boolean; switchError?: boolean } = {}) {
  fetchMock.mockImplementation(async (input, init) => {
    const url = String(input);
    if (url === '/api/v1/llm/status') return response({ enabled: options.enabled ?? true });
    if (url === '/api/v1/admin/llm/models') return response({
      models: options.available === false ? [] : ['model-a', 'model-b'],
      selected_model: 'model-a',
      available: options.available !== false,
      warning: options.available === false ? 'Discovery is unavailable.' : null,
    });
    if (url === '/api/v1/admin/llm/model' && init?.method === 'PUT') {
      return options.switchError
        ? response({ error: { code: 'model_unavailable', message: 'Model switch failed safely.' } }, 503)
        : response({ selected_model: 'model-b' });
    }
    if (url === '/api/v1/admin/llm-usage') return response({ users: [{
      user_id: 4,
      name: 'Morgan',
      email: 'morgan@example.com',
      models: [{ model: 'model-a', kind: 'ask', requests: 8, cached_requests: 3, prompt_tokens: 44, completion_tokens: 22 }],
    }] });
    throw new Error(`Unexpected request ${init?.method ?? 'GET'} ${url} (${String(input)})`);
  });
  vi.stubGlobal('fetch', fetchMock);
}

function renderPage() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(<QueryClientProvider client={queryClient}><AdminPage /></QueryClientProvider>);
}

beforeEach(() => fetchMock.mockReset());
afterEach(() => {
  fetchMock.mockResolvedValue(response({}));
  cleanup();
  vi.unstubAllGlobals();
});

describe('AdminPage', () => {
  it('switches only to a listed model and renders lifetime usage without cost', async () => {
    const user = userEvent.setup();
    setupFetch();
    renderPage();

    const modelSelect = await screen.findByLabelText(t('selectedLlmModel'));
    expect((screen.getByRole('button', { name: t('saveLlmModel') }) as HTMLButtonElement).disabled).toBe(true);
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/admin/llm/models', expect.anything());
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/admin/llm-usage', expect.anything());
    expect(screen.getByRole('cell', { name: 'Morgan' })).toBeTruthy();
    expect(screen.getByRole('cell', { name: '44' })).toBeTruthy();
    expect(screen.queryByRole('columnheader', { name: /cost/i })).toBeNull();

    await user.selectOptions(modelSelect, 'model-b');
    const saveButton = screen.getByRole('button', { name: t('saveLlmModel') });
    expect((saveButton as HTMLButtonElement).disabled).toBe(false);
    await user.click(saveButton);
    await screen.findByText(t('llmModelSaved'));

    expect(fetchMock).toHaveBeenCalledWith('/api/v1/admin/llm/model', expect.objectContaining({
      method: 'PUT',
      body: JSON.stringify({ model: 'model-b' }),
    }));
  });

  it('shows safe model-switch errors and leaves the selection available for retry', async () => {
    const user = userEvent.setup();
    setupFetch({ switchError: true });
    renderPage();

    await user.selectOptions(await screen.findByLabelText(t('selectedLlmModel')), 'model-b');
    await user.click(screen.getByRole('button', { name: t('saveLlmModel') }));

    expect((await screen.findByRole('alert')).textContent).toContain('Model switch failed safely.');
    expect(screen.getByLabelText(t('selectedLlmModel'))).toHaveProperty('value', 'model-b');
  });

  it('disables model selection if discovery is unavailable and offers refresh', async () => {
    setupFetch({ available: false });
    renderPage();

    expect(await screen.findByText('Discovery is unavailable.')).toBeTruthy();
    expect((screen.getByLabelText(t('selectedLlmModel')) as HTMLSelectElement).disabled).toBe(true);
    expect((screen.getByRole('button', { name: t('saveLlmModel') }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole('button', { name: t('refresh') }) as HTMLButtonElement).disabled).toBe(false);
  });

  it('hides model controls when the global LLM feature is disabled but keeps admin usage visible', async () => {
    setupFetch({ enabled: false });
    renderPage();

    expect(await screen.findByText(t('llmFeaturesDisabled'))).toBeTruthy();
    expect(screen.queryByLabelText(t('selectedLlmModel'))).toBeNull();
    await waitFor(() => expect(screen.getByRole('cell', { name: 'Morgan' })).toBeTruthy());
  });
});
