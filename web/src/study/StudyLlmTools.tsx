import { useEffect, useRef, useState, type FormEvent } from 'react';
import { ApiError } from '../api/client';
import { addNoteAlternative, askAboutNote, checkAnswerSynonym, suggestMnemonics, type CheckAnswerSynonymResult } from '../api/llm';
import type { QueueCard } from '../api/study';
import { t } from '../i18n/en';
import { Button } from '../ui/Button';

function errorMessage(error: unknown): string {
  return error instanceof ApiError && error.serverMessage ? error.serverMessage : t('requestFailed');
}

export function StudyLlmTools({
  card,
  enabled,
  answer,
  onSynonymAccepted,
}: {
  card: QueueCard;
  enabled: boolean;
  answer?: string;
  onSynonymAccepted: () => void;
}) {
  const [askOpen, setAskOpen] = useState(false);
  const [question, setQuestion] = useState('');
  const [assistantAnswer, setAssistantAnswer] = useState('');
  const [askError, setAskError] = useState<unknown>(null);
  const [asking, setAsking] = useState(false);
  const [mnemonics, setMnemonics] = useState<string[]>([]);
  const [mnemonicError, setMnemonicError] = useState<unknown>(null);
  const [loadingMnemonics, setLoadingMnemonics] = useState(false);
  const [synonym, setSynonym] = useState<CheckAnswerSynonymResult | null>(null);
  const [synonymError, setSynonymError] = useState<unknown>(null);
  const [checkingSynonym, setCheckingSynonym] = useState(false);
  const [savingSynonym, setSavingSynonym] = useState(false);
  const [synonymSaved, setSynonymSaved] = useState(false);
  const abortController = useRef<AbortController | null>(null);

  useEffect(() => () => {
    abortController.current?.abort();
    abortController.current = null;
  }, []);
  useEffect(() => {
    setSynonym(null);
    setSynonymError(null);
    setSynonymSaved(false);
  }, [answer]);

  if (!enabled) return null;

  async function ask(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const cleanQuestion = question.trim();
    if (!cleanQuestion || asking) return;
    abortController.current?.abort();
    const controller = new AbortController();
    abortController.current = controller;
    setAssistantAnswer('');
    setAskError(null);
    setAsking(true);
    try {
      await askAboutNote({ note_id: card.note_id, question: cleanQuestion }, (token) => {
        setAssistantAnswer((current) => current + token);
      }, controller.signal);
    } catch (caught) {
      if (!controller.signal.aborted) setAskError(caught);
    } finally {
      if (abortController.current === controller) {
        abortController.current = null;
        setAsking(false);
      }
    }
  }

  async function suggestMnemonic() {
    if (loadingMnemonics) return;
    setLoadingMnemonics(true);
    setMnemonicError(null);
    setMnemonics([]);
    try {
      const result = await suggestMnemonics(card.note_id);
      setMnemonics(result.mnemonics);
    } catch (caught) {
      setMnemonicError(caught);
    } finally {
      setLoadingMnemonics(false);
    }
  }

  async function checkSynonym() {
    if (!answer?.trim() || checkingSynonym || card.direction !== 'forward') return;
    setCheckingSynonym(true);
    setSynonymError(null);
    setSynonym(null);
    setSynonymSaved(false);
    try {
      setSynonym(await checkAnswerSynonym(card.id, answer.trim()));
    } catch (caught) {
      setSynonymError(caught);
    } finally {
      setCheckingSynonym(false);
    }
  }

  async function acceptSynonym() {
    if (!synonym?.is_synonym || !answer?.trim() || card.deck_id === undefined || savingSynonym) return;
    setSavingSynonym(true);
    setSynonymError(null);
    try {
      await addNoteAlternative(card.note_id, answer);
      setSynonymSaved(true);
      onSynonymAccepted();
    } catch (caught) {
      setSynonymError(caught);
    } finally {
      setSavingSynonym(false);
    }
  }

  return (
    <section className="grid gap-3 rounded-2xl bg-surface-container-low p-4" aria-label={t('askAssistant')}>
      <div className="flex flex-wrap gap-2">
        <Button type="button" variant="tonal" onClick={() => setAskOpen((open) => !open)}>{t('askAboutThisWord')}</Button>
        <Button type="button" variant="tonal" onClick={() => void suggestMnemonic()} disabled={loadingMnemonics}>
          {t(loadingMnemonics ? 'suggestingMnemonic' : 'suggestMnemonic')}
        </Button>
      </div>

      {askOpen && (
        <form onSubmit={(event) => void ask(event)} className="grid gap-3" aria-label={t('askAssistant')}>
          <p className="m-0 text-sm text-on-surface-variant">{t('askAssistantHint')}</p>
          <label htmlFor={`llm-question-${card.id}`} className="grid gap-2 text-sm font-medium text-on-surface">
            {t('askQuestion')}
            <input id={`llm-question-${card.id}`} value={question} maxLength={1000} onChange={(event) => setQuestion(event.target.value)}
              className="min-h-12 rounded-xl border border-outline bg-surface-container-lowest px-4 text-base font-normal text-on-surface focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary" />
          </label>
          <div className="flex flex-wrap gap-2">
            <Button type="submit" disabled={asking || !question.trim()}>{t(asking ? 'askingAssistant' : 'sendQuestion')}</Button>
            {asking && <Button type="button" variant="tonal" onClick={() => abortController.current?.abort()}>{t('stopGenerating')}</Button>}
          </div>
          {askError !== null && <p role="alert" className="m-0 text-error">{errorMessage(askError)}</p>}
          {(asking || assistantAnswer) && (
            <div className="grid gap-1 rounded-xl bg-surface-container p-3" aria-label={t('assistantAnswer')}>
              <h3 className="m-0 text-sm font-semibold text-on-surface">{t('assistantAnswer')}</h3>
              <p role="status" className="m-0 whitespace-pre-wrap break-words text-on-surface">{assistantAnswer || t('askingAssistant')}</p>
            </div>
          )}
        </form>
      )}

      {mnemonicError !== null && <p role="alert" className="m-0 text-error">{errorMessage(mnemonicError)}</p>}
      {mnemonics.length > 0 && (
        <div className="grid gap-2 rounded-xl bg-surface-container p-3" aria-label={t('mnemonicSuggestions')}>
          <h3 className="m-0 text-sm font-semibold text-on-surface">{t('mnemonicSuggestions')}</h3>
          <p className="m-0 text-sm text-on-surface-variant">{t('mnemonicSuggestionHint')}</p>
          <ul className="m-0 list-disc space-y-1 ps-5 text-on-surface">
            {mnemonics.map((mnemonic, index) => <li key={`${index}-${mnemonic}`}>{mnemonic}</li>)}
          </ul>
        </div>
      )}

      {card.direction === 'forward' && answer?.trim() && (
        <div className="grid gap-2 border-t border-outline-variant pt-3">
          <Button type="button" variant="tonal" onClick={() => void checkSynonym()} disabled={checkingSynonym || synonymSaved}>
            {t(checkingSynonym ? 'checkingSynonym' : 'checkSynonym')}
          </Button>
          {synonymError !== null && <p role="alert" className="m-0 text-error">{errorMessage(synonymError)}</p>}
          {synonym && (
            <div className="grid gap-2 rounded-xl bg-surface-container p-3">
              <p role="status" className="m-0 font-medium text-on-surface">{t(synonym.is_synonym ? 'synonymYes' : 'synonymNo')}</p>
              <p className="m-0 text-sm text-on-surface-variant">{synonym.explanation}</p>
              {synonym.is_synonym && !synonymSaved && <Button type="button" onClick={() => void acceptSynonym()} disabled={savingSynonym || card.deck_id === undefined}>
                {t(savingSynonym ? 'acceptingSynonym' : 'acceptSynonym')}
              </Button>}
              {synonymSaved && <p role="status" className="m-0 text-sm text-on-surface-variant">{t('synonymSaved')}</p>}
              {synonym.is_synonym && <p className="m-0 text-sm text-on-surface-variant">{t('synonymRatingHint')}</p>}
            </div>
          )}
        </div>
      )}
    </section>
  );
}
