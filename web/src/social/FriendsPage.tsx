import { useState, type FormEvent } from 'react';
import { ApiError, type DeckShare } from '../api/client';
import { type DeckId, type DeckNode } from '../api/content';
import { t } from '../i18n/en';
import { Button } from '../ui/Button';
import { focus } from '../ui/shared';
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
import './FriendsPage.css';

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

function Person({ name, detail }: { name: string; detail: string }) {
  return (
    <div className="flex min-w-0 items-center gap-3">
      <span className="friends-avatar" aria-hidden="true">
        <svg viewBox="0 0 40 40" width="40" height="40"><path d="M20 4C26-1 34 4 34 11C41 14 41 24 35 28C35 35 26 41 20 36C14 41 5 35 5 28C-1 24-1 14 6 11C6 4 14-1 20 4Z" /></svg>
        <span>{Array.from((name.trim() || detail.trim()).toLocaleUpperCase())[0]}</span>
      </span>
      <div className="min-w-0">
        <p className="m-0 break-words font-medium text-on-surface">{name}</p>
        <p className="m-0 break-words text-sm text-on-surface-variant">{detail}</p>
      </div>
    </div>
  );
}

const fieldClass = `min-h-12 w-full min-w-0 rounded-xl border border-outline bg-surface-container-lowest px-3 text-base font-normal text-on-surface disabled:opacity-60 ${focus}`;

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
    <section className="friends-page" aria-labelledby="friends-heading">
      <header className="page-intro">
        <h1 id="friends-heading">{t('friendsHeading')}</h1>
        <p>{t('friendsDescription')}</p>
      </header>

      <div className="friends-pair">
        <section className="friends-group friends-invite" aria-labelledby="friend-invite-heading">
          <div>
            <h2 id="friend-invite-heading">{t('addFriend')}</h2>
            <p id="friend-invite-privacy" className="m-0 mt-1 text-sm">{t('friendRequestPrivacy')}</p>
          </div>
          <form onSubmit={submitFriendRequest} className="flex flex-wrap items-end gap-2">
            <label className="grid min-w-0 flex-1 basis-52 gap-1 text-sm font-medium" htmlFor="friend-email">
              {t('friendEmail')}
              <input id="friend-email" type="email" required autoComplete="email" aria-describedby="friend-invite-privacy" value={email} onChange={(event) => setEmail(event.target.value)} className={fieldClass} />
            </label>
            <Button type="submit" disabled={requestMutation.isPending}>{t(requestMutation.isPending ? 'sendingFriendRequest' : 'sendFriendRequest')}</Button>
          </form>
          {requestNotice && <p role="status" className="m-0 text-sm">{t('friendRequestSent')}</p>}
          {requestMutation.isError && <p role="alert" className="m-0 rounded-lg bg-error-container p-2 text-sm text-on-error-container">{errorMessage(requestMutation.error)}</p>}
        </section>

        <section className="friends-group" aria-labelledby="privacy-heading">
          <div className="grid min-h-12 grid-cols-[minmax(0,1fr)_auto] items-start gap-3">
            <div>
              <h2 id="privacy-heading"><label htmlFor="social-visibility" className="cursor-pointer">{t('socialVisibility')}</label></h2>
              <p id="social-visibility-hint" className="m-0 mt-1 text-sm text-on-surface-variant">{t('socialVisibilityHint')}</p>
            </div>
            {settingsQuery.isSuccess && <label className="flex size-12 cursor-pointer items-center justify-center rounded-xl bg-surface-container focus-within:outline focus-within:outline-2 focus-within:outline-offset-2 focus-within:outline-primary">
              <input id="social-visibility" aria-labelledby="privacy-heading" aria-describedby="social-visibility-hint" type="checkbox" checked={settingsQuery.data.visible} disabled={settingsMutation.isPending} onChange={(event) => settingsMutation.mutate(event.target.checked)} className="size-5 accent-primary" />
            </label>}
          </div>
          {settingsQuery.isPending && <p role="status" className="m-0 text-sm text-on-surface-variant">{t('loading')}</p>}
          {settingsQuery.isError && <p role="alert" className="m-0 text-sm text-error">{errorMessage(settingsQuery.error)}</p>}
          {settingsMutation.isSuccess && <p role="status" className="m-0 text-sm text-on-surface-variant">{t('socialSettingsSaved')}</p>}
          {settingsMutation.isError && <p role="alert" className="m-0 text-sm text-error">{errorMessage(settingsMutation.error)}</p>}
        </section>
      </div>

      <div className="friends-pair">
        <section className="friends-group" aria-labelledby="requests-heading">
          <h2 id="requests-heading">{t('friendRequests')}</h2>
          {requestsQuery.isPending && <p role="status" className="m-0 text-on-surface-variant">{t('loading')}</p>}
          {requestsQuery.isError && <p role="alert" className="m-0 text-error">{errorMessage(requestsQuery.error)}</p>}
          {requestsQuery.isSuccess && requests.length === 0 && <p className="m-0 text-on-surface-variant">{t('noFriendRequests')}</p>}
          {incoming.length > 0 && <div className="space-y-2">
            <h3 className="friends-label">{t('incomingRequests')}</h3>
            <ul className="friends-list">
              {incoming.map((request) => <li key={request.id} className="friends-row">
                <Person name={request.name} detail={request.email} />
                <div className="flex min-w-0 flex-wrap gap-1">
                  <Button type="button" onClick={() => acceptMutation.mutate(request.id)} disabled={acceptMutation.isPending}>{t('acceptFriendRequest')}</Button>
                  <Button type="button" variant="tonal" onClick={() => declineMutation.mutate(request.id)} disabled={declineMutation.isPending}>{t('declineFriendRequest')}</Button>
                </div>
              </li>)}
            </ul>
          </div>}
          {outgoing.length > 0 && <div className="space-y-2">
            <h3 className="friends-label">{t('outgoingRequests')}</h3>
            <ul className="friends-list">
              {outgoing.map((request) => <li key={request.id} className="friends-row">
                <Person name={request.name} detail={request.email} />
                <Button type="button" variant="tonal" onClick={() => cancelMutation.mutate(request.id)} disabled={cancelMutation.isPending}>{t('cancelFriendRequest')}</Button>
              </li>)}
            </ul>
          </div>}
          {(acceptMutation.isError || declineMutation.isError || cancelMutation.isError) && <p role="alert" className="m-0 text-error">{errorMessage(acceptMutation.error ?? declineMutation.error ?? cancelMutation.error)}</p>}
        </section>

        <section className="friends-group" aria-labelledby="friends-list-heading">
          <h2 id="friends-list-heading">{t('yourFriends')}</h2>
          {friendsQuery.isPending && <p role="status" className="m-0 text-on-surface-variant">{t('loading')}</p>}
          {friendsQuery.isError && <p role="alert" className="m-0 text-error">{errorMessage(friendsQuery.error)}</p>}
          {friendsQuery.isSuccess && friends.length === 0 && <p className="m-0 text-on-surface-variant">{t('noFriends')}</p>}
          <ul className="friends-list">
            {friends.map((friend) => <li key={friend.id} className="friends-row">
              <Person name={friend.name} detail={friend.email} />
              <Button type="button" variant="tonal" onClick={() => removeFriend(friend.id, friend.name)} disabled={removeMutation.isPending}>{t('removeFriend')}</Button>
            </li>)}
          </ul>
          {removeMutation.isError && <p role="alert" className="m-0 text-error">{errorMessage(removeMutation.error)}</p>}
        </section>
      </div>

      <div className="friends-pair">
        <section className="friends-group" aria-labelledby="leaderboard-heading">
          <div>
            <h2 id="leaderboard-heading">{t('weeklyLeaderboard')}</h2>
            <p className="m-0 mt-1 text-sm text-on-surface-variant">{t('weeklyLeaderboardDescription')}</p>
          </div>
          {leaderboardQuery.isPending && <p role="status" className="m-0 text-on-surface-variant">{t('loading')}</p>}
          {leaderboardQuery.isError && <p role="alert" className="m-0 text-error">{errorMessage(leaderboardQuery.error)}</p>}
          {leaderboardQuery.isSuccess && leaderboardQuery.data.length === 0 && <p className="m-0 text-on-surface-variant">{t('noLeaderboard')}</p>}
          <ol className="friends-list" aria-label={t('weeklyLeaderboard')}>
            {(leaderboardQuery.data ?? []).map((entry) => <li key={entry.user_id} className="friends-leader">
              <div className="flex flex-wrap items-baseline justify-between gap-2">
                <h3 className="m-0 min-w-0 break-words font-medium text-on-surface">{entry.name}{entry.user_id === userId ? ` (${t('you')})` : ''}</h3>
                <span className="rounded-full bg-secondary-container px-2 py-1 text-xs font-medium tabular-nums text-on-secondary-container">{replace(t('leaderboardRank'), { rank: entry.rank })}</span>
              </div>
              <p className="m-0 mt-2 flex flex-wrap gap-x-4 gap-y-1 text-sm text-on-surface-variant">
                <span>{replace(t('weeklyReviews'), { count: entry.review_count })}</span>
                <span>{replace(t('weeklyLearned'), { count: entry.learned_count })}</span>
                <span>{replace(t('currentStreak'), { count: entry.streak_days })}</span>
              </p>
            </li>)}
          </ol>
        </section>

        <section className="friends-group" aria-labelledby="activity-heading">
          <div>
            <h2 id="activity-heading">{t('socialActivity')}</h2>
            <p className="m-0 mt-1 text-sm text-on-surface-variant">{t('socialActivityDescription')}</p>
          </div>
          {activityQuery.isPending && <p role="status" className="m-0 text-on-surface-variant">{t('loading')}</p>}
          {activityQuery.isError && <p role="alert" className="m-0 text-error">{errorMessage(activityQuery.error)}</p>}
          {activityQuery.isSuccess && activityQuery.data.length === 0 && <p className="m-0 text-on-surface-variant">{t('noSocialActivity')}</p>}
          <ul className="friends-list">
            {(activityQuery.data ?? []).map((item) => <li key={`${item.user_id}-${item.day}`} className="friends-row">
              <div className="min-w-0"><p className="m-0 break-words font-medium text-on-surface">{item.name}</p><p className="m-0 text-sm text-on-surface-variant">{formatDay(item.day)}</p></div>
              <span className="text-sm text-on-surface-variant">{replace(t('socialDaySummary'), { count: item.review_count, streak: item.streak_days })}</span>
            </li>)}
          </ul>
        </section>
      </div>

      <section className="friends-group" aria-labelledby="shared-decks-heading">
        <div>
          <h2 id="shared-decks-heading">{t('sharedDecks')}</h2>
          <p className="m-0 mt-1 max-w-3xl text-sm text-on-surface-variant">{t('sharedDecksDescription')}</p>
        </div>
        <form onSubmit={submitShare} className="grid gap-3" aria-labelledby="shared-decks-heading">
          <div className="friends-fields">
            <label className="grid min-w-0 gap-1 text-sm font-medium text-on-surface" htmlFor="share-deck">
              {t('chooseDeckToShare')}
              <select id="share-deck" value={deckId} onChange={(event) => setDeckId(event.target.value)} required disabled={decks.length === 0} className={fieldClass}>
                <option value="">{t('chooseDeckToShare')}</option>
                {decks.map((deck) => <option key={String(deck.id)} value={deck.id}>{deck.name}</option>)}
              </select>
            </label>
            <label className="grid min-w-0 gap-1 text-sm font-medium text-on-surface" htmlFor="share-friend">
              {t('chooseFriend')}
              <select id="share-friend" value={recipientId} onChange={(event) => setRecipientId(event.target.value ? Number(event.target.value) : '')} required disabled={friends.length === 0} className={fieldClass}>
                <option value="">{t('chooseFriend')}</option>
                {friends.map((friend) => <option key={friend.id} value={friend.id}>{friend.name}</option>)}
              </select>
            </label>
          </div>
          <fieldset className="m-0 min-w-0 border-0 p-0" aria-describedby="sharing-privacy-notice">
            <legend className="mb-1 text-sm font-medium text-on-surface">{t('shareMode')}</legend>
            <div className="friends-modes">
              <label className="friends-mode">
                <input type="radio" name="share-mode" value="subscribe" checked={shareMode === 'subscribe'} onChange={() => setShareMode('subscribe')} className="mt-1 size-5 shrink-0 accent-primary" />
                <span><span className="block font-medium">{t('subscribeMode')}</span><span className="block text-sm">{t('subscribeModeHint')}</span></span>
              </label>
              <label className="friends-mode">
                <input type="radio" name="share-mode" value="copy" checked={shareMode === 'copy'} onChange={() => setShareMode('copy')} className="mt-1 size-5 shrink-0 accent-primary" />
                <span><span className="block font-medium">{t('copyMode')}</span><span className="block text-sm">{t('copyModeHint')}</span></span>
              </label>
            </div>
          </fieldset>
          <p id="sharing-privacy-notice" className="m-0 max-w-3xl text-sm text-on-surface-variant">{t('sharingPrivacyNotice')}</p>
          {(friendsQuery.isSuccess && friends.length === 0) && <p className="m-0 text-sm text-on-surface-variant">{t('noShareTargets')}</p>}
          {decksQuery.isError && <p role="alert" className="m-0 text-sm text-error">{errorMessage(decksQuery.error)}</p>}
          <div className="flex flex-wrap items-center gap-3">
            <Button type="submit" disabled={isBusy || decks.length === 0 || friends.length === 0}>{t(shareMutation.isPending ? 'sharingDeck' : 'shareDeck')}</Button>
            {shareNotice && <p role="status" className="m-0 text-sm text-on-surface-variant">{t('sharingSaved')}</p>}
            {shareMutation.isError && <p role="alert" className="m-0 text-sm text-error">{errorMessage(shareMutation.error)}</p>}
          </div>
        </form>
        <div className="space-y-2 border-t border-outline-variant pt-3">
          <h3 className="friends-label">{t('sharedDecks')}</h3>
          {sharesQuery.isPending && <p role="status" className="m-0 text-on-surface-variant">{t('loading')}</p>}
          {sharesQuery.isError && <p role="alert" className="m-0 text-error">{errorMessage(sharesQuery.error)}</p>}
          {sharesQuery.isSuccess && sharesQuery.data.length === 0 && <p className="m-0 text-on-surface-variant">{t('noSharedDecks')}</p>}
          <ul className="friends-list">
            {(sharesQuery.data ?? []).map((share) => {
              const counterpart = share.role === 'recipient' ? share.source_owner_name : share.recipient_name;
              const active = share.mode === 'subscribe' && share.status === 'active';
              const status = share.mode === 'copy'
                ? t('shareCopyStatus')
                : share.status === 'active' ? t('shareSubscriptionActive')
                  : share.status === 'revoked' ? t('shareSubscriptionRevoked') : t('shareSubscriptionEnded');
              return <li key={share.id} className="friends-row">
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
