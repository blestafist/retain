import { useEffect, useId, useRef, useState, useSyncExternalStore, type FormEvent } from 'react';
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
import { DeckGlyph } from './DeckGlyph';
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
type Direction = 'forward' | 'reverse' | 'both';

const directionFlags = {
  forward: { reverse_cards: false, reverse_only: false },
  reverse: { reverse_cards: true, reverse_only: true },
  both: { reverse_cards: true, reverse_only: false },
} as const;

function deckDirection(deck: DeckNode): Direction {
  return deck.reverseOnly ? 'reverse' : deck.reverseCards ? 'both' : 'forward';
}
const directionLabels = { reverse: 'directionReverse', forward: 'directionForward', both: 'directionBoth' } as const;
type DeckPanel = 'add' | 'rename' | 'direction' | 'transfer' | 'enrich' | null;

const expandedViewport = '(min-width: 52.5rem)';
const isExpanded = () => window.matchMedia?.(expandedViewport).matches ?? true;
function subscribeViewport(listener: () => void) {
  const media = window.matchMedia?.(expandedViewport);
  media?.addEventListener('change', listener);
  return () => media?.removeEventListener('change', listener);
}

const numberFormat = new Intl.NumberFormat('en');

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

function wordCount(count: number, includesSubdecks: boolean) {
  const words = count === 1 ? t('oneDeckWord') : t('deckWordCount').replace('{count}', numberFormat.format(count));
  return includesSubdecks ? t('deckWordTotal').replace('{words}', words) : words;
}

function DeckArtwork() {
  return (
    <svg className="deck-artwork" viewBox="0 0 256 240" aria-hidden="true" focusable="false">
      <path className="deck-art-orbit" d="M6 182C-2 159 49 112 123 77S250 38 258 61s-46 73-120 108S14 205 6 182Z" />
      <path className="deck-art-card-back" d="M108 40h85a25 25 0 0 1 25 25v91a25 25 0 0 1-25 25h-85a25 25 0 0 1-25-25V65a25 25 0 0 1 25-25Z" transform="rotate(17 150 110)" />
      <rect className="deck-art-card-front" x="44" y="63" width="116" height="138" rx="28" transform="rotate(-16 102 132)" />
      <path className="deck-art-lines" d="M70 123h64m-64 19h42m-42 19h26" transform="rotate(-16 102 132)" />
      <path className="deck-art-spark" d="M203 128c0 23 10 33 33 33-23 0-33 10-33 33 0-23-10-33-33-33 23 0 33-10 33-33ZM48 40c0 12 6 18 18 18-12 0-18 6-18 18 0-12-6-18-18-18 12 0 18-6 18-18Z" />
    </svg>
  );
}

function DeckEntry({ deck, selected, onSelect }: { deck: DeckNode; selected?: boolean; onSelect: (deck: DeckNode, opener: HTMLButtonElement) => void }) {
  const id = useId();
  const children = deck.children.length;
  const subdecks = children === 1 ? t('oneSubdeck') : t('subdeckCount').replace('{count}', numberFormat.format(children));
  const meta = [children > 0 && subdecks, deck.counts && wordCount(deck.counts.notes, children > 0)].filter(Boolean).join(' · ');
  return (
    <button type="button" className="deck-entry" aria-current={selected ? 'true' : undefined} onClick={(event) => onSelect(deck, event.currentTarget)} aria-labelledby={id} aria-describedby={meta ? `${id}-meta` : undefined}>
      <span className={`deck-entry-icon${children ? ' is-folder' : ''}`}><DeckGlyph name={children ? 'folder' : 'deck'} /></span>
      <span className="deck-entry-copy">
        <bdi className="deck-entry-name" id={id}>{deck.name}</bdi>
        {meta && <span className="deck-entry-meta" id={`${id}-meta`}>{meta}</span>}
      </span>
      <span className="deck-entry-next"><DeckGlyph name="next" /></span>
    </button>
  );
}

function DeckTree({
  decks,
  selectedId,
  onSelect,
  depth = 0,
}: {
  decks: DeckNode[];
  selectedId: string | null;
  onSelect: (deck: DeckNode, opener: HTMLButtonElement) => void;
  depth?: number;
}) {
  if (decks.length === 0) return null;
  return (
    <ul className={depth === 0 ? 'deck-tree' : 'deck-branches'} data-indent={depth > 0 && depth <= 3 || undefined} aria-label={depth === 0 ? t('deckList') : undefined}>
      {decks.map((deck) => (
        <li key={deck.id}>
          <DeckEntry deck={deck} selected={selectedId === String(deck.id)} onSelect={onSelect} />
          <DeckTree decks={deck.children} selectedId={selectedId} onSelect={onSelect} depth={depth + 1} />
        </li>
      ))}
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
  const expanded = useSyncExternalStore(subscribeViewport, isExpanded);
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
  const [direction, setDirection] = useState<Direction>('reverse');
  const [editingNote, setEditingNote] = useState<DeckId | null>(null);
  const [newNoteTerm, setNewNoteTerm] = useState('');
  const [newNoteFields, setNewNoteFields] = useState<Record<string, unknown>>({});
  const newNoteForm = useRef<HTMLFormElement>(null);
  const taskOpener = useRef<HTMLElement | null>(null);
  const deckActionsTrigger = useRef<HTMLButtonElement>(null);
  const detailHeading = useRef<HTMLHeadingElement>(null);
  const libraryHeading = useRef<HTMLHeadingElement>(null);
  const deckOpener = useRef<HTMLButtonElement | null>(null);
  const viewFocus = useRef<'detail' | 'library' | null>(null);
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
    if (expanded && !selectedId && decks[0]) setSelectedId(String(decks[0].id));
  }, [decks, expanded, selectedId]);

  useEffect(() => {
    if (viewFocus.current === 'detail' && selectedDeck && !expanded) detailHeading.current?.focus();
    if (viewFocus.current === 'library') {
      const opener = deckOpener.current;
      if (opener?.isConnected) opener.focus();
      else libraryHeading.current?.focus();
    }
    viewFocus.current = null;
  }, [selectedId, expanded, selectedDeck]);

  function beginCreate(mode: Exclude<CreateMode, null>) {
    rememberTaskOpener();
    createDeckMutation.reset();
    setDirection('reverse');
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
    if (next === 'direction' && selectedDeck) setDirection(deckDirection(selectedDeck));
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

  function selectDeck(deck: DeckNode, opener: HTMLButtonElement) {
    deckOpener.current = opener;
    if (!expanded) {
      viewFocus.current = 'detail';
      window.scrollTo(0, 0);
    }
    setSelectedId(String(deck.id));
    setEditingNote(null);
    setPanel(null);
    patchDeckMutation.reset();
  }

  function showLibrary() {
    viewFocus.current = 'library';
    setSelectedId(null);
    setEditingNote(null);
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
      ...directionFlags[direction],
    }, { onSuccess: () => setCreateMode(null) });
  }

  function submitDeckRename(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!selectedDeck) return;
    const name = String(new FormData(event.currentTarget).get('name') ?? '').trim();
    if (name) patchDeckMutation.mutate({ id: selectedDeck.id, input: { name } }, { onSuccess: () => setPanel(null) });
  }

  function submitDeckDirection(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (selectedDeck) patchDeckMutation.mutate({ id: selectedDeck.id, input: directionFlags[direction] }, {
      onSuccess: () => setPanel(null),
    });
  }

  function removeDeck(deck: DeckNode) {
    if (!window.confirm(t('confirmDeleteDeck').replace('{name}', deck.name))) return;
    const selected = findDeck(decks, selectedId);
    const removed = new Set(flattenDecks([deck]).map((item) => String(item.id)));
    deleteDeckMutation.mutate(deck.id, {
      onSuccess: () => {
        if (selected && removed.has(String(selected.id))) {
          if (!expanded) showLibrary();
          else {
            setSelectedId(deck.parentId === null ? null : String(deck.parentId));
            setEditingNote(null);
          }
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
  const counts = selectedDeck?.counts;
  const hasSubdecks = !!selectedDeck?.children.length;
  const showWords = !hasSubdecks || !notesQuery.isSuccess || notesQuery.data.length > 0;
  const LibraryHeading = expanded && selectedDeck ? 'h2' : 'h1';

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
          <section hidden={!expanded && !!selectedDeck} className="deck-sidebar" aria-labelledby="deck-list-heading">
            <div className="deck-sidebar-heading page-intro">
              <LibraryHeading id="deck-list-heading" ref={libraryHeading} tabIndex={-1}>{t('deckLibrary')}</LibraryHeading>
              {!expanded && decks.length > 0 && <p>{t('deckLibraryHint')}</p>}
            </div>
            <div className="deck-library-actions">
              <Button type="button" onClick={() => beginCreate('root')}><DeckGlyph name="plus" />{t('newDeck')}</Button>
              <button type="button" className="deck-library-import workspace-icon-button" aria-label={t('importExport')} title={t('importExport')} onClick={() => openPanel('transfer')}><DeckGlyph name="transfer" /></button>
            </div>
            {decks.length === 0 ? <div className="deck-library-empty"><span className="deck-empty-icon"><DeckGlyph name="deck" /></span><p>{t('noDecks')}</p></div> : (
              <DeckTree decks={decks} selectedId={selectedId} onSelect={selectDeck} />
            )}
          </section>

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
                <label className="grid gap-2 text-sm font-medium text-on-surface" htmlFor="new-deck-direction">
                  {t('studyDirection')}
                  <select id="new-deck-direction" value={direction} onChange={(event) => setDirection(event.target.value as Direction)} className="min-h-12 rounded-xl border border-outline bg-surface-container-lowest px-4 text-base font-normal text-on-surface focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary">
                    <option value="reverse">{t('directionReverse')}</option><option value="forward">{t('directionForward')}</option><option value="both">{t('directionBoth')}</option>
                  </select>
                  <span className="font-normal text-on-surface-variant">{t('studyDirectionHint')}</span>
                </label>
                {createDeckMutation.isError && <p role="alert" className="m-0 text-error">{errorMessage(createDeckMutation.error)}</p>}
                <div className="flex flex-wrap gap-3">
                  <Button type="submit" disabled={createDeckMutation.isPending}>{t(createDeckMutation.isPending ? 'creatingDeck' : 'createDeck')}</Button>
                  <Button type="button" variant="tonal" onClick={() => setCreateMode(null)}>{t('cancel')}</Button>
                </div>
              </form>
            </WorkspaceDialog>

            {selectedDeck ? (
              <section className="deck-detail" aria-labelledby="selected-deck-heading">
                {!expanded && <button type="button" className="deck-back" onClick={showLibrary}><DeckGlyph name="back" />{t('backToDecks')}</button>}
                <header className="deck-detail-header">
                  <div className="deck-heading-copy">
                     <p className="deck-language"><DeckGlyph name={hasSubdecks ? 'folder' : 'deck'} />{pair ?? t('languagePairNotSet')}</p>
                    <h1 id="selected-deck-heading" ref={detailHeading} tabIndex={-1}>{selectedDeck.name}</h1>
                    {counts && <ul className="deck-count-summary">
                       <li>{wordCount(counts.notes, hasSubdecks)}</li>
                      <li>{t('deckNewCount').replace('{count}', numberFormat.format(counts.new))}</li>
                      <li>{t('deckLearningCount').replace('{count}', numberFormat.format(counts.learning))}</li>
                      <li>{t('deckDueCount').replace('{count}', numberFormat.format(counts.due))}</li>
                    </ul>}
                  </div>
                  <DeckArtwork />
                   <div className="deck-header-actions">
                     <a className="deck-study-link" href={`/study?deck=${encodeURIComponent(selectedDeck.id)}`}><DeckGlyph name="play" />{t('study')}</a>
                     {selectedDeck.canEdit !== false && <button type="button" className="workspace-icon-button deck-direction-button" aria-label={t('studyDirection')} title={t('studyDirection')} onClick={() => openPanel('direction')}><DeckGlyph name="direction" /></button>}
                     {selectedDeck.canEdit !== false && <DropdownMenu.Root>
                       <DropdownMenu.Trigger ref={deckActionsTrigger} className="workspace-icon-button deck-more-button" aria-label={t('deckActions')}><DeckGlyph name="more" /></DropdownMenu.Trigger>
                       <DropdownMenu.Portal><DropdownMenu.Content className="deck-actions-menu" align="end" sideOffset={8} collisionPadding={12}>
                         <DropdownMenu.Item onSelect={() => openPanel('rename')}><DeckGlyph name="edit" />{t('renameDeck')}</DropdownMenu.Item>
                         <DropdownMenu.Item onSelect={() => beginCreate('child')}><DeckGlyph name="subdeck" />{t('addChildDeck')}</DropdownMenu.Item>
                         <DropdownMenu.Item onSelect={() => openPanel('transfer')}><DeckGlyph name="transfer" />{t('importExport')}</DropdownMenu.Item>
                         {ownerId > 0 && <DropdownMenu.Item onSelect={() => openPanel('enrich')}><DeckGlyph name="sparkle" />{t('deckEnrichment')}</DropdownMenu.Item>}
                         <DropdownMenu.Separator />
                         <DropdownMenu.Item className="deck-action-destructive" onSelect={() => removeDeck(selectedDeck)}><DeckGlyph name="delete" />{t('deleteDeck')}</DropdownMenu.Item>
                      </DropdownMenu.Content></DropdownMenu.Portal>
                    </DropdownMenu.Root>}
                  </div>
                </header>
                {deleteDeckMutation.isError && <p role="alert" className="text-error">{errorMessage(deleteDeckMutation.error)}</p>}
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

                 {selectedDeck.canEdit !== false && <WorkspaceDialog open={panel === 'direction'} onOpenChange={closePanel} title={t('studyDirection')} returnFocusRef={taskOpener}>
                   <form onSubmit={submitDeckDirection} className="grid gap-3" aria-label={t('studyDirection')}>
                     <label className="grid gap-2 text-sm font-medium text-on-surface">
                       {t('studyDirection')}
                       <select value={direction} onChange={(event) => setDirection(event.target.value as Direction)} aria-describedby="deck-direction-hint" className="min-h-12 rounded-xl border border-outline bg-surface-container-lowest px-4 text-base font-normal text-on-surface focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary">
                         {Object.entries(directionLabels).map(([value, label]) => <option key={value} value={value}>{t(label)}</option>)}
                       </select>
                     </label>
                     <p id="deck-direction-hint" className="m-0 text-sm text-on-surface-variant">{t('studyDirectionHint')} {hasSubdecks && t('deckDirectionScope')}</p>
                     {patchDeckMutation.isError && <p role="alert" className="m-0 text-error">{errorMessage(patchDeckMutation.error)}</p>}
                     <Button type="submit" disabled={patchDeckMutation.isPending}>{t(patchDeckMutation.isPending ? 'savingDeck' : 'saveDeck')}</Button>
                   </form>
                 </WorkspaceDialog>}

                 {hasSubdecks && <section className="deck-subdecks" aria-labelledby="deck-subdecks-heading">
                   <div className="deck-words-heading"><h2 id="deck-subdecks-heading">{t('subdecks')}</h2>
                     {!showWords && selectedDeck.canEdit !== false && <Button type="button" variant="tonal" onClick={() => openPanel('add')}><DeckGlyph name="plus" />{t('addNote')}</Button>}
                   </div>
                   {hasSubdecks && <p className="deck-group-scope">{t('deckGroupScope')}</p>}
                   <ul className="deck-subdeck-list">
                     {selectedDeck.children.map((deck) => <li key={deck.id}><DeckEntry deck={deck} onSelect={selectDeck} /></li>)}
                   </ul>
                 </section>}

                 <div className={showWords ? 'deck-words' : undefined}>
                   {showWords && <>
                   <div className="deck-words-heading"><h2>{t(hasSubdecks ? 'directWords' : 'notes')}</h2>
                     {selectedDeck.canEdit !== false && <Button type="button" variant="tonal" onClick={() => openPanel('add')}><DeckGlyph name="plus" />{t('addNote')}</Button>}
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
                                 <button type="button" aria-label={`${t('deleteNote')} ${note.term}`} title={t('deleteNote')} onClick={() => removeNote(note)} className="inline-flex min-h-12 min-w-12 items-center justify-center rounded-full text-on-surface-variant hover:bg-error-container hover:text-on-error-container focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary"><DeckGlyph name="delete" /></button>
                              </div>}
                            </div>
                          )}
                        </li>
                      ))}
                    </ul>
                   ) : <div className="deck-empty">
                      <span className="deck-empty-icon"><DeckGlyph name="deck" /></span>
                     <h3>{t('noNotes')}</h3>
                       {selectedDeck.canEdit !== false && <p>{t('emptyDeckHint')}</p>}
                       {selectedDeck.canEdit !== false && <button type="button" className="deck-import-action" onClick={() => openPanel('transfer')}><DeckGlyph name="import" />{t('importDeck')}</button>}
                   </div>)}
                   {deleteNoteMutation.isError && <p role="alert" className="m-0 text-error">{errorMessage(deleteNoteMutation.error)}</p>}
                   </>}

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
            ) : expanded && <div className="deck-library-art" aria-hidden="true"><DeckArtwork /></div>}
            <WorkspaceDialog open={panel === 'transfer'} onOpenChange={closePanel} title={t('importExport')} returnFocusRef={taskOpener}>
              <ImportPanel decks={decks} selectedDeck={selectedDeck?.canEdit === false ? undefined : selectedDeck} onDirtyChange={setPanelDirty} onBusyChange={setPanelBusy} />
            </WorkspaceDialog>
          </div>
        </div>
      )}
    </section>
  );
}
