import { useEffect, useId, useRef, useState, type ChangeEvent, type FormEvent } from 'react';
import { ApiError } from '../api/client';
import { downloadExport, type ImportCounts, type ImportPreviewDeck } from '../api/import';
import type { DeckNode, NativeLanguage, TargetLanguage } from '../api/content';
import { t } from '../i18n/en';
import { Button } from '../ui/Button';
import { useApplyImport, useExportDeck, useImportHistory, usePreviewImport } from './hooks';
import './ImportPanel.css';

interface ImportPanelProps {
  decks: DeckNode[];
  selectedDeck?: DeckNode;
  onDirtyChange?: (dirty: boolean) => void;
  onBusyChange?: (busy: boolean) => void;
}

function destinations(decks: DeckNode[], path = ''): Array<{ deck: DeckNode; label: string }> {
  return decks.flatMap((deck) => {
    const label = path ? `${path} › ${deck.name}` : deck.name;
    return [
      ...(deck.canEdit === true ? [{ deck, label }] : []),
      ...destinations(deck.children, label),
    ];
  });
}

function errorMessage(error: unknown, fallback = t('requestFailed')): string {
  return error instanceof ApiError && error.serverMessage ? error.serverMessage : fallback;
}

const numberFormat = new Intl.NumberFormat('en');

function total(counts: ImportCounts): number {
  return counts.new + counts.changed + counts.unchanged;
}

function totalLabel(count: number, kind: 'decks' | 'words'): string {
  const key = kind === 'decks'
    ? (count === 1 ? 'importOneDeck' : 'importDeckTotal')
    : (count === 1 ? 'importOneWord' : 'importWordTotal');
  return t(key).replace('{count}', numberFormat.format(count));
}

function countsLabel(counts: ImportCounts): string {
  return [
    counts.new > 0 && t('importNewCount').replace('{count}', numberFormat.format(counts.new)),
    counts.changed > 0 && t('importChangedCount').replace('{count}', numberFormat.format(counts.changed)),
    counts.unchanged > 0 && t('importUnchangedCount').replace('{count}', numberFormat.format(counts.unchanged)),
  ].filter(Boolean).join(' · ');
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

function PreviewTree({ decks, depth = 0 }: { decks: ImportPreviewDeck[]; depth?: number }) {
  return (
    <ul className="import-tree" role="list" aria-label={depth === 0 ? t('importTree') : undefined} data-indent={depth > 0 && depth <= 3 || undefined}>
      {decks.map((deck, index) => (
        <li key={`${deck.external_id ?? deck.name}-${index}`}>
          <div className="import-tree-row">
            <bdi className="font-medium">{deck.name}</bdi>
            <span className="import-meta">{statusLabel(deck.status)}</span>
            <p className="import-meta">
              {deck.decks.length > 0
                ? t('importWordsWithSubdecks').replace('{words}', totalLabel(total(deck.counts), 'words'))
                : totalLabel(total(deck.counts), 'words')}
              {total(deck.counts) > 0 && ` · ${countsLabel(deck.counts)}`}
            </p>
          </div>
          {deck.decks.length > 0 && <PreviewTree decks={deck.decks} depth={depth + 1} />}
        </li>
      ))}
    </ul>
  );
}

function warningsFor(decks: ImportPreviewDeck[]): Array<{ warning: string; deckName: string }> {
  return decks.flatMap((deck) => [
    ...deck.warnings.map((warning) => ({ warning, deckName: deck.name })),
    ...warningsFor(deck.decks),
  ]);
}

function formatDate(value: string): string {
  return new Intl.DateTimeFormat('en', { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value));
}

export function ImportPanel({ decks, selectedDeck, onDirtyChange, onBusyChange }: ImportPanelProps) {
  const id = useId();
  const fileRef = useRef<HTMLInputElement>(null);
  const previewHeading = useRef<HTMLHeadingElement>(null);
  const confirmHeading = useRef<HTMLHeadingElement>(null);
  const reviewButton = useRef<HTMLButtonElement>(null);
  const destinationSelect = useRef<HTMLSelectElement>(null);
  const [file, setFile] = useState<File | null>(null);
  const [fileError, setFileError] = useState(false);
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
  const deckOptions = destinations(decks);
  const parent = deckOptions.find(({ deck }) => String(deck.id) === parentDeckId)?.deck;
  const parentUnavailable = parentDeckId !== '' && !parent;
  const canExport = !!selectedDeck && selectedDeck.canEdit !== false;
  const busy = previewMutation.isPending || applyMutation.isPending || exportMutation.isPending;
  const deckWarnings = preview ? warningsFor(preview.tree) : [];
  const deckWarningMessages = new Set(deckWarnings.map(({ warning }) => warning));
  const otherWarnings = preview?.warnings.filter((warning) => !deckWarningMessages.has(warning)) ?? [];

  useEffect(() => { onDirtyChange?.(!applied && (file !== null || preview !== undefined)); }, [applied, file, preview, onDirtyChange]);
  useEffect(() => { onBusyChange?.(busy); }, [busy, onBusyChange]);
  useEffect(() => { if (preview) previewHeading.current?.focus(); }, [preview]);
  useEffect(() => { if (confirming) confirmHeading.current?.focus(); }, [confirming]);
  useEffect(() => { if (applied) fileRef.current?.focus(); }, [applied]);

  async function submitPreview(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!file || busy || preview) return;
    try {
      await previewMutation.mutateAsync(file);
    } catch {
      // Mutation state renders the request error beside the form.
    }
  }

  function selectFile(event: ChangeEvent<HTMLInputElement>) {
    const selected = event.currentTarget.files?.[0] ?? null;
    const invalid = selected !== null && !/\.(json|csv)$/i.test(selected.name);
    if (invalid) event.currentTarget.value = '';
    setFile(invalid ? null : selected);
    setFileError(invalid);
    setConfirming(false);
    setApplied(false);
    setParentDeckId('');
    setOnMissing('keep');
    previewMutation.reset();
    applyMutation.reset();
  }

  async function confirmApply() {
    if (!preview?.tree.length || !confirming || parentUnavailable || busy) return;
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
      if (fileRef.current) fileRef.current.value = '';
    } catch {
      // Mutation state renders the request error in the preview panel.
    }
  }

  async function startExport(format: 'retain' | 'csv') {
    if (!selectedDeck || !canExport || busy) return;
    try {
      const download = await exportMutation.mutateAsync({ deckId: selectedDeck.id, format });
      downloadExport(download);
    } catch {
      // Mutation state renders the request error beside the export controls.
    }
  }

  return (
    <div className="import-panel">
      {applied && <p role="status" className="import-success">{t('importApplied')}</p>}
      <form onSubmit={(event) => void submitPreview(event)} className="import-upload">
        <label className="import-field">
          {t('chooseImportFile')}
          <input
            ref={fileRef}
            type="file"
            accept=".json,.csv,application/json,text/csv"
            disabled={busy}
            onChange={selectFile}
            aria-invalid={fileError || undefined}
            aria-describedby={`${id}-file-hint${fileError ? ` ${id}-file-error` : ''}`}
          />
        </label>
        <p id={`${id}-file-hint`} className="import-meta">{t('supportedImportFormats')}</p>
        {fileError && <p id={`${id}-file-error`} role="alert" className="text-error">{t('importFileTypeError')}</p>}
        {previewMutation.isError && <p role="alert" className="text-error">{errorMessage(previewMutation.error)}</p>}
        {!preview && (
          <Button type="submit" disabled={!file || busy}>
            {t(previewMutation.isPending ? 'previewingImport' : 'previewImport')}
          </Button>
        )}
      </form>

      {preview && (
        <section className="import-preview" aria-labelledby={`${id}-preview`}>
          <div className="import-totals">
            <h3 id={`${id}-preview`} ref={previewHeading} tabIndex={-1} aria-describedby={`${id}-totals`}>{t('importPreview')}</h3>
            <div id={`${id}-totals`} className="import-metrics">
              {([['decks', preview.deck_counts], ['words', preview.counts]] as const).map(([kind, counts]) => (
                <div key={kind}>
                  <p className="import-total">{totalLabel(total(counts), kind)}</p>
                  {total(counts) > 0 && <p>{countsLabel(counts)}</p>}
                </div>
              ))}
            </div>
          </div>

          {(deckWarnings.length > 0 || otherWarnings.length > 0) && (
            <section className="import-warnings" aria-labelledby={`${id}-warnings`}>
              <h4 id={`${id}-warnings`}>{t('importWarnings')}</h4>
              <ul>
                {deckWarnings.map(({ deckName, warning }, index) => (
                  <li key={`${deckName}-${warning}-${index}`}><bdi className="font-medium">{deckName}</bdi>: <span>{warning}</span></li>
                ))}
                {otherWarnings.map((warning, index) => <li key={`${warning}-${index}`}>{warning}</li>)}
              </ul>
            </section>
          )}

          {preview.tree.length > 0 ? (
            <>
              <details className="import-disclosure" open={total(preview.deck_counts) <= 4}>
                <summary>{t('importTree')}</summary>
                <PreviewTree decks={preview.tree} />
              </details>
              <form className="import-setup" onSubmit={(event) => {
                event.preventDefault();
                if (!parentUnavailable && !busy) setConfirming(true);
              }}>
                <fieldset disabled={busy || confirming}>
                  <legend>{t('importSetup')}</legend>
                  <div className="import-languages">
                    <label className="import-field">
                      {t('targetLanguage')}
                      <select value={targetLanguage} onChange={(event) => setTargetLanguage(event.currentTarget.value as TargetLanguage)}>
                        <option value="de">{t('german')}</option>
                        <option value="en">{t('english')}</option>
                      </select>
                    </label>
                    <label className="import-field">
                      {t('nativeLanguage')}
                      <select value={nativeLanguage} onChange={(event) => setNativeLanguage(event.currentTarget.value as NativeLanguage)}>
                        <option value="pl">{t('polish')}</option>
                        <option value="ru">{t('russian')}</option>
                      </select>
                    </label>
                  </div>
                  <label className="import-field">
                    {t('optionalParentDeck')}
                    <select ref={destinationSelect} value={parentDeckId} aria-invalid={parentUnavailable || undefined} aria-describedby={parentUnavailable ? `${id}-destination-error` : undefined} onChange={(event) => setParentDeckId(event.currentTarget.value)}>
                      {parentUnavailable && <option value={parentDeckId} disabled>{t('importDestinationUnavailable')}</option>}
                      <option value="">{t('noParentDeck')}</option>
                      {deckOptions.map(({ deck, label }) => <option key={deck.id} value={String(deck.id)}>{label}</option>)}
                    </select>
                  </label>
                  {parentUnavailable && <p id={`${id}-destination-error`} role="alert" className="text-error">{t('importChooseAnotherDestination')}</p>}
                  <details className="import-disclosure">
                    <summary>{t('importReimportOptions')}</summary>
                    <label className="import-field">
                      {t('missingImportItems')}
                      <select value={onMissing} onChange={(event) => setOnMissing(event.currentTarget.value as 'keep' | 'suspend')}>
                        <option value="keep">{t('keepMissingItems')}</option>
                        <option value="suspend">{t('suspendMissingItems')}</option>
                      </select>
                    </label>
                  </details>
                </fieldset>
                {!confirming ? (
                  <Button ref={reviewButton} type="submit" disabled={parentUnavailable || busy}>{t('reviewBeforeApply')}</Button>
                ) : (
                  <div className="import-confirm" role="group" aria-labelledby={`${id}-confirm`}>
                    <h4 id={`${id}-confirm`} ref={confirmHeading} tabIndex={-1} aria-describedby={`${id}-confirm-hint`}>{t('confirmImportHeading')}</h4>
                    <p id={`${id}-confirm-hint`}>
                      {t('confirmImportDescription')}
                      {onMissing === 'suspend' && ` ${t('confirmSuspendMissingItems')}`}
                    </p>
                    {applyMutation.isError && <p role="alert" className="text-error">{errorMessage(applyMutation.error)}</p>}
                    <div className="import-actions">
                      <Button disabled={parentUnavailable || busy} onClick={() => void confirmApply()}>
                        {t(applyMutation.isPending ? 'applyingImport' : 'confirmApplyImport')}
                      </Button>
                      <Button variant="tonal" disabled={busy} onClick={() => {
                        setConfirming(false);
                        requestAnimationFrame(() => (parentUnavailable ? destinationSelect.current : reviewButton.current)?.focus());
                      }}>{t('cancel')}</Button>
                    </div>
                  </div>
                )}
              </form>
            </>
          ) : <p>{t('noImportDecks')}</p>}
        </section>
      )}

      <section className="import-export" aria-labelledby={`${id}-export`}>
        <h3 id={`${id}-export`}>{t('exportSelectedDeck')}</h3>
        <p className="import-meta">{canExport ? <bdi>{selectedDeck.name}</bdi> : t('exportRequiresDeck')}</p>
        <div className="import-actions">
          <Button variant="tonal" disabled={!canExport || busy} onClick={() => void startExport('retain')}>{t('exportRetain')}</Button>
          <Button variant="tonal" disabled={!canExport || busy} onClick={() => void startExport('csv')}>{t('exportCsv')}</Button>
        </div>
        {exportMutation.isPending && <p role="status" className="import-meta">{t('exporting')}</p>}
        {exportMutation.isError && <p role="alert" className="text-error">{errorMessage(exportMutation.error)}</p>}
      </section>

      <details className="import-disclosure import-history">
        <summary>{t('importHistory')}</summary>
        {historyQuery.isPending && <p role="status" className="import-meta">{t('loadingImportHistory')}</p>}
        {historyQuery.isError && (
          <div className="import-history-error">
            <p role="alert" className="text-error">{errorMessage(historyQuery.error, t('importHistoryError'))}</p>
            <Button variant="tonal" onClick={() => void historyQuery.refetch()}>{t('tryAgain')}</Button>
          </div>
        )}
        {historyQuery.isSuccess && (historyQuery.data.length > 0 ? (
          <ul className="import-batches" role="list">
            {historyQuery.data.slice(0, 5).map((batch) => (
              <li key={batch.id}>
                <bdi className="font-medium">{batch.filename || batch.adapter}</bdi>
                <span className="import-meta">{historyStatusLabel(batch.status)}</span>
                <p className="import-meta">{batch.adapter} · <time dateTime={batch.created_at}>{formatDate(batch.created_at)}</time></p>
              </li>
            ))}
          </ul>
        ) : <p className="import-meta">{t('noImportHistory')}</p>)}
      </details>
    </div>
  );
}
