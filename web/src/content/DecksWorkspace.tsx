import { useEffect, useRef, useState, type FormEvent } from 'react';
import * as DropdownMenu from '@radix-ui/react-dropdown-menu';
import { ApiError } from '../api/client';
import {
  type CreateNoteRequest,
  type DeckId,
  type DeckNode,
  type NativeLanguage,
  type Note,
  type PatchNoteRequest,
  type TargetLanguage,
} from '../api/content';
import { t } from '../i18n/en';
import { Button } from '../ui/Button';
import { WorkspaceDialog } from '../ui/WorkspaceDialog';
import { DeckEnrichmentPanel } from './DeckEnrichmentPanel';
import { ImportPanel } from './ImportPanel';
import { WordAutoFill, type SuggestionKey } from './WordAutoFill';
import './DecksWorkspace.css';
import {
  useCreateDeck,
  useCreateDeckNote,
  useDeckNotes,
  useDecks,
  useDeleteDeck,
  useDeleteNote,
  usePatchDeck,
  usePatchNote,
  useLlmStatus,
} from './hooks';

type CreateMode = 'root' | 'child' | null;
type DeckPanel = 'add' | 'rename' | 'transfer' | 'enrich' | null;

function flattenDecks(decks: DeckNode[]): DeckNode[] {
  return decks.flatMap((deck) => [deck, ...flattenDecks(deck.children)]);
}

function findDeck(decks: DeckNode[], id: DeckId | null): DeckNode | undefined {
  if (id === null) return undefined;
  for (const deck of decks) {
    if (String(deck.id) === String(id)) return deck;
    const child = findDeck(deck.children, id);
    if (child) return child;
  }
  return undefined;
}

function languageName(language: TargetLanguage | NativeLanguage): string {
  return t(language === 'de' ? 'german' : language === 'en' ? 'english' : language === 'pl' ? 'polish' : 'russian');
}

function languagePair(decks: DeckNode[], deck: DeckNode): string | undefined {
  let current: DeckNode | undefined = deck;
  const visited = new Set<string>();
  while (current && !visited.has(String(current.id))) {
    visited.add(String(current.id));
    if (current.langTarget && current.langNative) {
      return `${languageName(current.langTarget)} → ${languageName(current.langNative)}`;
    }
    current = current.parentId === null ? undefined : findDeck(decks, current.parentId);
  }
  return undefined;
}

function errorMessage(error: unknown): string {
  return error instanceof ApiError && error.serverMessage ? error.serverMessage : t('requestFailed');
}

function DeleteIcon() {
  return <svg aria-hidden="true" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8"><path d="M4 7h16M10 11v6m4-6v6M5 7l1 13h12l1-13M9 7V4h6v3" /></svg>;
}

function DeckIcon() {
  return <svg aria-hidden="true" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round"><path d="M8 3h10a3 3 0 0 1 3 3v11M4 7h10a3 3 0 0 1 3 3v8a3 3 0 0 1-3 3H4a3 3 0 0 1-3-3v-8a3 3 0 0 1 3-3Z" /><path d="M5 12h8M5 16h5" /></svg>;
}

function PlusIcon() {
  return <svg aria-hidden="true" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round"><path d="M12 5v14M5 12h14" /></svg>;
}

function ImportIcon() {
  return <svg aria-hidden="true" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"><path d="M12 3v12m-4-4 4 4 4-4M4 15v5h16v-5" /></svg>;
}

function DeckTree({
  decks,
  selectedId,
  onSelect,
  depth = 0,
}: {
  decks: DeckNode[];
  selectedId: string | null;
  onSelect: (deck: DeckNode) => void;
  depth?: number;
}) {
  if (decks.length === 0) return null;
  return (
    <ul className="m-0 list-none space-y-1 p-0" aria-label={depth === 0 ? t('deckList') : undefined}>
      {decks.map((deck) => {
        const selected = selectedId === String(deck.id);
        const counts = deck.counts;
        return (
          <li key={String(deck.id)}>
            <div style={{ paddingInlineStart: `${Math.min(depth, 6) * 0.75}rem` }}>
              <button
                type="button"
                aria-pressed={selected}
                onClick={() => onSelect(deck)}
                className={`deck-tree-item ${selected ? 'is-selected' : ''}`}
              >
                <span className="deck-tree-icon"><DeckIcon /></span>
                <span className="deck-tree-copy"><span className="deck-tree-name">{deck.name}</span>
                  {counts && <span className="deck-tree-count">{t('deckWordCount').replace('{count}', new Intl.NumberFormat('en').format(counts.notes))}</span>}
                </span>
                {selected && <svg className="deck-tree-check" aria-hidden="true" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="m5 12 4 4 10-10" /></svg>}
              </button>
            </div>
            <DeckTree decks={deck.children} selectedId={selectedId} onSelect={onSelect} depth={depth + 1} />
          </li>
        );
      })}
    </ul>
  );
}

function optionalValue(value: string): string | undefined {
  const clean = value.trim();
  return clean || undefined;
}

function noteInput(form: HTMLFormElement, fields?: Record<string, unknown>): CreateNoteRequest {
  const data = new FormData(form);
  const input: CreateNoteRequest = {
    term: String(data.get('term') ?? '').trim(),
    translation: String(data.get('translation') ?? '').trim(),
  };
  const transcription = optionalValue(String(data.get('transcription') ?? ''));
  const example = optionalValue(String(data.get('example') ?? ''));
  const exampleTranslation = optionalValue(String(data.get('example_translation') ?? ''));
  const notes = optionalValue(String(data.get('notes') ?? ''));
  if (transcription) input.transcription = transcription;
  if (example) input.example = example;
  if (exampleTranslation) input.example_translation = exampleTranslation;
  if (notes) input.notes = notes;
  const tags = String(data.get('tags') ?? '').split(',').map((value) => value.trim()).filter(Boolean);
  const alternatives = String(data.get('alternatives') ?? '').split(',').map((value) => value.trim()).filter(Boolean);
  if (tags.length > 0) input.tags = tags;
  if (alternatives.length > 0) input.alternatives = alternatives;
  if (fields && Object.keys(fields).length > 0) input.fields = fields;
  return input;
}

function NoteFields({ note, prefix, onTermChange }: { note?: Note; prefix: string; onTermChange?: (term: string) => void }) {
  const fieldId = (name: string) => `${prefix}-${name}`;
  return (
    <>
      <div className="grid gap-4 medium:grid-cols-2">
        <label className="grid gap-2 text-sm font-medium text-on-surface" htmlFor={fieldId('term')}>
          {t('term')}
          <input id={fieldId('term')} name="term" required defaultValue={note?.term ?? ''} onChange={onTermChange ? (event) => onTermChange(event.target.value) : undefined} className="min-h-12 rounded-xl border border-outline bg-surface-container-lowest px-4 text-base font-normal text-on-surface focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary" />
        </label>
        <label className="grid gap-2 text-sm font-medium text-on-surface" htmlFor={fieldId('translation')}>
          {t('translation')}
          <input id={fieldId('translation')} name="translation" required defaultValue={note?.translation ?? ''} className="min-h-12 rounded-xl border border-outline bg-surface-container-lowest px-4 text-base font-normal text-on-surface focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary" />
        </label>
      </div>
      <details className="note-optional-fields">
        <summary>{t('moreWordFields')}</summary>
        <div className="grid gap-4 medium:grid-cols-2">
        <label className="grid gap-2 text-sm font-medium text-on-surface" htmlFor={fieldId('transcription')}>
          {t('transcription')}
          <input id={fieldId('transcription')} name="transcription" defaultValue={note?.transcription ?? ''} className="min-h-12 rounded-xl border border-outline bg-surface-container-lowest px-4 text-base font-normal text-on-surface focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary" />
        </label>
        <label className="grid gap-2 text-sm font-medium text-on-surface" htmlFor={fieldId('example')}>
          {t('example')}
          <input id={fieldId('example')} name="example" defaultValue={note?.example ?? ''} className="min-h-12 rounded-xl border border-outline bg-surface-container-lowest px-4 text-base font-normal text-on-surface focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary" />
        </label>
        <label className="grid gap-2 text-sm font-medium text-on-surface" htmlFor={fieldId('example-translation')}>
          {t('exampleTranslation')}
          <input id={fieldId('example-translation')} name="example_translation" defaultValue={note?.exampleTranslation ?? ''} className="min-h-12 rounded-xl border border-outline bg-surface-container-lowest px-4 text-base font-normal text-on-surface focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary" />
        </label>
        <label className="grid gap-2 text-sm font-medium text-on-surface" htmlFor={fieldId('tags')}>
          {t('tags')}
          <input id={fieldId('tags')} name="tags" defaultValue={note?.tags?.join(', ') ?? ''} aria-describedby={fieldId('tags-hint')} className="min-h-12 rounded-xl border border-outline bg-surface-container-lowest px-4 text-base font-normal text-on-surface focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary" />
          <span id={fieldId('tags-hint')} className="font-normal text-on-surface-variant">{t('commaSeparatedHint')}</span>
        </label>
        <label className="grid gap-2 text-sm font-medium text-on-surface medium:col-span-2" htmlFor={fieldId('alternatives')}>
          {t('alternatives')}
          <input id={fieldId('alternatives')} name="alternatives" defaultValue={note?.alternatives?.join(', ') ?? ''} aria-describedby={fieldId('alternatives-hint')} className="min-h-12 rounded-xl border border-outline bg-surface-container-lowest px-4 text-base font-normal text-on-surface focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary" />
          <span id={fieldId('alternatives-hint')} className="font-normal text-on-surface-variant">{t('commaSeparatedHint')}</span>
        </label>
      </div>
      <label className="mt-4 grid gap-2 text-sm font-medium text-on-surface" htmlFor={fieldId('notes')}>
        {t('wordNotes')}
        <textarea id={fieldId('notes')} name="notes" rows={3} defaultValue={note?.notes ?? ''} className="rounded-xl border border-outline bg-surface-container-lowest px-4 py-3 text-base font-normal text-on-surface focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary" />
      </label>
      </details>
    </>
  );
}

export function DecksWorkspace({ ownerId = 0 }: { ownerId?: number }) {
  const decksQuery = useDecks();
  const llmStatusQuery = useLlmStatus();
  const decks = decksQuery.data ?? [];
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [createMode, setCreateMode] = useState<CreateMode>(null);
  const [panel, setPanel] = useState<DeckPanel>(null);
  const [panelDirty, setPanelDirty] = useState(false);
  const [panelBusy, setPanelBusy] = useState(false);
  const [targetLanguage, setTargetLanguage] = useState<TargetLanguage>('de');
  const [nativeLanguage, setNativeLanguage] = useState<NativeLanguage>('pl');
  const [reverseCards, setReverseCards] = useState(false);
  const [editingNote, setEditingNote] = useState<DeckId | null>(null);
  const [newNoteTerm, setNewNoteTerm] = useState('');
  const [newNoteFields, setNewNoteFields] = useState<Record<string, unknown>>({});
  const newNoteForm = useRef<HTMLFormElement>(null);
  const taskOpener = useRef<HTMLElement | null>(null);
  const deckActionsTrigger = useRef<HTMLButtonElement>(null);
  const selectedDeck = findDeck(decks, selectedId);
  const notesQuery = useDeckNotes(selectedDeck?.id ?? null);
  const createDeckMutation = useCreateDeck();
  const patchDeckMutation = usePatchDeck();
  const deleteDeckMutation = useDeleteDeck();
  const createNoteMutation = useCreateDeckNote();
  const patchNoteMutation = usePatchNote();
  const deleteNoteMutation = useDeleteNote();

  useEffect(() => {
    if (selectedId && !findDeck(decks, selectedId)) {
      setSelectedId(null);
      setEditingNote(null);
      setPanel(null);
      setPanelDirty(false);
      setPanelBusy(false);
      setCreateMode(null);
    }
    if (!selectedId && decks[0]) setSelectedId(String(decks[0].id));
  }, [decks, selectedId]);

  function beginCreate(mode: Exclude<CreateMode, null>) {
    rememberTaskOpener();
    createDeckMutation.reset();
    setReverseCards(false);
    setCreateMode(mode);
  }

  function openPanel(next: Exclude<DeckPanel, null>) {
    rememberTaskOpener();
    patchDeckMutation.reset();
    createNoteMutation.reset();
    if (next === 'add') {
      setNewNoteTerm('');
      setNewNoteFields({});
    }
    setPanel(next);
    setPanelDirty(false);
    setPanelBusy(false);
  }

  function closePanel(open: boolean) {
    if (open || panelBusy) return;
    if (panelDirty && !window.confirm(t('discardTaskChanges'))) return;
    setPanel(null);
  }

  function rememberTaskOpener() {
    const active = document.activeElement;
    taskOpener.current = active instanceof HTMLElement
      ? active.getAttribute('role') === 'menuitem' ? deckActionsTrigger.current : active
      : null;
  }

  function selectDeck(deck: DeckNode) {
    setSelectedId(String(deck.id));
    setEditingNote(null);
    setPanel(null);
    patchDeckMutation.reset();
  }

  function submitDeck(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!createMode) return;
    const data = new FormData(event.currentTarget);
    const name = String(data.get('name') ?? '').trim();
    if (!name) return;
    createDeckMutation.mutate({
      name,
      parent_id: createMode === 'child' ? selectedDeck?.id ?? null : null,
      ...(createMode === 'root' ? { lang_target: targetLanguage, lang_native: nativeLanguage } : {}),
      reverse_cards: reverseCards,
    }, { onSuccess: () => setCreateMode(null) });
  }

  function submitDeckRename(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!selectedDeck) return;
    const name = String(new FormData(event.currentTarget).get('name') ?? '').trim();
    if (name) patchDeckMutation.mutate({ id: selectedDeck.id, input: { name } }, { onSuccess: () => setPanel(null) });
  }

  function removeDeck(deck: DeckNode) {
    if (!window.confirm(t('confirmDeleteDeck').replace('{name}', deck.name))) return;
    const selected = findDeck(decks, selectedId);
    const removed = new Set(flattenDecks([deck]).map((item) => String(item.id)));
    deleteDeckMutation.mutate(deck.id, {
      onSuccess: () => {
        if (selected && removed.has(String(selected.id))) {
          setSelectedId(deck.parentId === null ? null : String(deck.parentId));
          setEditingNote(null);
        }
      },
    });
  }

  function submitNote(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!selectedDeck) return;
    const form = event.currentTarget;
    const input = noteInput(form, newNoteFields);
    if (!input.term || !input.translation) return;
    createNoteMutation.mutate({ deckId: selectedDeck.id, input }, {
      onSuccess: () => {
        form.reset();
        setNewNoteTerm('');
        setNewNoteFields({});
        setPanel(null);
      },
    });
  }

  function useWordSuggestion(key: SuggestionKey, value: string | string[]) {
    if (key.startsWith('fields.')) {
      setNewNoteFields((previous) => ({ ...previous, [key.slice('fields.'.length)]: value }));
      return;
    }
    const elementName = key;
    const input = newNoteForm.current?.elements.namedItem(elementName);
    if (input instanceof HTMLInputElement || input instanceof HTMLTextAreaElement) {
      input.value = Array.isArray(value) ? value.join(', ') : value;
      const details = input.closest('details');
      if (details) details.open = true;
    }
  }

  function submitNoteUpdate(event: FormEvent<HTMLFormElement>, note: Note) {
    event.preventDefault();
    if (!selectedDeck) return;
    const input = noteInput(event.currentTarget);
    if (!input.term || !input.translation) return;
    const patch: PatchNoteRequest = {
      ...input,
      transcription: input.transcription ?? null,
      example: input.example ?? null,
      example_translation: input.example_translation ?? null,
      notes: input.notes ?? null,
      tags: input.tags ?? [],
      alternatives: input.alternatives ?? [],
    };
    patchNoteMutation.mutate({ deckId: selectedDeck.id, id: note.id, input: patch }, {
      onSuccess: () => setEditingNote(null),
    });
  }

  function removeNote(note: Note) {
    if (!window.confirm(t('confirmDeleteNote').replace('{term}', note.term))) return;
    if (selectedDeck) deleteNoteMutation.mutate({ deckId: selectedDeck.id, id: note.id });
  }

  const pair = selectedDeck ? languagePair(decks, selectedDeck) : undefined;

  return (
    <section className="deck-workspace" aria-label={t('decksWorkspace')}>
      {decksQuery.isPending && <p role="status" className="text-on-surface-variant">{t('loadingDecks')}</p>}
      {decksQuery.isError && (
        <div role="alert" className="space-y-3 rounded-2xl bg-error-container p-4 text-on-error-container">
          <p className="m-0">{errorMessage(decksQuery.error)}</p>
          <Button type="button" variant="tonal" onClick={() => void decksQuery.refetch()}>{t('tryAgain')}</Button>
        </div>
      )}

      {!decksQuery.isPending && !decksQuery.isError && (
        <div className="deck-workspace-layout">
          <aside className="deck-sidebar" aria-labelledby="deck-list-heading">
            <div className="deck-sidebar-heading">
              <h2 id="deck-list-heading">{t('decks')}</h2>
              <button type="button" className="workspace-icon-button" aria-label={t('newDeck')} onClick={() => beginCreate('root')}><PlusIcon /></button>
            </div>
            {decks.length > 0 && <label className="deck-mobile-picker" htmlFor="mobile-deck-picker">
              <span className="sr-only">{t('selectedDeck')}</span>
              <DeckIcon />
              <select id="mobile-deck-picker" value={selectedId ?? ''} onChange={(event) => { const deck = findDeck(decks, event.target.value); if (deck) selectDeck(deck); }}>
                {!selectedDeck && <option value="">{t('decks')}</option>}
                {flattenDecks(decks).map((deck) => <option key={String(deck.id)} value={String(deck.id)}>{deck.name}</option>)}
              </select>
            </label>}
            {decks.length === 0 ? <p className="m-0 py-3 text-on-surface-variant">{t('noDecks')}</p> : (
              <DeckTree decks={decks} selectedId={selectedId} onSelect={selectDeck} />
            )}
            {deleteDeckMutation.isError && <p role="alert" className="m-0 text-error">{errorMessage(deleteDeckMutation.error)}</p>}
            <button type="button" className="deck-import-action" onClick={() => openPanel('transfer')}><ImportIcon />{t('importExport')}</button>
          </aside>

          <div className="deck-main">
            <WorkspaceDialog open={createMode !== null} onOpenChange={(open) => { if (!open) setCreateMode(null); }} title={t(createMode === 'child' ? 'addChildDeck' : 'newDeck')} returnFocusRef={taskOpener}>
              <form onSubmit={submitDeck} className="space-y-4" aria-label={t(createMode === 'child' ? 'addChildDeck' : 'newDeck')}>
                <label className="grid gap-2 text-sm font-medium text-on-surface" htmlFor="new-deck-name">
                  {t('deckName')}
                  <input id="new-deck-name" name="name" required autoFocus className="min-h-12 rounded-xl border border-outline bg-surface-container-lowest px-4 text-base font-normal text-on-surface focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary" />
                </label>
                {createMode === 'root' && (
                  <div className="grid gap-4 medium:grid-cols-2">
                    <label className="grid gap-2 text-sm font-medium text-on-surface" htmlFor="target-language">
                      {t('targetLanguage')}
                      <select id="target-language" value={targetLanguage} onChange={(event) => setTargetLanguage(event.target.value as TargetLanguage)} className="min-h-12 rounded-xl border border-outline bg-surface-container-lowest px-4 text-base font-normal text-on-surface focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary">
                        <option value="de">{t('german')}</option><option value="en">{t('english')}</option>
                      </select>
                    </label>
                    <label className="grid gap-2 text-sm font-medium text-on-surface" htmlFor="native-language">
                      {t('nativeLanguage')}
                      <select id="native-language" value={nativeLanguage} onChange={(event) => setNativeLanguage(event.target.value as NativeLanguage)} className="min-h-12 rounded-xl border border-outline bg-surface-container-lowest px-4 text-base font-normal text-on-surface focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary">
                        <option value="pl">{t('polish')}</option><option value="ru">{t('russian')}</option>
                      </select>
                    </label>
                  </div>
                )}
                {createMode === 'child' && selectedDeck && <p className="m-0 text-sm text-on-surface-variant">{t('childDeckUnder')} <strong className="font-medium text-on-surface">{selectedDeck.name}</strong>{pair ? ` · ${pair}` : ''}</p>}
                <label className="flex min-h-12 items-center gap-3 text-sm font-medium text-on-surface" htmlFor="new-deck-reverse-cards">
                  <input id="new-deck-reverse-cards" type="checkbox" checked={reverseCards} onChange={(event) => setReverseCards(event.target.checked)} className="size-5 accent-primary" />
                  {t('reverseCards')}
                </label>
                <p className="m-0 text-sm text-on-surface-variant">{t('reverseCardsHint')}</p>
                {createDeckMutation.isError && <p role="alert" className="m-0 text-error">{errorMessage(createDeckMutation.error)}</p>}
                <div className="flex flex-wrap gap-3">
                  <Button type="submit" disabled={createDeckMutation.isPending}>{t(createDeckMutation.isPending ? 'creatingDeck' : 'createDeck')}</Button>
                  <Button type="button" variant="tonal" onClick={() => setCreateMode(null)}>{t('cancel')}</Button>
                </div>
              </form>
            </WorkspaceDialog>

            {selectedDeck ? (
              <section className="deck-detail" aria-labelledby="selected-deck-heading">
                <header className="deck-detail-header">
                  <div className="deck-heading-copy">
                    <p className="deck-language">{pair ?? t('languagePairNotSet')}</p>
                    <h1 id="selected-deck-heading">{selectedDeck.name}</h1>
                    {selectedDeck.counts && <p className="deck-count-summary">{t('deckCounts').replace('{notes}', new Intl.NumberFormat('en').format(selectedDeck.counts.notes)).replace('{new}', new Intl.NumberFormat('en').format(selectedDeck.counts.new)).replace('{learning}', new Intl.NumberFormat('en').format(selectedDeck.counts.learning)).replace('{due}', new Intl.NumberFormat('en').format(selectedDeck.counts.due))}</p>}
                  </div>
                  <div className="deck-header-actions">
                    <a className="deck-study-link" href="/study"><svg aria-hidden="true" width="20" height="20" viewBox="0 0 24 24" fill="currentColor"><path d="m8 5 11 7-11 7z" /></svg>{t('study')}</a>
                    {selectedDeck.canEdit !== false && <DropdownMenu.Root>
                      <DropdownMenu.Trigger ref={deckActionsTrigger} className="workspace-icon-button" aria-label={t('deckActions')}><svg aria-hidden="true" width="24" height="24" viewBox="0 0 24 24" fill="currentColor"><circle cx="5" cy="12" r="2" /><circle cx="12" cy="12" r="2" /><circle cx="19" cy="12" r="2" /></svg></DropdownMenu.Trigger>
                      <DropdownMenu.Portal><DropdownMenu.Content className="deck-actions-menu" align="end" sideOffset={8}>
                        <DropdownMenu.Item onSelect={() => openPanel('rename')}>{t('renameDeck')}</DropdownMenu.Item>
                        <DropdownMenu.Item onSelect={() => beginCreate('child')}>{t('addChildDeck')}</DropdownMenu.Item>
                        <DropdownMenu.Item onSelect={() => openPanel('transfer')}>{t('importExport')}</DropdownMenu.Item>
                        {ownerId > 0 && <DropdownMenu.Item onSelect={() => openPanel('enrich')}>{t('deckEnrichment')}</DropdownMenu.Item>}
                        <DropdownMenu.Separator />
                        <DropdownMenu.Item className="deck-action-destructive" onSelect={() => removeDeck(selectedDeck)}>{t('deleteDeck')}</DropdownMenu.Item>
                      </DropdownMenu.Content></DropdownMenu.Portal>
                    </DropdownMenu.Root>}
                  </div>
                </header>
                {selectedDeck.canEdit === false && <p className="deck-readonly">{t('readOnlySubscription')}</p>}

                {selectedDeck.canEdit !== false && <WorkspaceDialog open={panel === 'rename'} onOpenChange={(open) => { if (!open) setPanel(null); }} title={t('renameDeck')} returnFocusRef={taskOpener}>
                <form onSubmit={submitDeckRename} className="grid gap-3" aria-label={t('renameDeck')}>
                  <label className="grid gap-2 text-sm font-medium text-on-surface" htmlFor="rename-deck">
                    {t('deckName')}
                    <input id="rename-deck" name="name" required defaultValue={selectedDeck.name} key={String(selectedDeck.id) + selectedDeck.name} className="min-h-12 rounded-xl border border-outline bg-surface-container-lowest px-4 text-base font-normal text-on-surface focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary" />
                  </label>
                  <Button type="submit" className="self-end" disabled={patchDeckMutation.isPending}>{t(patchDeckMutation.isPending ? 'savingDeck' : 'saveDeck')}</Button>
                </form>
                {patchDeckMutation.isError && <p role="alert" className="m-0 text-error">{errorMessage(patchDeckMutation.error)}</p>}
                {patchDeckMutation.isSuccess && <p role="status" className="m-0 text-on-surface-variant">{t('deckSaved')}</p>}
                </WorkspaceDialog>}

                <div className="deck-words">
                  <div className="deck-words-heading"><h2>{t('notes')}</h2>
                    {selectedDeck.canEdit !== false && <Button type="button" variant="tonal" onClick={() => openPanel('add')}><PlusIcon />{t('addNote')}</Button>}
                  </div>
                  {notesQuery.isPending && <p role="status" className="m-0 text-on-surface-variant">{t('loadingNotes')}</p>}
                  {notesQuery.isError && <p role="alert" className="m-0 text-error">{errorMessage(notesQuery.error)}</p>}
                  {notesQuery.isSuccess && (notesQuery.data.length ? (
                    <ul className="deck-note-list m-0 list-none p-0" aria-label={t('noteList')}>
                      {notesQuery.data.map((note) => (
                        <li key={String(note.id)} className="deck-note">
                          {String(editingNote) === String(note.id) ? (
                            <form onSubmit={(event) => submitNoteUpdate(event, note)} className="space-y-4" aria-label={t('editNote')}>
                              <NoteFields note={note} prefix={`edit-${note.id}`} />
                              {patchNoteMutation.isError && <p role="alert" className="m-0 text-error">{errorMessage(patchNoteMutation.error)}</p>}
                              <div className="flex flex-wrap gap-3">
                                <Button type="submit" disabled={patchNoteMutation.isPending}>{t(patchNoteMutation.isPending ? 'savingNote' : 'saveNote')}</Button>
                                <Button type="button" variant="tonal" onClick={() => setEditingNote(null)}>{t('cancel')}</Button>
                              </div>
                            </form>
                          ) : (
                            <div className="deck-note-content">
                              <div className="deck-note-words min-w-0">
                                <p className="deck-note-term m-0 break-words text-on-surface">{note.term}</p>
                                <p className="deck-note-translation m-0 break-words text-on-surface-variant">{note.translation}</p>
                                {note.example && <p className="m-0 mt-2 break-words text-sm text-on-surface-variant">{note.example}</p>}
                                {note.tags && note.tags.length > 0 && <p className="m-0 mt-2 break-words text-sm text-on-surface-variant">{t('tags')}: {note.tags.join(', ')}</p>}
                              </div>
                              {selectedDeck.canEdit !== false && <div className="flex gap-2">
                                <Button type="button" variant="tonal" onClick={() => setEditingNote(note.id)}>{t('edit')}</Button>
                                <button type="button" aria-label={`${t('deleteNote')} ${note.term}`} title={t('deleteNote')} onClick={() => removeNote(note)} className="inline-flex min-h-12 min-w-12 items-center justify-center rounded-full text-on-surface-variant hover:bg-error-container hover:text-on-error-container focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary"><DeleteIcon /></button>
                              </div>}
                            </div>
                          )}
                        </li>
                      ))}
                    </ul>
                   ) : <div className="deck-empty">
                     <span className="deck-empty-icon"><DeckIcon /></span>
                     <h3>{t('noNotes')}</h3>
                     {selectedDeck.canEdit !== false && <><p>{t('emptyDeckHint')}</p><button type="button" className="deck-import-action" onClick={() => openPanel('transfer')}><ImportIcon />{t('importDeck')}</button></>}
                   </div>)}
                  {deleteNoteMutation.isError && <p role="alert" className="m-0 text-error">{errorMessage(deleteNoteMutation.error)}</p>}

                  {selectedDeck.canEdit !== false && <WorkspaceDialog key={String(selectedDeck.id)} open={panel === 'add'} onOpenChange={(open) => { if (!open) setPanel(null); }} title={t('addNote')} returnFocusRef={taskOpener}>
                    <form ref={newNoteForm} onSubmit={submitNote} className="space-y-4" aria-label={t('addNote')}>
                      <NoteFields prefix="new-note" onTermChange={setNewNoteTerm} />
                      {llmStatusQuery.data?.enabled && typeof selectedDeck.id === 'number' && (
                        <WordAutoFill deckId={selectedDeck.id} word={newNoteTerm} enabled onUse={useWordSuggestion} />
                      )}
                      <Button type="submit" disabled={createNoteMutation.isPending}>{t(createNoteMutation.isPending ? 'addingNote' : 'addNote')}</Button>
                      {createNoteMutation.isError && <p role="alert" className="text-error">{errorMessage(createNoteMutation.error)}</p>}
                    </form>
                  </WorkspaceDialog>}
                </div>

                {selectedDeck.canEdit !== false && ownerId > 0 && <WorkspaceDialog open={panel === 'enrich'} onOpenChange={closePanel} title={t('deckEnrichment')} returnFocusRef={taskOpener}>
                   {!llmStatusQuery.data?.enabled && <p className="text-on-surface-variant">{t('llmFeaturesDisabled')}</p>}
                   <DeckEnrichmentPanel
                    ownerId={ownerId}
                    deckId={selectedDeck.id}
                    deckName={selectedDeck.name}
                     enabled={llmStatusQuery.data?.enabled === true}
                     onDirtyChange={setPanelDirty}
                     onBusyChange={setPanelBusy}
                  />
                </WorkspaceDialog>}
              </section>
            ) : (
              <section className="deck-detail deck-no-selection"><DeckIcon /><h1>{t('selectOrCreateDeck')}</h1><Button type="button" onClick={() => beginCreate('root')}><PlusIcon />{t('newDeck')}</Button></section>
            )}
            <WorkspaceDialog open={panel === 'transfer'} onOpenChange={closePanel} title={t('importExport')} returnFocusRef={taskOpener}>
              <ImportPanel decks={decks} selectedDeck={selectedDeck?.canEdit === false ? undefined : selectedDeck} onDirtyChange={setPanelDirty} onBusyChange={setPanelBusy} />
            </WorkspaceDialog>
          </div>
        </div>
      )}
    </section>
  );
}
