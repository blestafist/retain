import { useState, type FormEvent } from 'react';
import { ApiError, type DeckShare } from '../api/client';
import { type DeckId, type DeckNode } from '../api/content';
import { t } from '../i18n/en';
import { Button } from '../ui/Button';
import { useDecks } from '../content/hooks';
import {
  useAcceptFriendRequest,
  useCancelFriendRequest,
  useCreateDeckShare,
  useCreateFriendRequest,
  useDeclineFriendRequest,
  useEndDeckShare,
  useFriendRequests,
  useFriends,
  useMyDeckShares,
  usePatchSocialSettings,
  useRemoveFriend,
  useSocialActivity,
  useSocialSettings,
  useWeeklyLeaderboard,
} from './hooks';

function errorMessage(error: unknown): string {
  return error instanceof ApiError && error.serverMessage ? error.serverMessage : t('requestFailed');
}

function flattenDecks(decks: DeckNode[]): DeckNode[] {
  return decks.flatMap((deck) => [deck, ...flattenDecks(deck.children)]);
}

function replace(template: string, values: Record<string, string | number>): string {
  return Object.entries(values).reduce((text, [key, value]) => text.replaceAll(`{${key}}`, String(value)), template);
}

function formatDay(day: string): string {
  const date = new Date(`${day}T00:00:00Z`);
  return Number.isNaN(date.getTime()) ? day : date.toLocaleDateString(undefined, { weekday: 'short', month: 'short', day: 'numeric', timeZone: 'UTC' });
}

function shareName(share: DeckShare): string {
  return share.source_deck_name || t('sharedDecks');
}

export function FriendsPage({ userId }: { userId: number }) {
  const decksQuery = useDecks();
  const friendsQuery = useFriends();
  const requestsQuery = useFriendRequests();
  const settingsQuery = useSocialSettings();
  const activityQuery = useSocialActivity();
  const leaderboardQuery = useWeeklyLeaderboard();
  const sharesQuery = useMyDeckShares();
  const requestMutation = useCreateFriendRequest();
  const acceptMutation = useAcceptFriendRequest();
  const declineMutation = useDeclineFriendRequest();
  const cancelMutation = useCancelFriendRequest();
  const removeMutation = useRemoveFriend();
  const settingsMutation = usePatchSocialSettings();
  const shareMutation = useCreateDeckShare();
  const endShareMutation = useEndDeckShare();

  const [email, setEmail] = useState('');
  const [deckId, setDeckId] = useState<DeckId | ''>('');
  const [recipientId, setRecipientId] = useState<number | ''>('');
  const [shareMode, setShareMode] = useState<'copy' | 'subscribe'>('subscribe');
  const [requestNotice, setRequestNotice] = useState(false);
  const [shareNotice, setShareNotice] = useState(false);

  const decks = flattenDecks(decksQuery.data ?? []).filter((deck) => deck.canEdit !== false);
  const friends = friendsQuery.data ?? [];
  const requests = requestsQuery.data ?? [];
  const incoming = requests.filter((request) => request.direction === 'incoming');
  const outgoing = requests.filter((request) => request.direction === 'outgoing');

  function submitFriendRequest(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const value = email.trim();
    if (!value) return;
    setRequestNotice(false);
    requestMutation.mutate(value, { onSuccess: () => { setEmail(''); setRequestNotice(true); } });
  }

  function submitShare(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (deckId === '' || recipientId === '') return;
    setShareNotice(false);
    shareMutation.mutate({ deckId, recipientId, mode: shareMode }, { onSuccess: () => setShareNotice(true) });
  }

  function removeFriend(id: number, name: string) {
    if (!window.confirm(replace(t('confirmRemoveFriend'), { name }))) return;
    removeMutation.mutate(id);
  }

  function endSubscription(share: DeckShare) {
    const name = shareName(share);
    if (!window.confirm(replace(t('confirmEndSubscription'), { name }))) return;
    endShareMutation.mutate({ deckId: share.source_deck_id, shareId: share.id });
  }

  const isBusy = requestMutation.isPending || shareMutation.isPending;

  return (
    <section className="mx-auto w-full max-w-6xl space-y-6 py-8" aria-labelledby="friends-heading">
      <header className="space-y-2">
        <p className="m-0 text-sm font-medium uppercase tracking-wide text-primary">{t('friends')}</p>
        <h1 id="friends-heading" className="m-0 text-3xl font-semibold text-on-surface">{t('friendsHeading')}</h1>
        <p className="m-0 max-w-3xl text-on-surface-variant">{t('friendsDescription')}</p>
      </header>

      <div className="grid gap-5 expanded:grid-cols-2">
        <section className="space-y-4 rounded-3xl bg-surface-container-low p-5" aria-labelledby="friend-invite-heading">
          <div>
            <h2 id="friend-invite-heading" className="m-0 text-xl font-semibold text-on-surface">{t('addFriend')}</h2>
            <p className="m-0 mt-1 text-sm text-on-surface-variant">{t('friendRequestPrivacy')}</p>
          </div>
          <form onSubmit={submitFriendRequest} className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_auto]">
            <label className="grid gap-2 text-sm font-medium text-on-surface" htmlFor="friend-email">
              {t('friendEmail')}
              <input id="friend-email" type="email" required autoComplete="email" value={email} onChange={(event) => setEmail(event.target.value)} className="min-h-12 rounded-xl border border-outline bg-surface-container-lowest px-4 text-base font-normal text-on-surface focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary" />
            </label>
            <Button type="submit" className="self-end" disabled={requestMutation.isPending}>{t(requestMutation.isPending ? 'sendingFriendRequest' : 'sendFriendRequest')}</Button>
          </form>
          {requestNotice && <p role="status" className="m-0 text-sm text-on-surface-variant">{t('friendRequestSent')}</p>}
          {requestMutation.isError && <p role="alert" className="m-0 text-sm text-error">{errorMessage(requestMutation.error)}</p>}
        </section>

        <section className="space-y-4 rounded-3xl bg-surface-container-low p-5" aria-labelledby="privacy-heading">
          <div>
            <h2 id="privacy-heading" className="m-0 text-xl font-semibold text-on-surface">{t('socialVisibility')}</h2>
            <p className="m-0 mt-1 text-sm text-on-surface-variant">{t('socialVisibilityHint')}</p>
          </div>
          {settingsQuery.isPending ? <p role="status" className="m-0 text-on-surface-variant">{t('loading')}</p>
            : settingsQuery.isError ? <p role="alert" className="m-0 text-error">{errorMessage(settingsQuery.error)}</p>
              : <label className="flex min-h-12 items-center gap-3 font-medium text-on-surface">
                <input type="checkbox" checked={settingsQuery.data.visible} disabled={settingsMutation.isPending} onChange={(event) => settingsMutation.mutate(event.target.checked)} className="size-5 accent-primary" />
                <span>{t('socialVisibility')}</span>
              </label>}
          {settingsMutation.isSuccess && <p role="status" className="m-0 text-sm text-on-surface-variant">{t('socialSettingsSaved')}</p>}
          {settingsMutation.isError && <p role="alert" className="m-0 text-sm text-error">{errorMessage(settingsMutation.error)}</p>}
        </section>
      </div>

      <div className="grid gap-5 expanded:grid-cols-2">
        <section className="space-y-4 rounded-3xl bg-surface-container-low p-5" aria-labelledby="requests-heading">
          <h2 id="requests-heading" className="m-0 text-xl font-semibold text-on-surface">{t('friendRequests')}</h2>
          {requestsQuery.isPending && <p role="status" className="m-0 text-on-surface-variant">{t('loading')}</p>}
          {requestsQuery.isError && <p role="alert" className="m-0 text-error">{errorMessage(requestsQuery.error)}</p>}
          {requestsQuery.isSuccess && requests.length === 0 && <p className="m-0 text-on-surface-variant">{t('noFriendRequests')}</p>}
          {incoming.length > 0 && <div className="space-y-2">
            <h3 className="m-0 text-sm font-semibold uppercase tracking-wide text-on-surface-variant">{t('incomingRequests')}</h3>
            <ul className="m-0 list-none space-y-2 p-0">
              {incoming.map((request) => <li key={request.id} className="flex flex-wrap items-center justify-between gap-3 rounded-2xl bg-surface-container p-3">
                <div className="min-w-0"><p className="m-0 break-words font-medium text-on-surface">{request.name}</p><p className="m-0 break-words text-sm text-on-surface-variant">{request.email}</p></div>
                <div className="flex flex-wrap gap-2">
                  <Button type="button" onClick={() => acceptMutation.mutate(request.id)} disabled={acceptMutation.isPending}>{t('acceptFriendRequest')}</Button>
                  <Button type="button" variant="tonal" onClick={() => declineMutation.mutate(request.id)} disabled={declineMutation.isPending}>{t('declineFriendRequest')}</Button>
                </div>
              </li>)}
            </ul>
          </div>}
          {outgoing.length > 0 && <div className="space-y-2">
            <h3 className="m-0 text-sm font-semibold uppercase tracking-wide text-on-surface-variant">{t('outgoingRequests')}</h3>
            <ul className="m-0 list-none space-y-2 p-0">
              {outgoing.map((request) => <li key={request.id} className="flex flex-wrap items-center justify-between gap-3 rounded-2xl bg-surface-container p-3">
                <div className="min-w-0"><p className="m-0 break-words font-medium text-on-surface">{request.name}</p><p className="m-0 break-words text-sm text-on-surface-variant">{request.email}</p></div>
                <Button type="button" variant="tonal" onClick={() => cancelMutation.mutate(request.id)} disabled={cancelMutation.isPending}>{t('cancelFriendRequest')}</Button>
              </li>)}
            </ul>
          </div>}
          {(acceptMutation.isError || declineMutation.isError || cancelMutation.isError) && <p role="alert" className="m-0 text-error">{errorMessage(acceptMutation.error ?? declineMutation.error ?? cancelMutation.error)}</p>}
        </section>

        <section className="space-y-4 rounded-3xl bg-surface-container-low p-5" aria-labelledby="friends-list-heading">
          <h2 id="friends-list-heading" className="m-0 text-xl font-semibold text-on-surface">{t('yourFriends')}</h2>
          {friendsQuery.isPending && <p role="status" className="m-0 text-on-surface-variant">{t('loading')}</p>}
          {friendsQuery.isError && <p role="alert" className="m-0 text-error">{errorMessage(friendsQuery.error)}</p>}
          {friendsQuery.isSuccess && friends.length === 0 && <p className="m-0 text-on-surface-variant">{t('noFriends')}</p>}
          <ul className="m-0 list-none space-y-2 p-0">
            {friends.map((friend) => <li key={friend.id} className="flex flex-wrap items-center justify-between gap-3 rounded-2xl bg-surface-container p-3">
              <div className="min-w-0"><p className="m-0 break-words font-medium text-on-surface">{friend.name}</p><p className="m-0 break-words text-sm text-on-surface-variant">{friend.email}</p></div>
              <Button type="button" variant="tonal" onClick={() => removeFriend(friend.id, friend.name)} disabled={removeMutation.isPending}>{t('removeFriend')}</Button>
            </li>)}
          </ul>
          {removeMutation.isError && <p role="alert" className="m-0 text-error">{errorMessage(removeMutation.error)}</p>}
        </section>
      </div>

      <div className="grid gap-5 expanded:grid-cols-2">
        <section className="space-y-4 rounded-3xl bg-surface-container-low p-5" aria-labelledby="leaderboard-heading">
          <div>
            <h2 id="leaderboard-heading" className="m-0 text-xl font-semibold text-on-surface">{t('weeklyLeaderboard')}</h2>
            <p className="m-0 mt-1 text-sm text-on-surface-variant">{t('weeklyLeaderboardDescription')}</p>
          </div>
          {leaderboardQuery.isPending && <p role="status" className="m-0 text-on-surface-variant">{t('loading')}</p>}
          {leaderboardQuery.isError && <p role="alert" className="m-0 text-error">{errorMessage(leaderboardQuery.error)}</p>}
          {leaderboardQuery.isSuccess && leaderboardQuery.data.length === 0 && <p className="m-0 text-on-surface-variant">{t('noLeaderboard')}</p>}
          <ol className="m-0 list-none space-y-2 p-0" aria-label={t('weeklyLeaderboard')}>
            {(leaderboardQuery.data ?? []).map((entry) => <li key={entry.user_id} className="rounded-2xl bg-surface-container p-4">
              <div className="flex flex-wrap items-baseline justify-between gap-2">
                <h3 className="m-0 font-semibold text-on-surface">{entry.name}{entry.user_id === userId ? ` (${t('you')})` : ''}</h3>
                <span className="text-sm text-on-surface-variant">{replace(t('leaderboardRank'), { rank: entry.rank })}</span>
              </div>
              <p className="m-0 mt-2 flex flex-wrap gap-x-4 gap-y-1 text-sm text-on-surface-variant">
                <span>{replace(t('weeklyReviews'), { count: entry.review_count })}</span>
                <span>{replace(t('weeklyLearned'), { count: entry.learned_count })}</span>
                <span>{replace(t('currentStreak'), { count: entry.streak_days })}</span>
              </p>
            </li>)}
          </ol>
        </section>

        <section className="space-y-4 rounded-3xl bg-surface-container-low p-5" aria-labelledby="activity-heading">
          <div>
            <h2 id="activity-heading" className="m-0 text-xl font-semibold text-on-surface">{t('socialActivity')}</h2>
            <p className="m-0 mt-1 text-sm text-on-surface-variant">{t('socialActivityDescription')}</p>
          </div>
          {activityQuery.isPending && <p role="status" className="m-0 text-on-surface-variant">{t('loading')}</p>}
          {activityQuery.isError && <p role="alert" className="m-0 text-error">{errorMessage(activityQuery.error)}</p>}
          {activityQuery.isSuccess && activityQuery.data.length === 0 && <p className="m-0 text-on-surface-variant">{t('noSocialActivity')}</p>}
          <ul className="m-0 list-none space-y-2 p-0">
            {(activityQuery.data ?? []).map((item) => <li key={`${item.user_id}-${item.day}`} className="flex flex-wrap items-center justify-between gap-3 rounded-2xl bg-surface-container p-3">
              <div><p className="m-0 font-medium text-on-surface">{item.name}</p><p className="m-0 text-sm text-on-surface-variant">{formatDay(item.day)}</p></div>
              <span className="text-sm text-on-surface-variant">{replace(t('socialDaySummary'), { count: item.review_count, streak: item.streak_days })}</span>
            </li>)}
          </ul>
        </section>
      </div>

      <section className="space-y-5 rounded-3xl bg-surface-container-low p-5 expanded:p-6" aria-labelledby="shared-decks-heading">
        <div>
          <h2 id="shared-decks-heading" className="m-0 text-xl font-semibold text-on-surface">{t('sharedDecks')}</h2>
          <p className="m-0 mt-1 max-w-3xl text-sm text-on-surface-variant">{t('sharedDecksDescription')}</p>
        </div>
        <form onSubmit={submitShare} className="grid gap-4 sm:grid-cols-2">
          <label className="grid gap-2 text-sm font-medium text-on-surface" htmlFor="share-deck">
            {t('chooseDeckToShare')}
            <select id="share-deck" value={deckId} onChange={(event) => setDeckId(event.target.value)} required disabled={decks.length === 0} className="min-h-12 rounded-xl border border-outline bg-surface-container-lowest px-4 text-base font-normal text-on-surface focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary">
              <option value="">{t('chooseDeckToShare')}</option>
              {decks.map((deck) => <option key={String(deck.id)} value={deck.id}>{deck.name}</option>)}
            </select>
          </label>
          <label className="grid gap-2 text-sm font-medium text-on-surface" htmlFor="share-friend">
            {t('chooseFriend')}
            <select id="share-friend" value={recipientId} onChange={(event) => setRecipientId(event.target.value ? Number(event.target.value) : '')} required disabled={friends.length === 0} className="min-h-12 rounded-xl border border-outline bg-surface-container-lowest px-4 text-base font-normal text-on-surface focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary">
              <option value="">{t('chooseFriend')}</option>
              {friends.map((friend) => <option key={friend.id} value={friend.id}>{friend.name}</option>)}
            </select>
          </label>
          <fieldset className="grid gap-3 sm:col-span-2">
            <legend className="text-sm font-medium text-on-surface">{t('shareMode')}</legend>
            <label className="flex min-h-12 items-start gap-3 rounded-2xl bg-surface-container p-3 text-on-surface">
              <input type="radio" name="share-mode" value="subscribe" checked={shareMode === 'subscribe'} onChange={() => setShareMode('subscribe')} className="mt-1 size-5 accent-primary" />
              <span><span className="block font-medium">{t('subscribeMode')}</span><span className="block text-sm text-on-surface-variant">{t('subscribeModeHint')}</span></span>
            </label>
            <label className="flex min-h-12 items-start gap-3 rounded-2xl bg-surface-container p-3 text-on-surface">
              <input type="radio" name="share-mode" value="copy" checked={shareMode === 'copy'} onChange={() => setShareMode('copy')} className="mt-1 size-5 accent-primary" />
              <span><span className="block font-medium">{t('copyMode')}</span><span className="block text-sm text-on-surface-variant">{t('copyModeHint')}</span></span>
            </label>
          </fieldset>
          {(friendsQuery.isSuccess && friends.length === 0) && <p className="m-0 text-sm text-on-surface-variant sm:col-span-2">{t('noShareTargets')}</p>}
          {decksQuery.isError && <p role="alert" className="m-0 text-sm text-error sm:col-span-2">{errorMessage(decksQuery.error)}</p>}
          <div className="flex flex-wrap items-center gap-3 sm:col-span-2">
            <Button type="submit" disabled={isBusy || decks.length === 0 || friends.length === 0}>{t(shareMutation.isPending ? 'sharingDeck' : 'shareDeck')}</Button>
            {shareNotice && <p role="status" className="m-0 text-sm text-on-surface-variant">{t('sharingSaved')}</p>}
            {shareMutation.isError && <p role="alert" className="m-0 text-sm text-error">{errorMessage(shareMutation.error)}</p>}
          </div>
        </form>
        <p className="m-0 rounded-2xl bg-surface-container p-4 text-sm text-on-surface-variant">{t('sharingPrivacyNotice')}</p>
        <div className="space-y-3 border-t border-outline-variant pt-5">
          <h3 className="m-0 text-lg font-semibold text-on-surface">{t('sharedDecks')}</h3>
          {sharesQuery.isPending && <p role="status" className="m-0 text-on-surface-variant">{t('loading')}</p>}
          {sharesQuery.isError && <p role="alert" className="m-0 text-error">{errorMessage(sharesQuery.error)}</p>}
          {sharesQuery.isSuccess && sharesQuery.data.length === 0 && <p className="m-0 text-on-surface-variant">{t('noSharedDecks')}</p>}
          <ul className="m-0 list-none space-y-2 p-0">
            {(sharesQuery.data ?? []).map((share) => {
              const counterpart = share.role === 'recipient' ? share.source_owner_name : share.recipient_name;
              const active = share.mode === 'subscribe' && share.status === 'active';
              const status = share.mode === 'copy'
                ? t('shareCopyStatus')
                : share.status === 'active' ? t('shareSubscriptionActive')
                  : share.status === 'revoked' ? t('shareSubscriptionRevoked') : t('shareSubscriptionEnded');
              return <li key={share.id} className="flex flex-wrap items-center justify-between gap-3 rounded-2xl bg-surface-container p-4">
                <div className="min-w-0">
                  <p className="m-0 break-words font-medium text-on-surface">{replace(share.role === 'recipient' ? t('shareAsRecipient') : t('shareAsOwner'), { name: counterpart || t('friends') })}</p>
                  <p className="m-0 break-words text-sm text-on-surface-variant">{shareName(share)} · {status}</p>
                </div>
                {active && <Button type="button" variant="tonal" onClick={() => endSubscription(share)} disabled={endShareMutation.isPending}>{t(share.role === 'recipient' ? 'unsubscribe' : 'revokeSubscription')}</Button>}
              </li>;
            })}
          </ul>
          {endShareMutation.isError && <p role="alert" className="m-0 text-error">{errorMessage(endShareMutation.error)}</p>}
          {endShareMutation.isSuccess && <p role="status" className="m-0 text-sm text-on-surface-variant">{t('sharingEnded')}</p>}
        </div>
      </section>
    </section>
  );
}
