import { useState, type ChangeEvent, type FormEvent } from 'react';
import { ApiError } from '../api/client';
import { downloadExport, type ImportPreviewDeck } from '../api/import';
import type { DeckNode, NativeLanguage, TargetLanguage } from '../api/content';
import { t } from '../i18n/en';
import { Button } from '../ui/Button';
import { useApplyImport, useExportDeck, useImportHistory, usePreviewImport } from './hooks';

interface ImportPanelProps {
  decks: DeckNode[];
  selectedDeck?: DeckNode;
}

function flattenDecks(decks: DeckNode[], depth = 0): Array<{ deck: DeckNode; depth: number }> {
  return decks.flatMap((deck) => [
    { deck, depth },
    ...flattenDecks(deck.children, depth + 1),
  ]);
}

function errorMessage(error: unknown, fallback = t('requestFailed')): string {
  return error instanceof ApiError && error.serverMessage ? error.serverMessage : fallback;
}

function countsLabel(counts: { new: number; changed: number; unchanged: number }): string {
  return `${t('newCount')} ${counts.new} · ${t('changedCount')} ${counts.changed} · ${t('unchangedCount')} ${counts.unchanged}`;
}

function statusLabel(status: ImportPreviewDeck['status']): string {
  return t(status === 'new' ? 'importStatusNew' : status === 'changed' ? 'importStatusChanged' : 'importStatusUnchanged');
}

function historyStatusLabel(status: string): string {
  if (status === 'previewed') return t('importStatusPreviewed');
  if (status === 'applied') return t('importStatusApplied');
  if (status === 'failed') return t('importStatusFailed');
  return status;
}

function PreviewTree({ decks }: { decks: ImportPreviewDeck[] }) {
  return (
    <ul className="m-0 list-none space-y-2 p-0" aria-label={t('importTree')}>
      {decks.map((deck, index) => (
        <li key={deck.external_id ?? `${deck.name}-${index}`}>
          <div className="rounded-2xl bg-surface-container p-3">
            <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
              <span className="break-words font-medium text-on-surface">{deck.name}</span>
              <span className="text-sm text-on-surface-variant">{statusLabel(deck.status)}</span>
            </div>
            <p className="m-0 mt-1 text-sm text-on-surface-variant">{countsLabel(deck.counts)}</p>
          </div>
          {deck.decks.length > 0 && (
            <div className="ms-3 mt-2 border-s border-outline-variant ps-3">
              <PreviewTree decks={deck.decks} />
            </div>
          )}
        </li>
      ))}
    </ul>
  );
}

function formatDate(value: string): string {
  return new Intl.DateTimeFormat('en', { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value));
}

export function ImportPanel({ decks, selectedDeck }: ImportPanelProps) {
  const [file, setFile] = useState<File | null>(null);
  const [fileInputKey, setFileInputKey] = useState(0);
  const [fileError, setFileError] = useState<'required' | 'type' | null>(null);
  const [targetLanguage, setTargetLanguage] = useState<TargetLanguage>('de');
  const [nativeLanguage, setNativeLanguage] = useState<NativeLanguage>('pl');
  const [parentDeckId, setParentDeckId] = useState('');
  const [onMissing, setOnMissing] = useState<'keep' | 'suspend'>('keep');
  const [confirming, setConfirming] = useState(false);
  const [applied, setApplied] = useState(false);
  const previewMutation = usePreviewImport();
  const applyMutation = useApplyImport();
  const historyQuery = useImportHistory();
  const exportMutation = useExportDeck();
  const preview = previewMutation.data;
  const deckOptions = flattenDecks(decks);

  async function submitPreview(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!file) {
      setFileError('required');
      return;
    }
    setFileError(null);
    setConfirming(false);
    setApplied(false);
    applyMutation.reset();
    try {
      await previewMutation.mutateAsync(file);
    } catch {
      // Mutation state renders the request error beside the form.
    }
  }

  function selectFile(event: ChangeEvent<HTMLInputElement>) {
    const selected = event.currentTarget.files?.[0] ?? null;
    if (selected && !/\.(json|csv)$/i.test(selected.name)) {
      setFile(null);
      setFileError('type');
      event.currentTarget.value = '';
      previewMutation.reset();
      return;
    }
    setFile(selected);
    setFileError(null);
    setConfirming(false);
    setApplied(false);
    previewMutation.reset();
    applyMutation.reset();
  }

  async function confirmApply() {
    if (!preview) return;
    const parent = deckOptions.find(({ deck }) => String(deck.id) === parentDeckId)?.deck;
    try {
      await applyMutation.mutateAsync({
        batchId: preview.batch_id,
        input: {
          lang_target: targetLanguage,
          lang_native: nativeLanguage,
          ...(parent ? { parent_deck_id: parent.id } : {}),
          on_missing: onMissing,
        },
      });
      setApplied(true);
      setConfirming(false);
      previewMutation.reset();
      setFile(null);
      setFileInputKey((current) => current + 1);
    } catch {
      // Mutation state renders the request error in the preview panel.
    }
  }

  async function startExport(format: 'retain' | 'csv') {
    if (!selectedDeck) return;
    try {
      const download = await exportMutation.mutateAsync({ deckId: selectedDeck.id, format });
      downloadExport(download);
    } catch {
      // Mutation state renders the request error beside the export controls.
    }
  }

  return (
    <section className="space-y-5 rounded-3xl bg-surface-container-low p-5 expanded:p-6" aria-labelledby="import-export-heading">
      <header>
        <p className="m-0 text-sm font-medium uppercase tracking-wide text-primary">{t('decks')}</p>
        <h2 id="import-export-heading" className="m-0 text-2xl font-semibold text-on-surface">{t('importExport')}</h2>
      </header>

      <section className="space-y-4" aria-labelledby="import-heading">
        <div>
          <h3 id="import-heading" className="m-0 text-lg font-semibold text-on-surface">{t('importSection')}</h3>
          <p className="m-0 mt-1 text-sm text-on-surface-variant">{t('importDescription')}</p>
        </div>

        <form onSubmit={(event) => void submitPreview(event)} className="space-y-4">
          <label className="grid gap-2 text-sm font-medium text-on-surface" htmlFor="import-file">
            {t('chooseImportFile')}
            <input
              id="import-file"
              key={fileInputKey}
              type="file"
              accept=".json,.csv,application/json,text/csv"
              disabled={previewMutation.isPending || applyMutation.isPending}
              onChange={selectFile}
              aria-invalid={fileError !== null || undefined}
              aria-describedby={`import-file-hint${fileError ? ' import-file-error' : ''}`}
              className="min-h-12 w-full rounded-xl border border-outline bg-surface-container-lowest px-3 py-2 text-base font-normal text-on-surface file:me-3 file:min-h-9 file:rounded-lg file:border-0 file:bg-secondary-container file:px-3 file:text-on-secondary-container focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary"
            />
          </label>
          <p id="import-file-hint" className="m-0 text-sm text-on-surface-variant">{t('supportedImportFormats')}</p>
          {fileError && <p id="import-file-error" role="alert" className="m-0 text-error">{t(fileError === 'type' ? 'importFileTypeError' : 'importFileRequired')}</p>}
          {previewMutation.isError && <p role="alert" className="m-0 text-error">{errorMessage(previewMutation.error)}</p>}
          <Button type="submit" disabled={!file || previewMutation.isPending}>
            {t(previewMutation.isPending ? 'previewingImport' : 'previewImport')}
          </Button>
        </form>

        {preview && (
          <section className="space-y-4 rounded-2xl border border-outline-variant bg-surface-container-lowest p-4" aria-labelledby="import-preview-heading">
            <div>
              <h4 id="import-preview-heading" className="m-0 text-lg font-semibold text-on-surface">{t('importPreview')}</h4>
              <p className="m-0 mt-1 break-words text-sm text-on-surface-variant">
                {t('detectedAdapter')}: {preview.adapter} · {t('importedSource')}: {preview.source}
              </p>
            </div>

            <dl className="m-0 grid gap-3 rounded-2xl bg-surface-container p-4 sm:grid-cols-2">
              <div>
                <dt className="text-sm font-medium text-on-surface">{t('importCounts')}</dt>
                <dd className="m-0 text-sm text-on-surface-variant">{countsLabel(preview.counts)}</dd>
              </div>
              <div>
                <dt className="text-sm font-medium text-on-surface">{t('importDeckCounts')}</dt>
                <dd className="m-0 text-sm text-on-surface-variant">{countsLabel(preview.deck_counts)}</dd>
              </div>
            </dl>

            <div className="space-y-2">
              <h5 className="m-0 text-base font-semibold text-on-surface">{t('importTree')}</h5>
              {preview.tree.length > 0 ? <PreviewTree decks={preview.tree} /> : <p className="m-0 text-sm text-on-surface-variant">{t('noImportDecks')}</p>}
            </div>

            <div className="space-y-2">
              <h5 className="m-0 text-base font-semibold text-on-surface">{t('importWarnings')}</h5>
              {preview.warnings.length > 0 ? (
                <ul className="m-0 list-disc space-y-1 rounded-2xl bg-tertiary-container p-4 ps-9 text-sm text-on-tertiary-container">
                  {preview.warnings.map((warning, index) => <li key={`${warning}-${index}`}>{warning}</li>)}
                </ul>
              ) : <p className="m-0 text-sm text-on-surface-variant">{t('importNoWarnings')}</p>}
            </div>

            <form onSubmit={(event: FormEvent<HTMLFormElement>) => { event.preventDefault(); setConfirming(true); }} className="space-y-4 border-t border-outline-variant pt-4">
              <div className="grid gap-4 sm:grid-cols-2">
                <label className="grid gap-2 text-sm font-medium text-on-surface" htmlFor="import-target-language">
                  {t('targetLanguage')}
                  <select id="import-target-language" value={targetLanguage} onChange={(event) => setTargetLanguage(event.currentTarget.value as TargetLanguage)} className="min-h-12 rounded-xl border border-outline bg-surface-container-lowest px-4 text-base font-normal text-on-surface focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary">
                    <option value="de">{t('german')}</option>
                    <option value="en">{t('english')}</option>
                  </select>
                </label>
                <label className="grid gap-2 text-sm font-medium text-on-surface" htmlFor="import-native-language">
                  {t('nativeLanguage')}
                  <select id="import-native-language" value={nativeLanguage} onChange={(event) => setNativeLanguage(event.currentTarget.value as NativeLanguage)} className="min-h-12 rounded-xl border border-outline bg-surface-container-lowest px-4 text-base font-normal text-on-surface focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary">
                    <option value="pl">{t('polish')}</option>
                    <option value="ru">{t('russian')}</option>
                  </select>
                </label>
                <label className="grid gap-2 text-sm font-medium text-on-surface sm:col-span-2" htmlFor="import-parent-deck">
                  {t('optionalParentDeck')}
                  <select id="import-parent-deck" value={parentDeckId} onChange={(event) => setParentDeckId(event.currentTarget.value)} className="min-h-12 rounded-xl border border-outline bg-surface-container-lowest px-4 text-base font-normal text-on-surface focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary">
                    <option value="">{t('noParentDeck')}</option>
                    {deckOptions.map(({ deck, depth }) => (
                      <option key={String(deck.id)} value={String(deck.id)}>{`${'↳ '.repeat(depth)}${deck.name}`}</option>
                    ))}
                  </select>
                </label>
                <label className="grid gap-2 text-sm font-medium text-on-surface sm:col-span-2" htmlFor="import-missing-policy">
                  {t('missingImportItems')}
                  <select id="import-missing-policy" value={onMissing} onChange={(event) => setOnMissing(event.currentTarget.value as 'keep' | 'suspend')} className="min-h-12 rounded-xl border border-outline bg-surface-container-lowest px-4 text-base font-normal text-on-surface focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary">
                    <option value="keep">{t('keepMissingItems')}</option>
                    <option value="suspend">{t('suspendMissingItems')}</option>
                  </select>
                </label>
              </div>
              {applyMutation.isError && <p role="alert" className="m-0 text-error">{errorMessage(applyMutation.error)}</p>}
              {!confirming ? (
                <Button type="submit">{t('reviewBeforeApply')}</Button>
              ) : (
                <div className="space-y-3 rounded-2xl bg-secondary-container p-4 text-on-secondary-container" role="group" aria-labelledby="confirm-import-heading">
                  <div>
                    <h5 id="confirm-import-heading" className="m-0 font-semibold">{t('confirmImportHeading')}</h5>
                    <p className="m-0 mt-1 text-sm">{t('confirmImportDescription')}</p>
                  </div>
                  <div className="flex flex-wrap gap-3">
                    <Button type="button" disabled={applyMutation.isPending} onClick={() => void confirmApply()}>
                      {t(applyMutation.isPending ? 'applyingImport' : 'confirmApplyImport')}
                    </Button>
                    <Button type="button" variant="tonal" disabled={applyMutation.isPending} onClick={() => setConfirming(false)}>{t('cancel')}</Button>
                  </div>
                </div>
              )}
            </form>
          </section>
        )}
        {applied && <p role="status" className="m-0 rounded-2xl bg-secondary-container p-4 text-on-secondary-container">{t('importApplied')}</p>}
      </section>

      <section className="space-y-3 border-t border-outline-variant pt-5" aria-labelledby="export-heading">
        <div>
          <h3 id="export-heading" className="m-0 text-lg font-semibold text-on-surface">{t('exportSelectedDeck')}</h3>
          <p className="m-0 mt-1 break-words text-sm text-on-surface-variant">
            {selectedDeck ? selectedDeck.name : t('exportRequiresDeck')}
          </p>
        </div>
        <div className="flex flex-wrap gap-3">
          <Button type="button" variant="tonal" disabled={!selectedDeck || exportMutation.isPending} onClick={() => void startExport('retain')}>
            {t(exportMutation.isPending ? 'exporting' : 'exportRetain')}
          </Button>
          <Button type="button" variant="tonal" disabled={!selectedDeck || exportMutation.isPending} onClick={() => void startExport('csv')}>
            {t(exportMutation.isPending ? 'exporting' : 'exportCsv')}
          </Button>
        </div>
        {exportMutation.isError && <p role="alert" className="m-0 text-error">{errorMessage(exportMutation.error)}</p>}
      </section>

      <section className="space-y-3 border-t border-outline-variant pt-5" aria-labelledby="import-history-heading">
        <h3 id="import-history-heading" className="m-0 text-lg font-semibold text-on-surface">{t('importHistory')}</h3>
        {historyQuery.isPending && <p role="status" className="m-0 text-on-surface-variant">{t('loadingImportHistory')}</p>}
        {historyQuery.isError && (
          <div role="alert" className="space-y-2 rounded-2xl bg-error-container p-4 text-on-error-container">
            <p className="m-0">{errorMessage(historyQuery.error, t('importHistoryError'))}</p>
            <Button type="button" variant="tonal" onClick={() => void historyQuery.refetch()}>{t('tryAgain')}</Button>
          </div>
        )}
        {historyQuery.isSuccess && (historyQuery.data.length > 0 ? (
          <ul className="m-0 divide-y divide-outline-variant rounded-2xl bg-surface-container p-0">
            {historyQuery.data.slice(0, 5).map((batch) => (
              <li key={batch.id} className="flex flex-wrap justify-between gap-2 px-4 py-3">
                <div className="min-w-0">
                  <p className="m-0 break-all font-medium text-on-surface">{batch.filename || batch.adapter}</p>
                  <p className="m-0 text-sm text-on-surface-variant">{batch.adapter} · {formatDate(batch.created_at)}</p>
                </div>
                <span className="self-center rounded-full bg-surface-container-high px-3 py-1 text-sm text-on-surface-variant">{historyStatusLabel(batch.status)}</span>
              </li>
            ))}
          </ul>
        ) : <p className="m-0 text-on-surface-variant">{t('noImportHistory')}</p>)}
      </section>
    </section>
  );
}
