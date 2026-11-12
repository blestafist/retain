import { useEffect, useRef, useState, type FormEvent } from 'react';
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
import { DeckEnrichmentPanel } from './DeckEnrichmentPanel';
import { ImportPanel } from './ImportPanel';
import { WordAutoFill, type SuggestionKey } from './WordAutoFill';
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

function DeckTree({
  decks,
  selectedId,
  onSelect,
  onDelete,
  depth = 0,
}: {
  decks: DeckNode[];
  selectedId: string | null;
  onSelect: (deck: DeckNode) => void;
  onDelete: (deck: DeckNode) => void;
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
            <div className="flex items-center gap-1" style={{ paddingInlineStart: `${Math.min(depth, 6) * 0.75}rem` }}>
              <button
                type="button"
                aria-pressed={selected}
                onClick={() => onSelect(deck)}
                className={`min-h-12 min-w-0 flex-1 rounded-2xl px-4 text-start text-on-surface focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary ${selected ? 'bg-secondary-container text-on-secondary-container' : 'hover:bg-surface-container-high'}`}
              >
                <span className="block truncate">{deck.name}</span>
                {counts && <span className="block truncate text-sm opacity-75">{t('deckCounts').replace('{notes}', String(counts.notes)).replace('{new}', String(counts.new)).replace('{learning}', String(counts.learning)).replace('{due}', String(counts.due))}</span>}
              </button>
              <button
                type="button"
                aria-label={`${t('deleteDeck')} ${deck.name}`}
                title={t('deleteDeck')}
                onClick={() => onDelete(deck)}
                className="inline-flex min-h-12 min-w-12 items-center justify-center rounded-full text-on-surface-variant hover:bg-error-container hover:text-on-error-container focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
              >
                <DeleteIcon />
              </button>
            </div>
            <DeckTree decks={deck.children} selectedId={selectedId} onSelect={onSelect} onDelete={onDelete} depth={depth + 1} />
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
      <div className="grid gap-4 sm:grid-cols-2">
        <label className="grid gap-2 text-sm font-medium text-on-surface" htmlFor={fieldId('term')}>
          {t('term')}
          <input id={fieldId('term')} name="term" required defaultValue={note?.term ?? ''} onChange={onTermChange ? (event) => onTermChange(event.target.value) : undefined} className="min-h-12 rounded-xl border border-outline bg-surface-container-lowest px-4 text-base font-normal text-on-surface focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary" />
        </label>
        <label className="grid gap-2 text-sm font-medium text-on-surface" htmlFor={fieldId('translation')}>
          {t('translation')}
          <input id={fieldId('translation')} name="translation" required defaultValue={note?.translation ?? ''} className="min-h-12 rounded-xl border border-outline bg-surface-container-lowest px-4 text-base font-normal text-on-surface focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary" />
        </label>
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
        <label className="grid gap-2 text-sm font-medium text-on-surface sm:col-span-2" htmlFor={fieldId('alternatives')}>
          {t('alternatives')}
          <input id={fieldId('alternatives')} name="alternatives" defaultValue={note?.alternatives?.join(', ') ?? ''} aria-describedby={fieldId('alternatives-hint')} className="min-h-12 rounded-xl border border-outline bg-surface-container-lowest px-4 text-base font-normal text-on-surface focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary" />
          <span id={fieldId('alternatives-hint')} className="font-normal text-on-surface-variant">{t('commaSeparatedHint')}</span>
        </label>
      </div>
      <label className="grid gap-2 text-sm font-medium text-on-surface" htmlFor={fieldId('notes')}>
        {t('notes')}
        <textarea id={fieldId('notes')} name="notes" rows={3} defaultValue={note?.notes ?? ''} className="rounded-xl border border-outline bg-surface-container-lowest px-4 py-3 text-base font-normal text-on-surface focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary" />
      </label>
    </>
  );
}

export function DecksWorkspace({ ownerId = 0 }: { ownerId?: number }) {
  const decksQuery = useDecks();
  const llmStatusQuery = useLlmStatus();
  const decks = decksQuery.data ?? [];
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [createMode, setCreateMode] = useState<CreateMode>(null);
  const [targetLanguage, setTargetLanguage] = useState<TargetLanguage>('de');
  const [nativeLanguage, setNativeLanguage] = useState<NativeLanguage>('pl');
  const [reverseCards, setReverseCards] = useState(false);
  const [editingNote, setEditingNote] = useState<DeckId | null>(null);
  const [newNoteTerm, setNewNoteTerm] = useState('');
  const [newNoteFields, setNewNoteFields] = useState<Record<string, unknown>>({});
  const newNoteForm = useRef<HTMLFormElement>(null);
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
    }
    if (!selectedId && decks[0]) setSelectedId(String(decks[0].id));
  }, [decks, selectedId]);

  function beginCreate(mode: Exclude<CreateMode, null>) {
    createDeckMutation.reset();
    setReverseCards(false);
    setCreateMode(mode);
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
    if (name) patchDeckMutation.mutate({ id: selectedDeck.id, input: { name } });
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
    <section className="mx-auto w-full max-w-6xl space-y-6 py-8" aria-labelledby="decks-workspace-heading">
      <header className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <p className="m-0 text-sm font-medium uppercase tracking-wide text-primary">{t('decks')}</p>
          <h1 id="decks-workspace-heading" className="m-0 text-3xl font-semibold text-on-surface">{t('decksWorkspace')}</h1>
        </div>
        <Button type="button" variant="tonal" onClick={() => beginCreate('root')}>{t('newTopLevelDeck')}</Button>
      </header>

      {decksQuery.isPending && <p role="status" className="text-on-surface-variant">{t('loadingDecks')}</p>}
      {decksQuery.isError && (
        <div role="alert" className="space-y-3 rounded-2xl bg-error-container p-4 text-on-error-container">
          <p className="m-0">{errorMessage(decksQuery.error)}</p>
          <Button type="button" variant="tonal" onClick={() => void decksQuery.refetch()}>{t('tryAgain')}</Button>
        </div>
      )}

      {!decksQuery.isPending && !decksQuery.isError && (
        <div className="grid gap-5 expanded:grid-cols-[minmax(16rem,0.85fr)_minmax(0,1.5fr)]">
          <aside className="space-y-4 rounded-3xl bg-surface-container-low p-4 expanded:p-5" aria-labelledby="deck-list-heading">
            <div className="flex items-center justify-between gap-3">
              <h2 id="deck-list-heading" className="m-0 text-xl font-semibold text-on-surface">{t('yourDecks')}</h2>
              {decks.length > 0 && <Button type="button" variant="tonal" onClick={() => beginCreate('child')} disabled={!selectedDeck}>{t('addChildDeck')}</Button>}
            </div>
            {decks.length === 0 ? <p className="m-0 py-3 text-on-surface-variant">{t('noDecks')}</p> : (
              <DeckTree decks={decks} selectedId={selectedId} onSelect={(deck) => { setSelectedId(String(deck.id)); setEditingNote(null); }} onDelete={removeDeck} />
            )}
            {deleteDeckMutation.isError && <p role="alert" className="m-0 text-error">{errorMessage(deleteDeckMutation.error)}</p>}
          </aside>

          <div className="min-w-0 space-y-5">
            {createMode && (
              <form onSubmit={submitDeck} className="space-y-4 rounded-3xl bg-surface-container-low p-5" aria-labelledby="create-deck-heading">
                <h2 id="create-deck-heading" className="m-0 text-xl font-semibold text-on-surface">{t(createMode === 'root' ? 'newTopLevelDeck' : 'addChildDeck')}</h2>
                <label className="grid gap-2 text-sm font-medium text-on-surface" htmlFor="new-deck-name">
                  {t('deckName')}
                  <input id="new-deck-name" name="name" required autoFocus className="min-h-12 rounded-xl border border-outline bg-surface-container-lowest px-4 text-base font-normal text-on-surface focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary" />
                </label>
                {createMode === 'root' && (
                  <div className="grid gap-4 sm:grid-cols-2">
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
            )}

            {selectedDeck ? (
              <section className="space-y-5 rounded-3xl bg-surface-container-low p-5 expanded:p-6" aria-labelledby="selected-deck-heading">
                <header className="space-y-1">
                  <p className="m-0 text-sm text-on-surface-variant">{t('selectedDeck')}</p>
                  <h2 id="selected-deck-heading" className="m-0 break-words text-2xl font-semibold text-on-surface">{selectedDeck.name}</h2>
                  <p className="m-0 text-sm text-on-surface-variant">{pair ?? t('languagePairNotSet')}</p>
                </header>

                <form onSubmit={submitDeckRename} className="grid gap-3 border-t border-outline-variant pt-4 sm:grid-cols-[minmax(0,1fr)_auto]" aria-label={t('renameDeck')}>
                  <label className="grid gap-2 text-sm font-medium text-on-surface" htmlFor="rename-deck">
                    {t('deckName')}
                    <input id="rename-deck" name="name" required defaultValue={selectedDeck.name} key={String(selectedDeck.id) + selectedDeck.name} className="min-h-12 rounded-xl border border-outline bg-surface-container-lowest px-4 text-base font-normal text-on-surface focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary" />
                  </label>
                  <Button type="submit" className="self-end" disabled={patchDeckMutation.isPending}>{t(patchDeckMutation.isPending ? 'savingDeck' : 'saveDeck')}</Button>
                </form>
                {patchDeckMutation.isError && <p role="alert" className="m-0 text-error">{errorMessage(patchDeckMutation.error)}</p>}
                {patchDeckMutation.isSuccess && <p role="status" className="m-0 text-on-surface-variant">{t('deckSaved')}</p>}

                <div className="space-y-4 border-t border-outline-variant pt-5">
                  <h3 className="m-0 text-lg font-semibold text-on-surface">{t('notes')}</h3>
                  {notesQuery.isPending && <p role="status" className="m-0 text-on-surface-variant">{t('loadingNotes')}</p>}
                  {notesQuery.isError && <p role="alert" className="m-0 text-error">{errorMessage(notesQuery.error)}</p>}
                  {notesQuery.isSuccess && (notesQuery.data.length ? (
                    <ul className="m-0 list-none space-y-2 p-0" aria-label={t('noteList')}>
                      {notesQuery.data.map((note) => (
                        <li key={String(note.id)} className="rounded-2xl bg-surface-container p-4">
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
                            <div className="flex flex-wrap items-start justify-between gap-3">
                              <div className="min-w-0">
                                <p className="m-0 break-words font-medium text-on-surface">{note.term}</p>
                                <p className="m-0 break-words text-on-surface-variant">{note.translation}</p>
                                {note.example && <p className="m-0 mt-2 break-words text-sm text-on-surface-variant">{note.example}</p>}
                                {note.tags && note.tags.length > 0 && <p className="m-0 mt-2 break-words text-sm text-on-surface-variant">{t('tags')}: {note.tags.join(', ')}</p>}
                              </div>
                              <div className="flex gap-2">
                                <Button type="button" variant="tonal" onClick={() => setEditingNote(note.id)}>{t('edit')}</Button>
                                <button type="button" aria-label={`${t('deleteNote')} ${note.term}`} title={t('deleteNote')} onClick={() => removeNote(note)} className="inline-flex min-h-12 min-w-12 items-center justify-center rounded-full text-on-surface-variant hover:bg-error-container hover:text-on-error-container focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary"><DeleteIcon /></button>
                              </div>
                            </div>
                          )}
                        </li>
                      ))}
                    </ul>
                  ) : <p className="m-0 text-on-surface-variant">{t('noNotes')}</p>)}
                  {(createNoteMutation.isError || deleteNoteMutation.isError) && <p role="alert" className="m-0 text-error">{errorMessage(createNoteMutation.error ?? deleteNoteMutation.error)}</p>}

                  <details className="rounded-2xl bg-surface-container p-4">
                    <summary className="min-h-12 cursor-pointer content-center font-semibold text-on-surface">{t('addNote')}</summary>
                    <form ref={newNoteForm} onSubmit={submitNote} className="space-y-4 pt-4" aria-label={t('addNote')}>
                      <NoteFields prefix="new-note" onTermChange={setNewNoteTerm} />
                      {llmStatusQuery.data?.enabled && typeof selectedDeck.id === 'number' && (
                        <WordAutoFill deckId={selectedDeck.id} word={newNoteTerm} enabled onUse={useWordSuggestion} />
                      )}
                      <Button type="submit" disabled={createNoteMutation.isPending}>{t(createNoteMutation.isPending ? 'addingNote' : 'addNote')}</Button>
                    </form>
                  </details>
                </div>

                <ImportPanel decks={decks} selectedDeck={selectedDeck} />
                {ownerId > 0 && (
                  <DeckEnrichmentPanel
                    ownerId={ownerId}
                    deckId={selectedDeck.id}
                    deckName={selectedDeck.name}
                    enabled={llmStatusQuery.data?.enabled === true}
                  />
                )}
              </section>
            ) : (
              !createMode && <section className="rounded-3xl bg-surface-container-low p-6"><h2 className="m-0 text-xl font-semibold text-on-surface">{t('selectOrCreateDeck')}</h2></section>
            )}
          </div>
        </div>
      )}
    </section>
  );
}
