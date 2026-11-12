import Dexie, { type Table } from 'dexie';
import type { MeResponse, UserSettings } from '../api/client';
import type { QueueCard, ReviewInput, StudyCard } from '../api/study';
import { applyLocalReview } from './scheduling';

export interface CachedNote {
  id: number;
  sync_seq?: number;
  deck_id: number;
  term: string;
  translation: string;
  transcription: string | null;
  example: string | null;
  example_translation: string | null;
  notes: string | null;
  tags: string[];
  alternatives: string[];
  fields: Record<string, unknown>;
  updated_at: string;
  deleted: boolean;
}

export interface CachedCard extends StudyCard {
  user_id: number;
  suspended: boolean;
  is_leech: boolean;
  deleted: boolean;
}

export interface OfflineReview extends ReviewInput {
  owner_id: number;
  account_scope_id: number;
  state_before: number;
  sync_status?: 'rejected';
  sync_error?: string;
}

export interface QueueCacheEntry {
  id: number;
  note_id: number;
  deck_id: number;
  scope: string;
  position: number;
  card: QueueCard;
}

interface MetadataEntry {
  key: string;
  value: unknown;
}

interface ProfileEntry {
  key: 'current';
  profile: MeResponse;
}

class RetainOfflineDatabase extends Dexie {
  notes!: Table<CachedNote, number>;
  cards!: Table<CachedCard, number>;
  queue!: Table<QueueCacheEntry, [string, number]>;
  outbox!: Table<OfflineReview, string>;
  metadata!: Table<MetadataEntry, string>;
  profiles!: Table<ProfileEntry, string>;

  constructor() {
    super('retain-offline');
    this.version(1).stores({
      notes: 'id, deck_id',
      cards: 'id, note_id, user_id, updated_at',
      queue: '[scope+id], id, note_id, deck_id, scope, position',
      outbox: 'id, owner_id, card_id, reviewed_at, [owner_id+reviewed_at]',
      metadata: 'key',
      profiles: 'key',
    });
  }
}

export const offlineDatabase = new RetainOfflineDatabase();

export function queueScope(ownerID: number, deckID: number | string | null): string {
  return `${ownerID}:${deckID === null ? '' : String(deckID)}`;
}

export async function prepareOfflineOwner(profile: MeResponse): Promise<void> {
  await offlineDatabase.transaction('rw', [
    offlineDatabase.notes,
    offlineDatabase.cards,
    offlineDatabase.queue,
    offlineDatabase.outbox,
    offlineDatabase.metadata,
    offlineDatabase.profiles,
  ], async () => {
    const owner = await offlineDatabase.metadata.get('owner');
    if (owner?.value !== profile.user.sync_scope) await clearCachedStores();
    await offlineDatabase.metadata.put({ key: 'owner', value: profile.user.sync_scope });
    await offlineDatabase.profiles.put({ key: 'current', profile });
  });
}

export async function getCachedProfile(): Promise<MeResponse | null> {
  const [owner, profile] = await Promise.all([
    offlineDatabase.metadata.get('owner'),
    offlineDatabase.profiles.get('current'),
  ]);
  if (!profile || owner?.value !== profile.profile.user.sync_scope) return null;
  return profile.profile;
}

export async function updateCachedSettings(settings: UserSettings): Promise<void> {
  await offlineDatabase.transaction('rw', [offlineDatabase.metadata, offlineDatabase.profiles], async () => {
    const profile = await offlineDatabase.profiles.get('current');
    if (!profile) return;
    const owner = await offlineDatabase.metadata.get('owner');
    if (owner?.value !== profile.profile.user.sync_scope) return;
    await offlineDatabase.profiles.put({ key: 'current', profile: { ...profile.profile, settings } });
  });
}

export async function clearOfflineData(): Promise<void> {
  await offlineDatabase.transaction('rw', [
    offlineDatabase.notes,
    offlineDatabase.cards,
    offlineDatabase.queue,
    offlineDatabase.metadata,
    offlineDatabase.profiles,
  ], clearCachedStores);
}

async function clearCachedStores(): Promise<void> {
  await Promise.all([
    offlineDatabase.notes.clear(),
    offlineDatabase.cards.clear(),
    offlineDatabase.queue.clear(),
    offlineDatabase.metadata.clear(),
    offlineDatabase.profiles.clear(),
  ]);
}

export async function cacheStudyQueue(
  ownerID: number,
  ownerScopeID: number,
  deckID: number | string | null,
  cards: QueueCard[],
  settings: UserSettings,
): Promise<QueueCard[]> {
  const scope = queueScope(ownerID, deckID);
  return offlineDatabase.transaction('rw', [
    offlineDatabase.metadata,
    offlineDatabase.profiles,
    offlineDatabase.cards,
    offlineDatabase.queue,
    offlineDatabase.notes,
    offlineDatabase.outbox,
  ], async () => {
    await assertOwner(ownerID, ownerScopeID);
    const existing = await offlineDatabase.queue.where('scope').equals(scope).toArray();
    let position = existing.reduce((maximum, entry) => Math.max(maximum, entry.position), -1) + 1;
    const mergedCards: QueueCard[] = [];
    for (const card of cards) {
      const previous = await offlineDatabase.queue.get([scope, card.id]);
      const previousCard = await offlineDatabase.cards.get(card.id);
      const cachedNote = await offlineDatabase.notes.get(card.note_id);
      // Queue responses are unversioned. If a sync tombstone landed while
      // this request was in flight, never let its stale result revive data.
      if (cachedNote?.deleted || previousCard?.deleted) continue;
      const pending = (await offlineDatabase.outbox.where('card_id').equals(card.id).toArray())
        .filter((review) => review.owner_id === ownerID
          && review.account_scope_id === ownerScopeID
          && review.sync_status !== 'rejected')
        .sort((left, right) => left.reviewed_at.localeCompare(right.reviewed_at));
      let mergedCard: QueueCard = card;
      let usesCachedScheduling = false;
      if (previousCard && (card.sync_seq === undefined || (previousCard.sync_seq ?? -1) > card.sync_seq)) {
        // The queue API has no feed sequence. Keep the locally newest known
        // scheduling snapshot while refreshing prompt text and previews.
        mergedCard = {
          ...card,
          ...previousCard,
          front: card.front,
          back: card.back,
          previews: card.previews,
        };
        usesCachedScheduling = true;
      }
      if (!usesCachedScheduling) {
        for (const review of pending) {
          mergedCard = applyLocalReview(mergedCard, review.rating, new Date(review.reviewed_at), settings);
        }
      }
      const entry: QueueCacheEntry = {
        id: card.id,
        note_id: card.note_id,
        deck_id: card.deck_id ?? 0,
        scope,
        position: previous?.scope === scope ? previous.position : position++,
        card: mergedCard,
      };
      await offlineDatabase.queue.put(entry);
      await offlineDatabase.cards.put({
        ...previousCard,
        ...mergedCard,
        user_id: ownerID,
        suspended: false,
        is_leech: false,
        deleted: false,
        sync_seq: mergedCard.sync_seq ?? previousCard?.sync_seq,
      });
      if (pending.length === 0 || mergedCard.state === 0 || Date.parse(mergedCard.due) <= Date.now()) {
        mergedCards.push(mergedCard);
      }
    }
    return mergedCards;
  });
}

export async function getCachedQueue(
  ownerID: number,
  ownerScopeID: number,
  deckID: number | string | null,
  limit: number,
  now = new Date(),
): Promise<QueueCard[]> {
  if (limit <= 0) return [];
  await assertOwner(ownerID, ownerScopeID);
  const scope = queueScope(ownerID, deckID);
  const entries = await offlineDatabase.queue.where('scope').equals(scope).sortBy('position');
  const result: QueueCard[] = [];
  for (const entry of entries) {
    const card = await offlineDatabase.cards.get(entry.id);
    const note = await offlineDatabase.notes.get(entry.note_id);
    if (card?.deleted || card?.suspended || note?.deleted) continue;
    const current = entry.card;
    if (current.state !== 0 && Date.parse(current.due) > now.getTime()) continue;
    result.push(current);
    if (result.length >= limit) break;
  }
  return result;
}

export async function pendingOfflineReviews(ownerID: number, ownerScopeID: number, limit: number): Promise<OfflineReview[]> {
  await assertOwner(ownerID, ownerScopeID);
  const reviews = await offlineDatabase.outbox.where('owner_id').equals(ownerID).toArray();
  return reviews
    .filter((review) => review.account_scope_id === ownerScopeID && review.sync_status !== 'rejected')
    .sort((left, right) => left.reviewed_at.localeCompare(right.reviewed_at))
    .slice(0, limit);
}

export async function rejectedOfflineReviews(ownerID: number, ownerScopeID: number): Promise<OfflineReview[]> {
  await assertOwner(ownerID, ownerScopeID);
  const reviews = await offlineDatabase.outbox.where('owner_id').equals(ownerID).toArray();
  return reviews.filter((review) => review.account_scope_id === ownerScopeID && review.sync_status === 'rejected')
    .sort((left, right) => left.reviewed_at.localeCompare(right.reviewed_at));
}

export async function discardRejectedOfflineReview(ownerID: number, ownerScopeID: number, id: string): Promise<void> {
  await offlineDatabase.transaction('rw', [offlineDatabase.metadata, offlineDatabase.profiles, offlineDatabase.outbox], async () => {
    await assertOwner(ownerID, ownerScopeID);
    const review = await offlineDatabase.outbox.get(id);
    if (!review || review.owner_id !== ownerID || review.account_scope_id !== ownerScopeID || review.sync_status !== 'rejected') {
      throw new Error('Rejected offline review is unavailable.');
    }
    await offlineDatabase.outbox.delete(id);
  });
}

export async function getSyncCursor(ownerID: number, ownerScopeID: number): Promise<string> {
  await assertOwner(ownerID, ownerScopeID);
  const value = await offlineDatabase.metadata.get(`cursor:${ownerScopeID}`);
  return typeof value?.value === 'string' ? value.value : '';
}

export async function resetSyncCursor(ownerID: number, ownerScopeID: number): Promise<void> {
  await offlineDatabase.transaction('rw', [offlineDatabase.metadata, offlineDatabase.profiles], async () => {
    await assertOwner(ownerID, ownerScopeID);
    await offlineDatabase.metadata.delete(`cursor:${ownerScopeID}`);
  });
}

export async function assertOwner(ownerID: number, ownerScopeID: number): Promise<void> {
  const owner = await offlineDatabase.metadata.get('owner');
  const profile = await offlineDatabase.profiles.get('current');
  if (profile?.profile.user.id !== ownerID
    || profile.profile.user.sync_scope !== ownerScopeID
    || owner?.value !== ownerScopeID) {
    throw new Error('Offline data belongs to a different account.');
  }
}

export async function isOfflineCardTombstoned(
  ownerID: number,
  ownerScopeID: number,
  cardID: number,
  noteID: number,
): Promise<boolean> {
  await assertOwner(ownerID, ownerScopeID);
  const [card, note] = await Promise.all([
    offlineDatabase.cards.get(cardID),
    offlineDatabase.notes.get(noteID),
  ]);
  return Boolean(card?.deleted || note?.deleted);
}
