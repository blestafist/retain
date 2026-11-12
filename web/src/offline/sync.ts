import type { UserSettings } from '../api/client';
import { ApiError } from '../api/client';
import { postSync, type SyncCard, type SyncNote, type SyncResponse } from '../api/sync';
import type { ReviewInput } from '../api/study';
import {
  assertOwner,
  getSyncCursor,
  offlineDatabase,
  pendingOfflineReviews,
  resetSyncCursor,
  type CachedCard,
  type CachedNote,
  type OfflineReview,
  type QueueCacheEntry,
} from './database';
import { applyLocalReview } from './scheduling';

const reviewBatchSize = 100;
const activeSync = new Map<number, Promise<void>>();
const queuedSync = new Map<number, Promise<void>>();

export function isNetworkFailure(error: unknown): boolean {
  if (error instanceof ApiError) return false;
  return error instanceof TypeError || (typeof navigator !== 'undefined' && !navigator.onLine);
}

export function synchronizeOfflineData(ownerID: number, ownerScopeID: number, settings: UserSettings): Promise<void> {
  const queued = queuedSync.get(ownerScopeID);
  if (queued) return queued;
  const existing = activeSync.get(ownerScopeID);
  if (existing) {
    const followup = existing.catch(() => undefined).then(() => {
      queuedSync.delete(ownerScopeID);
      return synchronizeOfflineData(ownerID, ownerScopeID, settings);
    });
    queuedSync.set(ownerScopeID, followup);
    void followup.then(() => {
      if (queuedSync.get(ownerScopeID) === followup) queuedSync.delete(ownerScopeID);
    }, () => {
      if (queuedSync.get(ownerScopeID) === followup) queuedSync.delete(ownerScopeID);
    });
    return followup;
  }
  const current = runSync(ownerID, ownerScopeID, settings).finally(() => {
    if (activeSync.get(ownerScopeID) === current) activeSync.delete(ownerScopeID);
  });
  activeSync.set(ownerScopeID, current);
  return current;
}

async function runSync(ownerID: number, ownerScopeID: number, settings: UserSettings): Promise<void> {
  await assertOwner(ownerID, ownerScopeID);
  let cursor = await readCursor(ownerID, ownerScopeID);
  let stabilizing = false;
  while (true) {
    const pending = await pendingOfflineReviews(ownerID, ownerScopeID, reviewBatchSize);
    const reviews: ReviewInput[] = pending.map((review) => {
      if (!Number.isInteger(review.state_before) || review.state_before < 0 || review.state_before > 3) {
        throw new Error('Offline review has an invalid previous card state.');
      }
      return {
        id: review.id,
        card_id: review.card_id,
        rating: review.rating,
        mode: review.mode,
        reviewed_at: review.reviewed_at,
        duration_ms: review.duration_ms,
      };
    });
    let response: SyncResponse;
    try {
      response = await postSync(cursor, reviews);
    } catch (error) {
      if (!(error instanceof ApiError) || error.code !== 'invalid_cursor') throw error;
      await resetSyncCursor(ownerID, ownerScopeID);
      cursor = '';
      stabilizing = false;
      continue;
    }
    try {
      await applySyncPage(ownerID, ownerScopeID, settings, pending, response, cursor);
    } catch (error) {
      if (!(error instanceof SyncCursorConflict)) throw error;
      cursor = await readCursor(ownerID, ownerScopeID);
      stabilizing = false;
      continue;
    }
    const previousCursor = cursor;
    cursor = response.cursor;
    if (response.has_more) {
      if (cursor === previousCursor) throw new Error('The sync server returned another page without advancing its cursor.');
      stabilizing = false;
      continue;
    }
    // One empty confirmation request after draining catches changes that were
    // committed while the previous page was being applied.
    if (stabilizing && cursor === previousCursor && pending.length === 0) return;
    stabilizing = true;
  }
}

async function readCursor(ownerID: number, ownerScopeID: number): Promise<string> {
  return getSyncCursor(ownerID, ownerScopeID);
}

export async function applySyncPage(
  ownerID: number,
  ownerScopeID: number,
  settings: UserSettings,
  sentReviews: OfflineReview[],
  response: SyncResponse,
  expectedCursor?: string,
): Promise<void> {
  await offlineDatabase.transaction('rw', [
    offlineDatabase.metadata,
    offlineDatabase.profiles,
    offlineDatabase.notes,
    offlineDatabase.cards,
    offlineDatabase.queue,
    offlineDatabase.outbox,
  ], async () => {
    await assertOwner(ownerID, ownerScopeID);
    const cursorEntry = await offlineDatabase.metadata.get(`cursor:${ownerScopeID}`);
    const storedCursor = typeof cursorEntry?.value === 'string' ? cursorEntry.value : '';
    if (expectedCursor !== undefined && storedCursor !== expectedCursor) throw new SyncCursorConflict();

    const sentIDs = new Set(sentReviews.map(({ id }) => id));
    const acknowledged = response.applied.filter((id) => sentIDs.has(id));
    if (acknowledged.length > 0) await offlineDatabase.outbox.bulkDelete(acknowledged);
    for (const rejection of response.rejected) {
      if (!sentIDs.has(rejection.id)) continue;
      const review = await offlineDatabase.outbox.get(rejection.id);
      if (!review || review.owner_id !== ownerID || review.account_scope_id !== ownerScopeID) continue;
      await offlineDatabase.outbox.put({ ...review, sync_status: 'rejected', sync_error: rejection.code });
    }
    for (const note of response.changed_notes) await applyNote(note);
    for (const card of response.changed_cards) await applyCard(card, ownerID, ownerScopeID, settings);
    await offlineDatabase.metadata.put({ key: `cursor:${ownerScopeID}`, value: response.cursor });
  });
}

class SyncCursorConflict extends Error {
  constructor() {
    super('The sync cursor advanced in another browser tab.');
    this.name = 'SyncCursorConflict';
  }
}

async function applyNote(note: SyncNote): Promise<void> {
  const current = await offlineDatabase.notes.get(note.id);
  if (note.sync_seq !== undefined && (current?.sync_seq ?? -1) > note.sync_seq) return;
  if (note.deleted) {
    await offlineDatabase.notes.put({ id: note.id, sync_seq: note.sync_seq, deleted: true } as CachedNote);
    const queues = await offlineDatabase.queue.where('note_id').equals(note.id).toArray();
    await offlineDatabase.queue.bulkDelete(queues.map(({ scope, id }) => [scope, id]));
    const cards = await offlineDatabase.cards.where('note_id').equals(note.id).toArray();
    for (const card of cards) {
      await offlineDatabase.cards.put({
        id: card.id,
        user_id: card.user_id,
        deleted: true,
        sync_seq: note.sync_seq ?? card.sync_seq,
      } as CachedCard);
    }
    return;
  }

  await offlineDatabase.notes.put({ ...note, sync_seq: note.sync_seq ?? current?.sync_seq } as CachedNote);
  const entries = await offlineDatabase.queue.where('note_id').equals(note.id).toArray();
  for (const entry of entries) {
    const front = entry.card.direction === 'reverse' ? note.translation : note.term;
    const back = entry.card.direction === 'reverse' ? note.term : note.translation;
    await offlineDatabase.queue.put({
      ...entry,
      deck_id: note.deck_id,
      card: { ...entry.card, deck_id: note.deck_id, front, back },
    });
  }
}

async function applyCard(card: SyncCard, ownerID: number, ownerScopeID: number, settings: UserSettings): Promise<void> {
  const current = await offlineDatabase.cards.get(card.id);
  if (card.sync_seq !== undefined && (current?.sync_seq ?? -1) > card.sync_seq) return;
  if (current?.sync_seq !== undefined && card.sync_seq === undefined) return;
  // Review and undo responses do not carry a feed sequence. A tombstone is
  // newer than any such in-flight response; only a sequenced feed upsert can
  // explicitly restore the card.
  if (current?.deleted && card.sync_seq === undefined && !card.deleted) return;
  if (card.deleted) {
    await offlineDatabase.cards.put({
      id: card.id,
      user_id: ownerID,
      deleted: true,
      sync_seq: card.sync_seq ?? current?.sync_seq,
    } as CachedCard);
    const entries = await offlineDatabase.queue.where('id').equals(card.id).toArray();
    await offlineDatabase.queue.bulkDelete(entries.map(({ scope, id }) => [scope, id]));
    return;
  }
  {
    const note = await offlineDatabase.notes.get(card.note_id);
    if (note?.deleted && (card.sync_seq === undefined || (note.sync_seq ?? 0) >= card.sync_seq)) return;
  }
  const snapshot: CachedCard = {
    ...current,
    ...card,
    user_id: ownerID,
    deleted: false,
    sync_seq: card.sync_seq ?? current?.sync_seq,
  };
  await offlineDatabase.cards.put(snapshot);
  const entries = await offlineDatabase.queue.where('id').equals(card.id).toArray();
  const pending = (await offlineDatabase.outbox.where('card_id').equals(card.id).toArray())
    .filter((review) => review.owner_id === ownerID
      && review.account_scope_id === ownerScopeID
      && review.sync_status !== 'rejected')
    .sort((left, right) => left.reviewed_at.localeCompare(right.reviewed_at));
  for (const entry of entries) {
    let updated: QueueCacheEntry['card'] = {
      ...entry.card,
      ...card,
      deck_id: entry.deck_id,
    };
    for (const review of pending) {
      updated = applyLocalReview(updated, review.rating, new Date(review.reviewed_at), settings);
    }
    await offlineDatabase.queue.put({ ...entry, card: updated });
  }
}

export async function cacheServerCard(
  ownerID: number,
  ownerScopeID: number,
  card: SyncCard,
  settings: UserSettings,
): Promise<void> {
  await offlineDatabase.transaction('rw', [
    offlineDatabase.metadata,
    offlineDatabase.profiles,
    offlineDatabase.notes,
    offlineDatabase.cards,
    offlineDatabase.queue,
    offlineDatabase.outbox,
  ], async () => {
    await assertOwner(ownerID, ownerScopeID);
    await applyCard(card, ownerID, ownerScopeID, settings);
  });
}

export async function saveOfflineReview(
  ownerID: number,
  ownerScopeID: number,
  review: OfflineReview,
  settings: UserSettings,
): Promise<void> {
  await offlineDatabase.transaction('rw', [offlineDatabase.metadata, offlineDatabase.profiles, offlineDatabase.cards, offlineDatabase.queue, offlineDatabase.outbox], async () => {
    await assertOwner(ownerID, ownerScopeID);
    if (review.owner_id !== ownerID || review.account_scope_id !== ownerScopeID) {
      throw new Error('Offline review belongs to a different account.');
    }
    const current = await offlineDatabase.cards.get(review.card_id);
    if (!current || current.deleted || current.suspended) throw new Error('Offline card is unavailable.');
    const entries = await offlineDatabase.queue.where('id').equals(review.card_id).toArray();
    const firstEntry = entries[0];
    if (!firstEntry) throw new Error('Offline study card is no longer in the saved queue.');
    const baseCard = firstEntry.card;
    const nextCard = applyLocalReview(baseCard, review.rating, new Date(review.reviewed_at), settings);
    await offlineDatabase.outbox.add({ ...review, state_before: baseCard.state });
    await offlineDatabase.cards.put({
      ...current,
      ...nextCard,
      user_id: ownerID,
      suspended: false,
      is_leech: current.is_leech,
      deleted: false,
    });
    for (const entry of entries) {
      await offlineDatabase.queue.put({
        ...entry,
        card: { ...entry.card, ...nextCard, front: entry.card.front, back: entry.card.back },
      });
    }
  });
}
