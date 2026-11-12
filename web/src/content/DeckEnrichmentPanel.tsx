import { useEffect, useState } from 'react';
import type { ApplyEnrichmentRequest, DeckId } from '../api/content';
import type { DeckEnrichmentItem, JobStatus } from '../api/jobs';
import { t, type TranslationKey } from '../i18n/en';
import { Button } from '../ui/Button';
import { useApplyNoteEnrichment, useJobStatus, useStartDeckEnrichment } from './hooks';

type FieldKey = 'transcription' | 'example' | 'example_translation' | 'alternatives'
  | 'fields.pos' | 'fields.article' | 'fields.plural' | 'fields.verb_forms' | 'fields.cefr';

interface EnrichmentEntry {
  key: FieldKey;
  missingField: string;
  label: TranslationKey;
  fieldName: string;
  value: string | string[];
  displayValue: string;
}

interface TrackedJob {
  jobId: string;
  deckId: string;
  deckName: string;
}

interface ApplySummary {
  applied: string[];
  skipped: string[];
}

const storagePrefix = 'retain:deck-enrichment-jobs:v1:';
const fields: Array<{ key: FieldKey; missing: string; label: TranslationKey; name: string }> = [
  { key: 'transcription', missing: 'transcription', label: 'transcription', name: 'transcription' },
  { key: 'example', missing: 'example', label: 'example', name: 'example' },
  { key: 'example_translation', missing: 'example_translation', label: 'exampleTranslation', name: 'example_translation' },
  { key: 'alternatives', missing: 'alternatives', label: 'alternatives', name: 'alternatives' },
  { key: 'fields.pos', missing: 'fields.pos', label: 'partOfSpeech', name: 'pos' },
  { key: 'fields.article', missing: 'fields.article', label: 'grammaticalArticle', name: 'article' },
  { key: 'fields.plural', missing: 'fields.plural', label: 'pluralForm', name: 'plural' },
  { key: 'fields.verb_forms', missing: 'fields.verb_forms', label: 'verbForms', name: 'verb_forms' },
  { key: 'fields.cefr', missing: 'fields.cefr', label: 'cefrLevel', name: 'cefr' },
];

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function readJobs(ownerId: number): TrackedJob[] {
  try {
    const parsed: unknown = JSON.parse(window.localStorage.getItem(`${storagePrefix}${ownerId}`) ?? '[]');
    if (!Array.isArray(parsed)) return [];
    return parsed.filter((item): item is TrackedJob => isRecord(item)
      && typeof item.jobId === 'string' && item.jobId.length > 0
      && typeof item.deckId === 'string' && typeof item.deckName === 'string').slice(-20);
  } catch {
    return [];
  }
}

function entriesFor(item: DeckEnrichmentItem): EnrichmentEntry[] {
  const suggestions: unknown = item.suggestions;
  if (!isRecord(suggestions)) return [];
  const suggestedFields: unknown = suggestions.fields;
  if (!isRecord(suggestedFields)) return [];
  const result: EnrichmentEntry[] = [];
  for (const field of fields) {
    if (!item.missing_fields.includes(field.missing)) continue;
    const source = field.key.startsWith('fields.') ? suggestedFields : suggestions;
    const raw = source[field.name];
    if (typeof raw === 'string' && raw.trim()) {
      result.push({
        key: field.key, missingField: field.missing, label: field.label, fieldName: field.name,
        value: raw.trim(), displayValue: raw.trim(),
      });
      continue;
    }
    if (Array.isArray(raw) && raw.every((value) => typeof value === 'string')) {
      const values = raw.map((value) => value.trim()).filter(Boolean);
      if (values.length > 0) result.push({
        key: field.key, missingField: field.missing, label: field.label, fieldName: field.name,
        value: values, displayValue: values.join(field.key === 'alternatives' ? '; ' : ', '),
      });
    }
  }
  return result;
}

function requestFor(entries: EnrichmentEntry[]): ApplyEnrichmentRequest {
  const result: ApplyEnrichmentRequest = {};
  const metadata: Record<string, unknown> = {};
  for (const entry of entries) {
    switch (entry.key) {
      case 'fields.pos':
      case 'fields.article':
      case 'fields.plural':
      case 'fields.verb_forms':
      case 'fields.cefr':
        metadata[entry.fieldName] = entry.value;
        break;
      case 'alternatives':
        result.alternatives = entry.value as string[];
        break;
      case 'transcription':
        result.transcription = entry.value as string;
        break;
      case 'example':
        result.example = entry.value as string;
        break;
      case 'example_translation':
        result.example_translation = entry.value as string;
        break;
    }
  }
  if (Object.keys(metadata).length > 0) result.fields = metadata;
  return result;
}

function statusText(job: JobStatus): string {
  if (job.status === 'queued') return t('enrichmentQueued');
  if (job.status === 'running') return t('enrichmentProgress').replace('{progress}', String(job.progress)).replace('{total}', String(job.total));
  return t(job.status === 'done' ? 'enrichmentCompleted' : 'enrichmentFailed');
}

export function DeckEnrichmentPanel({ ownerId, deckId, deckName, enabled }: {
  ownerId: number;
  deckId: DeckId;
  deckName: string;
  enabled: boolean;
}) {
  return <DeckEnrichmentPanelForOwner key={ownerId} ownerId={ownerId} deckId={deckId} deckName={deckName} enabled={enabled} />;
}

function DeckEnrichmentPanelForOwner({ ownerId, deckId, deckName, enabled }: {
  ownerId: number;
  deckId: DeckId;
  deckName: string;
  enabled: boolean;
}) {
  const [jobs, setJobs] = useState<TrackedJob[]>(() => readJobs(ownerId));
  const [selectedJobId, setSelectedJobId] = useState<string | null>(() => readJobs(ownerId).at(-1)?.jobId ?? null);
  const [startError, setStartError] = useState(false);
  const [selectedFields, setSelectedFields] = useState<Record<string, Partial<Record<FieldKey, boolean>>>>({});
  const [applyErrors, setApplyErrors] = useState<Record<string, boolean>>({});
  const [applyResults, setApplyResults] = useState<Record<string, ApplySummary>>({});
  const startMutation = useStartDeckEnrichment();
  const applyMutation = useApplyNoteEnrichment();
  const selectedJob = jobs.find((job) => job.jobId === selectedJobId);
  const jobQuery = useJobStatus(ownerId, selectedJobId, selectedJob !== undefined);
  const job = jobQuery.data;
  const isActive = job?.status === 'queued' || job?.status === 'running';
  const canStart = enabled && !startMutation.isPending && !isActive;

  useEffect(() => {
    try {
      window.localStorage.setItem(`${storagePrefix}${ownerId}`, JSON.stringify(jobs.slice(-20)));
    } catch {
      // The job can still be used until this panel is unmounted if storage is unavailable.
    }
  }, [jobs, ownerId]);

  async function start() {
    if (!canStart) return;
    setStartError(false);
    setSelectedFields({});
    setApplyErrors({});
    setApplyResults({});
    try {
      const result = await startMutation.mutateAsync(deckId);
      const tracked = { jobId: result.job_id, deckId: String(deckId), deckName };
      setJobs((current) => [...current.filter((item) => item.jobId !== tracked.jobId), tracked].slice(-20));
      setSelectedJobId(tracked.jobId);
    } catch {
      setStartError(true);
    }
  }

  function toggle(noteId: string, key: FieldKey, checked: boolean) {
    setSelectedFields((previous) => ({ ...previous, [noteId]: { ...previous[noteId], [key]: checked } }));
    setApplyErrors((previous) => ({ ...previous, [noteId]: false }));
    setApplyResults((previous) => {
      const next = { ...previous };
      delete next[noteId];
      return next;
    });
  }

  async function apply(item: DeckEnrichmentItem, entries: EnrichmentEntry[]) {
    if (!selectedJob || applyMutation.isPending) return;
    const noteId = String(item.note_id);
    const selected = entries.filter((entry) => selectedFields[noteId]?.[entry.key]);
    if (selected.length === 0) return;
    try {
      const result = await applyMutation.mutateAsync({
        deckId: selectedJob.deckId,
        id: item.note_id,
        input: requestFor(selected),
      });
      const applied = selected.filter((entry) => result.applied_fields.includes(entry.key)
        || result.applied_fields.includes(entry.missingField)
        || (entry.key.startsWith('fields.') && result.applied_fields.includes('fields')));
      setApplyResults((previous) => ({
        ...previous,
        [noteId]: {
          applied: applied.map((entry) => t(entry.label)),
          skipped: selected.filter((entry) => !applied.includes(entry)).map((entry) => t(entry.label)),
        },
      }));
      setSelectedFields((previous) => ({ ...previous, [noteId]: {} }));
    } catch {
      setApplyErrors((previous) => ({ ...previous, [noteId]: true }));
    }
  }

  if (!enabled && jobs.length === 0) return null;

  return (
    <section className="space-y-3 rounded-2xl bg-surface-container p-4" aria-labelledby="deck-enrichment-heading" aria-busy={startMutation.isPending || isActive}>
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h3 id="deck-enrichment-heading" className="m-0 font-semibold text-on-surface">{t('deckEnrichment')}</h3>
          <p className="m-0 mt-1 text-sm text-on-surface-variant">{t('deckEnrichmentHint')}</p>
        </div>
        {enabled && <Button type="button" variant="tonal" disabled={!canStart} onClick={() => void start()}>
          {t(startMutation.isPending ? 'startingDeckEnrichment' : jobs.some((item) => item.deckId === String(deckId)) ? 'enrichDeckAgain' : 'enrichDeck')}
        </Button>}
      </div>
      {startError && <p role="alert" className="m-0 text-error">{t('deckEnrichmentStartFailed')}</p>}
      {jobs.length > 0 && (
        <label className="grid gap-2 text-sm font-medium text-on-surface" htmlFor="tracked-enrichment-job">
          {t('enrichmentProgressLabel')}
          <select id="tracked-enrichment-job" value={selectedJobId ?? ''} onChange={(event) => {
            setSelectedJobId(event.target.value);
            setSelectedFields({});
            setApplyErrors({});
            setApplyResults({});
          }} className="min-h-12 rounded-xl border border-outline bg-surface-container-lowest px-4 text-base font-normal text-on-surface focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary">
            {jobs.slice().reverse().map((item) => <option key={item.jobId} value={item.jobId}>{item.deckName} — {item.jobId}</option>)}
          </select>
        </label>
      )}
      {selectedJob && jobQuery.isPending && <p role="status" className="m-0 text-sm text-on-surface-variant">{t('loadingEnrichmentStatus')}</p>}
      {selectedJob && jobQuery.isError && <div role="alert" className="space-y-2">
        <p className="m-0 text-error">{t('enrichmentStatusFailed')}</p>
        <Button type="button" variant="tonal" onClick={() => void jobQuery.refetch()}>{t('tryAgain')}</Button>
      </div>}
      {job && <p role={job.status === 'failed' ? 'alert' : 'status'} className={`m-0 text-sm ${job.status === 'failed' ? 'text-error' : 'text-on-surface-variant'}`}>
        {selectedJob?.deckName}: {statusText(job)}
      </p>}
      {isActive && job && <progress aria-label={t('enrichmentProgressLabel')} value={job.progress} max={Math.max(job.total, 1)} className="h-2 w-full accent-primary" />}
      {job?.status === 'done' && job.result && (
        <div className="space-y-3 border-t border-outline-variant pt-3">
          {job.result.partial && <p className="m-0 rounded-xl bg-surface-container-high p-3 text-sm text-on-surface-variant">
            {t(job.result.omitted_count === 1 ? 'enrichmentPartialSingular' : 'enrichmentPartialPlural').replace('{count}', String(job.result.omitted_count))}
          </p>}
          <p className="m-0 text-sm text-on-surface-variant">{t('enrichmentExplicitReviewHint')}</p>
          {job.result.items.length === 0 ? <p className="m-0 text-on-surface-variant">{t('noEnrichmentProposals')}</p> : (
            <ul className="m-0 list-none space-y-3 p-0" aria-label={t('enrichmentProposals')}>
              {job.result.items.map((item) => {
                const noteId = String(item.note_id);
                const entries = entriesFor(item);
                const selected = entries.filter((entry) => selectedFields[noteId]?.[entry.key]);
                const summary = applyResults[noteId];
                return <li key={noteId} className="space-y-3 rounded-xl bg-surface-container-high p-4">
                  <h4 className="m-0 break-words font-medium text-on-surface">{item.term}</h4>
                  {entries.length === 0 ? <p className="m-0 text-sm text-on-surface-variant">{t('noSupportedEnrichmentFields')}</p> : (
                    <ul className="m-0 list-none space-y-2 p-0">
                      {entries.map((entry) => {
                        const id = `enrichment-${noteId}-${entry.key}`;
                        return <li key={entry.key}>
                          <label htmlFor={id} className="flex min-h-12 cursor-pointer items-start gap-3 rounded-xl bg-surface-container-lowest p-3">
                            <input id={id} type="checkbox" checked={selectedFields[noteId]?.[entry.key] === true}
                              onChange={(event) => toggle(noteId, entry.key, event.target.checked)} disabled={applyMutation.isPending}
                              className="mt-1 size-5 shrink-0 accent-primary" />
                            <span className="min-w-0">
                              <span className="block text-sm font-medium text-on-surface-variant">{t(entry.label)}</span>
                              <span className="block break-words text-on-surface">{entry.displayValue}</span>
                            </span>
                          </label>
                        </li>;
                      })}
                    </ul>
                  )}
                  {applyErrors[noteId] && <p role="alert" className="m-0 text-sm text-error">{t('enrichmentApplyFailed')}</p>}
                  {!!summary?.applied.length && <p role="status" className="m-0 text-sm text-on-surface-variant">{t('enrichmentFieldsApplied').replace('{fields}', summary.applied.join(', '))}</p>}
                  {!!summary?.skipped.length && <p role="status" className="m-0 text-sm text-on-surface-variant">{t('enrichmentFieldsSkipped').replace('{fields}', summary.skipped.join(', '))}</p>}
                  <Button type="button" variant="tonal" onClick={() => void apply(item, entries)} disabled={selected.length === 0 || applyMutation.isPending}>
                    {t(applyMutation.isPending ? 'applyingEnrichmentFields' : 'applySelectedEnrichmentFields')}
                  </Button>
                </li>;
              })}
            </ul>
          )}
        </div>
      )}
    </section>
  );
}
