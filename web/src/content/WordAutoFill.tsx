import { useEffect, useRef, useState } from 'react';
import { ApiError } from '../api/client';
import { enrichWord, type EnrichedWord } from '../api/llm';
import { t } from '../i18n/en';
import { Button } from '../ui/Button';

type SuggestionKey = 'translation' | 'alternatives' | 'transcription' | 'example' | 'example_translation'
  | 'fields.pos' | 'fields.article' | 'fields.plural' | 'fields.verb_forms' | 'fields.cefr';

interface SuggestionEntry {
  key: SuggestionKey;
  label: string;
  value: string | string[];
  displayValue: string;
}

const suggestionLabels: Record<SuggestionKey, string> = {
  translation: 'translation', alternatives: 'alternatives', transcription: 'transcription', example: 'example',
  example_translation: 'exampleTranslation', 'fields.pos': 'partOfSpeech', 'fields.article': 'grammaticalArticle',
  'fields.plural': 'pluralForm', 'fields.verb_forms': 'verbForms', 'fields.cefr': 'cefrLevel',
};

function errorMessage(error: unknown): string {
  return error instanceof ApiError && error.serverMessage ? error.serverMessage : t('requestFailed');
}

function cleanStrings(values: string[]): string[] {
  return values.map((value) => value.trim()).filter(Boolean);
}

function entriesFor(suggestion: EnrichedWord): SuggestionEntry[] {
  const values: Array<[SuggestionKey, string | string[]]> = [
    ['translation', suggestion.translation], ['alternatives', suggestion.alternatives],
    ['transcription', suggestion.transcription], ['example', suggestion.example],
    ['example_translation', suggestion.example_translation], ['fields.pos', suggestion.fields.pos],
    ['fields.article', suggestion.fields.article], ['fields.plural', suggestion.fields.plural],
    ['fields.verb_forms', suggestion.fields.verb_forms], ['fields.cefr', suggestion.fields.cefr],
  ];
  return values.flatMap(([key, rawValue]) => {
    const value = typeof rawValue === 'string' ? rawValue.trim() : cleanStrings(rawValue);
    if (typeof value === 'string' ? value.length === 0 : value.length === 0) return [];
    return [{
      key,
      label: t(suggestionLabels[key] as keyof typeof import('../i18n/en').en),
      value,
      displayValue: Array.isArray(value) ? value.join(key === 'alternatives' ? '; ' : ', ') : value,
    }];
  });
}

export function WordAutoFill({
  deckId,
  word,
  enabled,
  disabled,
  onUse,
}: {
  deckId: number;
  word: string;
  enabled: boolean;
  disabled?: boolean;
  onUse: (key: SuggestionKey, value: string | string[]) => void;
}) {
  const [suggestion, setSuggestion] = useState<EnrichedWord | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [pending, setPending] = useState(false);
  const [used, setUsed] = useState<Partial<Record<SuggestionKey, boolean>>>({});
  const requestID = useRef(0);
  const normalizedWord = word.trim();

  useEffect(() => {
    requestID.current += 1;
    setSuggestion(null);
    setError(null);
    setPending(false);
    setUsed({});
  }, [deckId, normalizedWord]);

  if (!enabled) return null;

  const entries = suggestion ? entriesFor(suggestion) : [];
  async function requestSuggestion() {
    if (!normalizedWord || pending || disabled) return;
    const currentID = ++requestID.current;
    setPending(true);
    setError(null);
    try {
      const result = await enrichWord({ deck_id: deckId, word: normalizedWord });
      if (requestID.current === currentID) {
        setSuggestion(result);
        setUsed({});
      }
    } catch (caught) {
      if (requestID.current === currentID) setError(caught);
    } finally {
      if (requestID.current === currentID) setPending(false);
    }
  }

  function accept(entry: SuggestionEntry) {
    onUse(entry.key, entry.value);
    setUsed((previous) => ({ ...previous, [entry.key]: true }));
  }

  return (
    <section aria-labelledby="word-fill-heading" aria-busy={pending} className="space-y-3 rounded-2xl bg-surface-container p-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h4 id="word-fill-heading" className="m-0 text-base font-semibold text-on-surface">{t('wordFill')}</h4>
        <Button type="button" variant="tonal" onClick={() => void requestSuggestion()} disabled={!normalizedWord || pending || disabled}>
          {t(pending ? 'fillingWord' : 'fillWord')}
        </Button>
      </div>
      <p className="m-0 text-sm text-on-surface-variant">{t('wordFillHint')}</p>
      {pending && <p role="status" className="m-0 text-sm text-on-surface-variant">{t('fillingWord')}</p>}
      {error !== null && <p role="alert" className="m-0 text-error">{errorMessage(error)}</p>}
      {suggestion && entries.length > 0 && (
        <section className="space-y-3 border-t border-outline-variant pt-3" aria-labelledby="word-suggestions-heading">
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div>
              <h5 id="word-suggestions-heading" className="m-0 font-medium text-on-surface">{t('wordSuggestions')}</h5>
              <p className="m-0 mt-1 text-sm text-on-surface-variant">{t('suggestionsNotSaved')}</p>
            </div>
            <Button type="button" variant="tonal" onClick={() => entries.forEach(accept)} disabled={entries.every((entry) => used[entry.key])}>
              {t('useAllSuggestions')}
            </Button>
          </div>
          <ul className="m-0 list-none space-y-2 p-0" aria-label={t('wordSuggestions')}>
            {entries.map((entry) => (
              <li key={entry.key} className="flex flex-wrap items-start justify-between gap-3 rounded-xl bg-surface-container-lowest p-3">
                <div className="min-w-0 flex-1">
                  <span className="block text-sm font-medium text-on-surface-variant">{entry.label}</span>
                  <p className="m-0 break-words text-on-surface">{entry.displayValue}</p>
                </div>
                <Button type="button" variant="tonal" onClick={() => accept(entry)} disabled={used[entry.key]}>
                  {t(used[entry.key] ? 'suggestionAdded' : 'useSuggestion').replace('{field}', entry.label)}
                </Button>
              </li>
            ))}
          </ul>
        </section>
      )}
    </section>
  );
}

export type { SuggestionKey };
