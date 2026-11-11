import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { applyImport, exportDeck, getImportHistory, previewImport, type ApplyImportInput } from '../api/import';
import {
  createDeck,
  createDeckNote,
  deleteDeck,
  deleteNote,
  getDeckNotes,
  getDecks,
  patchDeck,
  patchNote,
  type CreateDeckRequest,
  type CreateNoteRequest,
  type DeckId,
  type PatchDeckRequest,
  type PatchNoteRequest,
} from '../api/content';

export const decksQueryKey = ['decks'] as const;
export const deckNotesQueryKey = ['deck-notes'] as const;
export const importHistoryQueryKey = ['import-history'] as const;

export function useDecks() {
  return useQuery({ queryKey: decksQueryKey, queryFn: getDecks });
}

export function useDeckNotes(deckId: DeckId | null) {
  return useQuery({
    queryKey: [...deckNotesQueryKey, deckId === null ? null : String(deckId)],
    queryFn: () => {
      if (deckId === null) throw new Error('A deck must be selected to load notes.');
      return getDeckNotes(deckId);
    },
    enabled: deckId !== null,
  });
}

export function useCreateDeck() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: CreateDeckRequest) => createDeck(input),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: decksQueryKey }),
  });
}

export function usePatchDeck() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, input }: { id: DeckId; input: PatchDeckRequest }) => patchDeck(id, input),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: decksQueryKey }),
  });
}

export function useDeleteDeck() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: DeckId) => deleteDeck(id),
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: decksQueryKey }),
        queryClient.invalidateQueries({ queryKey: deckNotesQueryKey }),
      ]);
    },
  });
}

export function useCreateDeckNote() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ deckId, input }: { deckId: DeckId; input: CreateNoteRequest }) => createDeckNote(deckId, input),
    onSuccess: async (_data, variables) => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: [...deckNotesQueryKey, String(variables.deckId)] }),
        queryClient.invalidateQueries({ queryKey: decksQueryKey }),
      ]);
    },
  });
}

export function usePatchNote() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, input }: { deckId: DeckId; id: DeckId; input: PatchNoteRequest }) => patchNote(id, input),
    onSuccess: async (_data, variables) => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: [...deckNotesQueryKey, String(variables.deckId)] }),
        queryClient.invalidateQueries({ queryKey: decksQueryKey }),
      ]);
    },
  });
}

export function useDeleteNote() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id }: { deckId: DeckId; id: DeckId }) => deleteNote(id),
    onSuccess: async (_data, variables) => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: [...deckNotesQueryKey, String(variables.deckId)] }),
        queryClient.invalidateQueries({ queryKey: decksQueryKey }),
      ]);
    },
  });
}

export function useImportHistory() {
  return useQuery({ queryKey: importHistoryQueryKey, queryFn: getImportHistory });
}

export function usePreviewImport() {
  return useMutation({ mutationFn: (file: File) => previewImport(file) });
}

export function useApplyImport() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ batchId, input }: { batchId: string; input: ApplyImportInput }) => applyImport(batchId, input),
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: decksQueryKey }),
        queryClient.invalidateQueries({ queryKey: deckNotesQueryKey }),
        queryClient.invalidateQueries({ queryKey: importHistoryQueryKey }),
      ]);
    },
  });
}

export function useExportDeck() {
  return useMutation({ mutationFn: ({ deckId, format }: { deckId: DeckId; format: 'retain' | 'csv' }) => exportDeck(deckId, format) });
}
