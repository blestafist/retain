// @vitest-environment jsdom
import { cleanup, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { t } from '../i18n/en';
import type { QueueCard } from '../api/study';
import { StudyLlmTools } from './StudyLlmTools';

const card = {
  id: 5,
  note_id: 8,
  deck_id: 2,
  direction: 'forward',
  state: 1,
  due: '2025-01-01T00:00:00Z',
  elapsed_days: 1,
  scheduled_days: 1,
  step: 0,
  reps: 1,
  lapses: 0,
  front: 'hello',
  back: 'cześć',
  previews: {
    1: { state: 1, due: '2025-01-02T00:00:00Z', scheduled_days: 1, interval: '1d' },
    2: { state: 1, due: '2025-01-02T00:00:00Z', scheduled_days: 1, interval: '1d' },
    3: { state: 1, due: '2025-01-02T00:00:00Z', scheduled_days: 1, interval: '1d' },
    4: { state: 1, due: '2025-01-02T00:00:00Z', scheduled_days: 1, interval: '1d' },
  },
} satisfies QueueCard;

function jsonResponse(value: unknown) {
  return new Response(JSON.stringify(value), { headers: { 'Content-Type': 'application/json' } });
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('study LLM tools', () => {
  it('streams word explanations and shows mnemonic suggestions without saving them', async () => {
    const user = userEvent.setup();
    const fetchMock = vi.fn<typeof fetch>((input) => {
      const url = String(input);
      if (url === '/api/v1/llm/ask') {
        return Promise.resolve(new Response('event: token\ndata: {"text":"A greeting"}\n\nevent: done\ndata: {}\n\n', {
          headers: { 'Content-Type': 'text/event-stream' },
        }));
      }
      if (url === '/api/v1/llm/mnemonic') return Promise.resolve(jsonResponse({ mnemonics: ['Idea one', 'Idea two'] }));
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    render(<StudyLlmTools card={card} enabled onSynonymAccepted={() => undefined} />);

    await user.click(screen.getByRole('button', { name: t('askAboutThisWord') }));
    await user.type(screen.getByRole('textbox', { name: t('askQuestion') }), 'When is it used?');
    await user.click(screen.getByRole('button', { name: t('sendQuestion') }));
    expect(await screen.findByText('A greeting')).toBeTruthy();
    await user.click(screen.getByRole('button', { name: t('suggestMnemonic') }));
    expect(await screen.findByText('Idea one')).toBeTruthy();
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toEqual(['/api/v1/llm/ask', '/api/v1/llm/mnemonic']);
  });

  it('requires explicit acceptance before saving a forward-card synonym', async () => {
    const user = userEvent.setup();
    const fetchMock = vi.fn<typeof fetch>((input, init) => {
      const url = String(input);
      if (url === '/api/v1/llm/check-answer') return Promise.resolve(jsonResponse({ is_synonym: true, explanation: 'A natural equivalent.' }));
      if (url === '/api/v1/notes/8/alternatives' && init?.method === 'POST') return Promise.resolve(jsonResponse({ id: 8 }));
      throw new Error(`Unexpected request: ${init?.method ?? 'GET'} ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const onSynonymAccepted = vi.fn();
    render(<StudyLlmTools card={card} enabled answer="witaj" onSynonymAccepted={onSynonymAccepted} />);

    await user.click(screen.getByRole('button', { name: t('checkSynonym') }));
    expect(await screen.findByText(t('synonymYes'))).toBeTruthy();
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toEqual(['/api/v1/llm/check-answer']);
    await user.click(screen.getByRole('button', { name: t('acceptSynonym') }));
    expect(await screen.findByText(t('synonymSaved'))).toBeTruthy();
    const appendCall = fetchMock.mock.calls.find(([input, init]) => String(input) === '/api/v1/notes/8/alternatives' && init?.method === 'POST');
    expect(JSON.parse(String(appendCall?.[1]?.body))).toEqual({ alternative: 'witaj' });
    expect(onSynonymAccepted).toHaveBeenCalledOnce();
  });

  it('does not offer synonym checks for reverse cards', () => {
    render(<StudyLlmTools card={{ ...card, direction: 'reverse' }} enabled answer="hello" onSynonymAccepted={() => undefined} />);
    expect(screen.queryByRole('button', { name: t('checkSynonym') })).toBeNull();
  });
});
