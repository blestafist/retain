// @vitest-environment jsdom
import 'fake-indexeddb/auto';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { MeResponse, UserSettings } from '../api/client';
import type { QueueCard } from '../api/study';
import type { SyncCard, SyncResponse } from '../api/sync';
import {
  cacheStudyQueue,
  discardRejectedOfflineReview,
  getCachedQueue,
  getSyncCursor,
  offlineDatabase,
  pendingOfflineReviews,
  prepareOfflineOwner,
  rejectedOfflineReviews,
  queueScope,
  type OfflineReview,
} from './database';
import { applyLocalReview } from './scheduling';
import { applySyncPage, cacheServerCard, saveOfflineReview, synchronizeOfflineData } from './sync';

const settings: UserSettings = {
  new_per_day: 20,
  max_reviews_per_day: 200,
  request_retention: 0.9,
  maximum_interval: 36500,
  learning_steps: '1m 10m',
  relearning_steps: '10m',
  fsrs_weights: null,
  timezone: 'UTC',
  day_start_hour: 4,
  reminder_time: null,
  tts_auto: false,
  theme_mode: 'system',
  theme_seed: '#3A4527',
  theme_variant: 'fidelity',
  theme_contrast: 0,
};

function profile(userID = 1, scopeID = userID): MeResponse {
  return {
    user: { id: userID, sync_scope: scopeID, email: `user-${userID}@example.test`, name: 'Offline learner' },
    settings,
  };
}

const queueCard: QueueCard = {
  id: 5,
  note_id: 8,
  deck_id: 2,
  direction: 'forward',
  state: 0,
  due: '2026-04-03T12:00:00.000Z',
  elapsed_days: 0,
  scheduled_days: 0,
  step: 0,
  reps: 0,
  lapses: 0,
  front: 'hello',
  back: 'hallo',
  previews: Object.fromEntries([1, 2, 3, 4].map((rating) => [rating, {
    state: 1,
    due: '2026-04-03T12:01:00.000Z',
    scheduled_days: 0,
    interval: '1m0s',
  }])) as QueueCard['previews'],
};

const review: OfflineReview = {
  id: '018f0f3e-7b00-7000-8000-000000000001',
  owner_id: 1,
  account_scope_id: 1,
  card_id: queueCard.id,
  rating: 3,
  mode: 'flip',
  reviewed_at: '2026-04-03T12:00:00.000Z',
  duration_ms: 500,
  state_before: 0,
};

beforeEach(async () => {
  offlineDatabase.close();
  await offlineDatabase.delete();
  await offlineDatabase.open();
  await prepareOfflineOwner(profile());
});

afterEach(() => {
  vi.unstubAllGlobals();
  offlineDatabase.close();
});

describe('offline sync storage', () => {
  it('runs a follow-up feed sync when a mutation requests sync during an active run', async () => {
    let releaseFirst: ((response: Response) => void) | undefined;
    const firstResponse = new Promise<Response>((resolve) => { releaseFirst = resolve; });
    let requests = 0;
    const noChanges = {
      applied: [],
      rejected: [],
      changed_cards: [],
      changed_notes: [],
      cursor: 'stable-cursor',
      has_more: false,
    };
    const fetchMock = vi.fn<typeof fetch>(() => {
      requests += 1;
      return requests === 1
        ? firstResponse
        : Promise.resolve(new Response(JSON.stringify(noChanges), { headers: { 'Content-Type': 'application/json' } }));
    });
    vi.stubGlobal('fetch', fetchMock);

    const backgroundSync = synchronizeOfflineData(1, 1, settings);
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const mutationSync = synchronizeOfflineData(1, 1, settings);
    releaseFirst?.(new Response(JSON.stringify(noChanges), { headers: { 'Content-Type': 'application/json' } }));
    await Promise.all([backgroundSync, mutationSync]);

    expect(fetchMock).toHaveBeenCalledTimes(4);
  });

  it('commits authoritative cards, acknowledged outbox IDs, and cursor in one page application', async () => {
    await cacheStudyQueue(1, 1, null, [queueCard], settings);
    const localCard = applyLocalReview(queueCard, 3, new Date(review.reviewed_at), settings);
    await saveOfflineReview(1, 1, review, settings);

    const serverCard: SyncCard = {
      ...localCard,
      state: 1,
      due: '2026-04-03T12:10:00.000Z',
      stability: 0.4,
      difficulty: 5,
      elapsed_days: 0,
      scheduled_days: 0,
      step: 0,
      reps: 1,
      last_review: review.reviewed_at,
      user_id: 1,
      suspended: false,
      is_leech: false,
      deleted: false,
    };
    const response: SyncResponse = {
      applied: [review.id],
      rejected: [],
      changed_cards: [serverCard],
      changed_notes: [],
      cursor: 'signed-cursor-1',
      has_more: false,
    };

    await applySyncPage(1, 1, settings, [review], response);

    expect(await offlineDatabase.outbox.count()).toBe(0);
    expect(await getSyncCursor(1, 1)).toBe(response.cursor);
    expect((await offlineDatabase.queue.get([queueScope(1, null), queueCard.id]))?.card.state).toBe(serverCard.state);
    expect(await getCachedQueue(1, 1, null, 50, new Date(review.reviewed_at))).toEqual([]);
  });

  it('keeps separately scoped queue copies for all decks and the all-decks queue', async () => {
    await cacheStudyQueue(1, 1, null, [queueCard], settings);
    await cacheStudyQueue(1, 1, 2, [queueCard], settings);

    expect(await getCachedQueue(1, 1, null, 10, new Date(review.reviewed_at))).toHaveLength(1);
    expect(await getCachedQueue(1, 1, 2, 10, new Date(review.reviewed_at))).toHaveLength(1);
  });

  it('keeps unsent local scheduling when caching a stale server queue response', async () => {
    await cacheStudyQueue(1, 1, null, [queueCard], settings);
    const recentReview = { ...review, reviewed_at: new Date().toISOString() };
    await saveOfflineReview(1, 1, recentReview, settings);

    const refreshed = await cacheStudyQueue(1, 1, null, [queueCard], settings);

    expect(refreshed).toEqual([]);
    expect((await offlineDatabase.queue.get([queueScope(1, null), queueCard.id]))?.card.state).toBe(1);
    expect(await pendingOfflineReviews(1, 1, 10)).toHaveLength(1);
  });

  it('reapplies local reviews that were added while an authoritative page was in flight', async () => {
    await cacheStudyQueue(1, 1, null, [queueCard], settings);
    const localCard = applyLocalReview(queueCard, 3, new Date(review.reviewed_at), settings);
    await saveOfflineReview(1, 1, review, settings);
    const serverSnapshot: SyncCard = {
      ...queueCard,
      user_id: 1,
      suspended: false,
      is_leech: false,
      deleted: false,
    };

    await applySyncPage(1, 1, settings, [], {
      applied: [],
      rejected: [],
      changed_cards: [serverSnapshot],
      changed_notes: [],
      cursor: 'new-server-page',
      has_more: false,
    });

    expect((await offlineDatabase.queue.get([queueScope(1, null), queueCard.id]))?.card.state).toBe(localCard.state);
    expect(await pendingOfflineReviews(1, 1, 10)).toHaveLength(1);
  });

  it('does not let an older card event overwrite a newer rejected-review snapshot', async () => {
    await cacheStudyQueue(1, 1, null, [queueCard], settings);
    const newest: SyncCard = {
      ...queueCard,
      state: 2,
      sync_seq: 20,
      user_id: 1,
      suspended: false,
      is_leech: false,
      deleted: false,
    };
    const older: SyncCard = {
      ...queueCard,
      state: 1,
      sync_seq: 10,
      user_id: 1,
      suspended: false,
      is_leech: false,
      deleted: false,
    };

    await applySyncPage(1, 1, settings, [], {
      applied: [],
      rejected: [],
      changed_cards: [newest, older],
      changed_notes: [],
      cursor: 'ordered-card-events',
      has_more: true,
    });

    expect(await offlineDatabase.cards.get(queueCard.id)).toMatchObject({ state: 2, sync_seq: 20 });
    expect((await offlineDatabase.queue.get([queueScope(1, null), queueCard.id]))?.card.state).toBe(2);
  });

  it('does not let unversioned queue, review, or undo responses overwrite a sequenced card', async () => {
    await cacheStudyQueue(1, 1, null, [queueCard], settings);
    const newest: SyncCard = {
      ...queueCard,
      state: 2,
      sync_seq: 20,
      user_id: 1,
      suspended: false,
      is_leech: false,
      deleted: false,
    };
    await applySyncPage(1, 1, settings, [], {
      applied: [],
      rejected: [],
      changed_cards: [newest],
      changed_notes: [],
      cursor: 'sequenced-card-cursor',
      has_more: false,
    });

    const staleServerCard: SyncCard = {
      ...queueCard,
      state: 0,
      user_id: 1,
      suspended: false,
      is_leech: false,
      deleted: false,
    };
    await cacheServerCard(1, 1, staleServerCard, settings);
    const cachedQueue = await cacheStudyQueue(1, 1, null, [queueCard], settings);

    expect(await offlineDatabase.cards.get(queueCard.id)).toMatchObject({ state: 2, sync_seq: 20 });
    expect((await offlineDatabase.queue.get([queueScope(1, null), queueCard.id]))?.card.state).toBe(2);
    expect(cachedQueue).toMatchObject([{ id: queueCard.id, state: 2 }]);
  });

  it('does not let a delayed queue response overwrite a newer unversioned review result', async () => {
    await cacheStudyQueue(1, 1, null, [queueCard], settings);
    await cacheServerCard(1, 1, {
      ...queueCard,
      state: 2,
      user_id: 1,
      suspended: false,
      is_leech: false,
      deleted: false,
    }, settings);

    await cacheStudyQueue(1, 1, null, [queueCard], settings);

    expect(await offlineDatabase.cards.get(queueCard.id)).toMatchObject({ state: 2 });
    expect((await offlineDatabase.queue.get([queueScope(1, null), queueCard.id]))?.card.state).toBe(2);
  });

  it('rolls back card changes, outbox acknowledgement, and cursor when applying a page fails', async () => {
    await cacheStudyQueue(1, 1, null, [queueCard], settings);
    const localCard = applyLocalReview(queueCard, 3, new Date(review.reviewed_at), settings);
    await saveOfflineReview(1, 1, review, settings);
    await offlineDatabase.metadata.put({ key: 'cursor:1', value: 'previous-cursor' });
    const badCard = { id: undefined, deleted: true } as unknown as SyncCard;

    await expect(applySyncPage(1, 1, settings, [review], {
      applied: [review.id],
      rejected: [],
      changed_cards: [badCard],
      changed_notes: [{
        id: 99,
        deck_id: 2,
        term: 'new note',
        translation: 'translation',
        transcription: null,
        example: null,
        example_translation: null,
        notes: null,
        tags: [],
        alternatives: [],
        fields: {},
        updated_at: '2026-04-03T12:00:00.000Z',
        deleted: false,
      }],
      cursor: 'must-not-commit',
      has_more: false,
    })).rejects.toThrow();

    expect(await offlineDatabase.outbox.count()).toBe(1);
    expect(await getSyncCursor(1, 1)).toBe('previous-cursor');
    expect((await offlineDatabase.queue.get([queueScope(1, null), queueCard.id]))?.card.state).toBe(localCard.state);
    expect(await offlineDatabase.notes.get(99)).toBeUndefined();
  });

  it('keeps rejected reviews scoped to their owner until explicit discard', async () => {
    await cacheStudyQueue(1, 1, null, [queueCard], settings);
    await saveOfflineReview(1, 1, review, settings);
    await applySyncPage(1, 1, settings, [review], {
      applied: [],
      rejected: [{ id: review.id, code: 'not_found' }],
      changed_cards: [],
      changed_notes: [],
      cursor: 'rejected-cursor',
      has_more: false,
    });
    await offlineDatabase.outbox.add({
      ...review,
      id: '018f0f3e-7b00-7000-8000-000000000002',
      sync_status: undefined,
      sync_error: undefined,
    });

    expect(await pendingOfflineReviews(1, 1, 10)).toHaveLength(1);
    expect(await rejectedOfflineReviews(1, 1)).toMatchObject([{ id: review.id, sync_error: 'not_found' }]);
    await expect(prepareOfflineOwner(profile(1, 99))).resolves.toBeUndefined();
    await expect(pendingOfflineReviews(1, 99, 10)).resolves.toEqual([]);
    expect(await offlineDatabase.outbox.get(review.id)).toMatchObject({ owner_id: 1, sync_status: 'rejected' });

    await prepareOfflineOwner(profile(1, 1));
    expect(await pendingOfflineReviews(1, 1, 10)).toHaveLength(1);
    await discardRejectedOfflineReview(1, 1, review.id);
    expect(await rejectedOfflineReviews(1, 1)).toEqual([]);
  });

  it('applies note and card tombstones and isolates storage when the account changes', async () => {
    await cacheStudyQueue(1, 1, null, [queueCard], settings);
    await offlineDatabase.notes.put({
      id: queueCard.note_id,
      deck_id: queueCard.deck_id!,
      term: 'hello',
      translation: 'hallo',
      transcription: null,
      example: null,
      example_translation: null,
      notes: null,
      tags: [],
      alternatives: [],
      fields: {},
      updated_at: '2026-04-03T12:00:00.000Z',
      deleted: false,
    });
    await applySyncPage(1, 1, settings, [], {
      applied: [],
      rejected: [],
      changed_cards: [{ id: queueCard.id, deleted: true } as SyncCard],
      changed_notes: [{ id: queueCard.note_id, deleted: true } as SyncResponse['changed_notes'][number]],
      cursor: 'tombstone-cursor',
      has_more: false,
    });
    expect(await offlineDatabase.cards.get(queueCard.id)).toMatchObject({ deleted: true });
    expect(await offlineDatabase.notes.get(queueCard.note_id)).toMatchObject({ deleted: true });
    expect(await offlineDatabase.queue.get([queueScope(1, null), queueCard.id])).toBeUndefined();

    await prepareOfflineOwner(profile(2));
    expect(await offlineDatabase.queue.count()).toBe(0);
    expect(await offlineDatabase.cards.count()).toBe(0);
    expect(await offlineDatabase.notes.count()).toBe(0);
    expect(await offlineDatabase.outbox.count()).toBe(0);
    expect(await offlineDatabase.profiles.get('current')).toMatchObject({ profile: { user: { id: 2 } } });
  });

  it('does not let stale queue, review, or undo responses resurrect tombstoned content', async () => {
    await cacheStudyQueue(1, 1, null, [queueCard], settings);
    await applySyncPage(1, 1, settings, [], {
      applied: [],
      rejected: [],
      changed_cards: [{ id: queueCard.id, sync_seq: 10, deleted: true } as SyncCard],
      changed_notes: [{ id: queueCard.note_id, sync_seq: 11, deleted: true } as SyncResponse['changed_notes'][number]],
      cursor: 'delete-race-cursor',
      has_more: false,
    });

    const staleReviewOrUndo: SyncCard = {
      ...queueCard,
      user_id: 1,
      suspended: false,
      is_leech: false,
      deleted: false,
    };
    await expect(cacheServerCard(1, 1, staleReviewOrUndo, settings)).resolves.toBeUndefined();
    await expect(cacheStudyQueue(1, 1, null, [queueCard], settings)).resolves.toEqual([]);

    expect(await offlineDatabase.cards.get(queueCard.id)).toMatchObject({ deleted: true, sync_seq: 11 });
    expect(await offlineDatabase.notes.get(queueCard.note_id)).toMatchObject({ deleted: true, sync_seq: 11 });
    expect(await offlineDatabase.queue.get([queueScope(1, null), queueCard.id])).toBeUndefined();
  });
});
