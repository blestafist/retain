import { useCallback, useEffect, useMemo, useRef, useState, type PointerEvent } from 'react';
import { useQuery } from '@tanstack/react-query';
import { ApiError } from '../api/client';
import { getDecks, type DeckId, type DeckNode, type NativeLanguage, type TargetLanguage } from '../api/content';
import { createUUIDv7, getNextStudyQueue, getStudyQueue, submitStudyReview, undoStudyReview, type QueueCard, type ReviewInput, type StudyRating } from '../api/study';
import { t } from '../i18n/en';
import { Button } from '../ui/Button';

const ratingLabels: Record<StudyRating, 'again' | 'hard' | 'good' | 'easy'> = {
  1: 'again',
  2: 'hard',
  3: 'good',
  4: 'easy',
};

const ratingClasses: Record<StudyRating, string> = {
  1: 'bg-rating-again-container text-on-rating-again-container',
  2: 'bg-rating-hard-container text-on-rating-hard-container',
  3: 'bg-rating-good-container text-on-rating-good-container',
  4: 'bg-rating-easy-container text-on-rating-easy-container',
};

const queuePageSize = 50;

interface ReviewedCard {
  id: string;
  card: QueueCard;
}

function messageFor(error: unknown): string {
  return error instanceof ApiError && error.serverMessage ? error.serverMessage : t('requestFailed');
}

function findLanguages(decks: DeckNode[], deckId?: DeckId): { target?: TargetLanguage; native?: NativeLanguage } {
  if (deckId === undefined) return {};
  const all: DeckNode[] = [];
  const visit = (nodes: DeckNode[]) => nodes.forEach((deck) => { all.push(deck); visit(deck.children); });
  visit(decks);
  const result: { target?: TargetLanguage; native?: NativeLanguage } = {};
  const seen = new Set<string>();
  let current = all.find((deck) => String(deck.id) === String(deckId));
  while (current && (!result.target || !result.native) && !seen.has(String(current.id))) {
    seen.add(String(current.id));
    result.target ??= current.langTarget;
    result.native ??= current.langNative;
    current = current.parentId === null ? undefined : all.find((deck) => String(deck.id) === String(current?.parentId));
  }
  return result;
}

function intervalMilliseconds(value: string): number | undefined {
  const parts = [...value.matchAll(/(\d+(?:\.\d+)?)(ns|us|µs|μs|ms|s|m|h)/g)];
  if (parts.length === 0 || parts.map((part) => part[0]).join('') !== value) return undefined;
  const factors: Record<string, number> = {
    ns: 0.000001, us: 0.001, 'µs': 0.001, 'μs': 0.001,
    ms: 1, s: 1000, m: 60_000, h: 3_600_000,
  };
  return parts.reduce((total, part) => total + Number(part[1]) * factors[part[2]!], 0);
}

function formatInterval(value: string): string {
  const duration = intervalMilliseconds(value);
  if (duration === undefined) return value;
  if (duration <= 0) return t('intervalNow');
  if (duration < 60_000) return t('intervalUnderMinute');
  const units: Array<{ size: number; key: 'intervalMinutes' | 'intervalHours' | 'intervalDays' | 'intervalMonths' | 'intervalYears' }> = [
    { size: 365 * 24 * 60 * 60 * 1000, key: 'intervalYears' },
    { size: 30 * 24 * 60 * 60 * 1000, key: 'intervalMonths' },
    { size: 24 * 60 * 60 * 1000, key: 'intervalDays' },
    { size: 60 * 60 * 1000, key: 'intervalHours' },
    { size: 60_000, key: 'intervalMinutes' },
  ];
  const unit = units.find((candidate) => duration >= candidate.size)!;
  return t(unit.key).replace('{count}', String(Math.max(1, Math.round(duration / unit.size))));
}

function findVoice(voices: SpeechSynthesisVoice[], language?: TargetLanguage | NativeLanguage) {
  if (!language) return undefined;
  const prefix = `${language}-`;
  return voices.find((voice) => voice.lang.toLowerCase().startsWith(prefix));
}

function SpeakIcon() {
  return <svg aria-hidden="true" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8"><path d="M4 10v4h4l5 4V6l-5 4H4Z" /><path d="M16 9a5 5 0 0 1 0 6m2-9a9 9 0 0 1 0 12" /></svg>;
}

export function StudySession({ deckId = null, ttsAuto = false }: { deckId?: DeckId | null; ttsAuto?: boolean }) {
  const deckKey = deckId === null ? '' : String(deckId);
  const queueQuery = useQuery({
    queryKey: ['study-queue', deckKey],
    queryFn: () => getStudyQueue({ deckId, limit: queuePageSize }),
    retry: false,
  });
  const decksQuery = useQuery({ queryKey: ['decks'], queryFn: getDecks, retry: false });
  const [sessionQueue, setSessionQueue] = useState<{ deckKey: string; cards: QueueCard[]; hasMore: boolean } | null>(null);
  const [reviewed, setReviewed] = useState<ReviewedCard[]>([]);
  const [answerShown, setAnswerShown] = useState(false);
  const [busy, setBusy] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const [pendingReview, setPendingReview] = useState<ReviewInput | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [voices, setVoices] = useState<SpeechSynthesisVoice[]>([]);
  const shownAt = useRef(Date.now());
  const pendingReviewRef = useRef<ReviewInput | null>(null);
  const swipeStart = useRef<{ pointerId: number; x: number; y: number } | null>(null);
  const activeSession = sessionQueue?.deckKey === deckKey ? sessionQueue : null;
  const queue = activeSession?.cards ?? [];
  const currentCard = queue[reviewed.length];
  const languages = useMemo(
    () => findLanguages(decksQuery.data ?? [], currentCard?.deck_id ?? deckId ?? undefined),
    [currentCard?.deck_id, deckId, decksQuery.data],
  );
  const frontLanguage = currentCard?.direction === 'reverse' ? languages.native : languages.target;
  const voice = useMemo(() => findVoice(voices, frontLanguage), [frontLanguage, voices]);

  useEffect(() => {
    setReviewed([]);
    setAnswerShown(false);
    pendingReviewRef.current = null;
    setPendingReview(null);
  }, [deckId]);

  useEffect(() => {
    if (!queueQuery.data || activeSession) return;
    setSessionQueue({
      deckKey,
      cards: queueQuery.data,
      hasMore: queueQuery.data.length === queuePageSize,
    });
  }, [activeSession, deckKey, queueQuery.data]);

  useEffect(() => {
    if (typeof window === 'undefined' || !window.speechSynthesis) return undefined;
    const synthesis = window.speechSynthesis;
    const readVoices = () => setVoices(synthesis.getVoices());
    readVoices();
    synthesis.addEventListener('voiceschanged', readVoices);
    return () => synthesis.removeEventListener('voiceschanged', readVoices);
  }, []);

  const speak = useCallback((card: QueueCard | undefined, selectedVoice: SpeechSynthesisVoice | undefined) => {
    if (!card || !selectedVoice || typeof window === 'undefined' || !window.speechSynthesis) return;
    const utterance = new SpeechSynthesisUtterance(card.front);
    utterance.voice = selectedVoice;
    utterance.lang = selectedVoice.lang;
    window.speechSynthesis.cancel();
    window.speechSynthesis.speak(utterance);
  }, []);

  useEffect(() => {
    shownAt.current = Date.now();
    setAnswerShown(false);
    setError(null);
    if (ttsAuto) speak(currentCard, voice);
  }, [currentCard?.id, currentCard?.front, speak, ttsAuto, voice]);

  const submitReview = useCallback(async (input: ReviewInput, card: QueueCard) => {
    setBusy(true);
    setError(null);
    try {
      await submitStudyReview(input);
      pendingReviewRef.current = null;
      setPendingReview(null);
      setReviewed((previous) => [...previous, { id: input.id, card }]);
      setAnswerShown(false);
    } catch (caught) {
      setError(caught);
      if (caught instanceof ApiError && caught.status >= 400 && caught.status < 500 && caught.status !== 408) {
        pendingReviewRef.current = null;
        setPendingReview(null);
      }
    } finally {
      setBusy(false);
    }
  }, []);

  const rate = useCallback(async (rating: StudyRating) => {
    if (!currentCard || busy) return;
    const existing = pendingReviewRef.current;
    const input = existing?.card_id === currentCard.id ? existing : {
        id: createUUIDv7(),
        card_id: currentCard.id,
        rating,
        mode: 'flip',
        reviewed_at: new Date().toISOString(),
        duration_ms: Math.max(0, Date.now() - shownAt.current),
      } satisfies ReviewInput;
    pendingReviewRef.current = input;
    setPendingReview(input);
    await submitReview(input, currentCard);
  }, [busy, currentCard, submitReview]);

  const retryPendingReview = useCallback(async () => {
    const input = pendingReviewRef.current;
    if (!input || !currentCard || input.card_id !== currentCard.id || busy) return;
    await submitReview(input, currentCard);
  }, [busy, currentCard, submitReview]);

  const loadMore = useCallback(async () => {
    if (!activeSession?.hasMore || loadingMore) return;
    setLoadingMore(true);
    setError(null);
    try {
      const nextPage = await getNextStudyQueue({
        deckId,
        limit: queuePageSize,
        excludeCardIds: activeSession.cards.map((card) => card.id),
      });
      setSessionQueue((previous) => {
        if (!previous || previous.deckKey !== deckKey) return previous;
        const seen = new Set([
          ...previous.cards.map((card) => card.id),
          ...reviewed.map(({ card }) => card.id),
        ]);
        const newCards = nextPage.filter((card) => !seen.has(card.id));
        return {
          deckKey,
          cards: [...previous.cards, ...newCards],
          hasMore: nextPage.length === queuePageSize && newCards.length > 0,
        };
      });
    } catch (caught) {
      setError(caught);
    } finally {
      setLoadingMore(false);
    }
  }, [activeSession, deckId, deckKey, loadingMore, reviewed]);

  const onSwipeStart = useCallback((event: PointerEvent<HTMLElement>) => {
    if (event.pointerType !== 'touch' || !event.isPrimary || !currentCard) return;
    if (event.target instanceof Element && event.target.closest('button, a, input, select, textarea, [contenteditable="true"]')) return;
    swipeStart.current = { pointerId: event.pointerId, x: event.clientX, y: event.clientY };
  }, [currentCard]);

  const onSwipeEnd = useCallback((event: PointerEvent<HTMLElement>) => {
    const start = swipeStart.current;
    swipeStart.current = null;
    if (!start || start.pointerId !== event.pointerId || !answerShown || busy) return;
    const deltaX = event.clientX - start.x;
    const deltaY = event.clientY - start.y;
    if (Math.abs(deltaX) < 80 || Math.abs(deltaX) < Math.abs(deltaY) * 1.3) return;
    void rate(deltaX < 0 ? 1 : 3);
  }, [answerShown, busy, rate]);

  const undo = useCallback(async () => {
    const last = reviewed.at(-1);
    if (!last || busy || pendingReview !== null) return;
    setBusy(true);
    setError(null);
    try {
      await undoStudyReview(last.id);
      setReviewed((previous) => previous.slice(0, -1));
      setAnswerShown(false);
    } catch (caught) {
      setError(caught);
    } finally {
      setBusy(false);
    }
  }, [busy, pendingReview, reviewed]);

  useEffect(() => {
    function onKeyDown(event: KeyboardEvent) {
      if (event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return;
      if (event.target instanceof Element && event.target.closest('a, button, input, select, textarea, [contenteditable="true"]')) return;
      if ((event.key === ' ' || event.key === 'Spacebar') && currentCard && !answerShown) {
        event.preventDefault();
        setAnswerShown(true);
      } else if (/^[1-4]$/.test(event.key) && answerShown && currentCard && !busy) {
        event.preventDefault();
        void rate(Number(event.key) as StudyRating);
      }
    }
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [answerShown, busy, currentCard, rate]);

  const sessionReady = activeSession !== null;
  const progress = queue.length === 0 ? 100 : (reviewed.length / queue.length) * 100;
  const complete = sessionReady && !currentCard;

  return (
    <section className="mx-auto grid w-full max-w-4xl gap-5 py-6" aria-labelledby="study-session-heading">
      <header className="flex flex-wrap items-center justify-between gap-3">
        <div className="space-y-1">
          <a href="/" className="inline-flex min-h-12 items-center rounded-full px-3 text-primary underline-offset-4 hover:bg-surface-container focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary">{t('backToDecks')}</a>
          <h1 id="study-session-heading" className="m-0 text-3xl font-semibold text-on-surface">{t('studySession')}</h1>
        </div>
        <div className="flex gap-2" aria-label={t('sessionActions')}>
          {voice && currentCard && <Button type="button" variant="tonal" onClick={() => speak(currentCard, voice)}><SpeakIcon />{t('speak')}</Button>}
          {reviewed.length > 0 && <Button type="button" variant="tonal" disabled={busy || pendingReview !== null} onClick={() => void undo()}>{t(busy ? 'undoing' : 'undo')}</Button>}
        </div>
      </header>

      {queueQuery.isPending && <p role="status" className="rounded-3xl bg-surface-container-low p-6 text-on-surface-variant">{t('loadingStudyQueue')}</p>}
      {queueQuery.isError && (
        <div role="alert" className="grid gap-3 rounded-3xl bg-error-container p-5 text-on-error-container">
          <p className="m-0">{messageFor(queueQuery.error)}</p>
          <Button type="button" variant="tonal" onClick={() => void queueQuery.refetch()}>{t('tryAgain')}</Button>
        </div>
      )}
      {error !== null && <p role="alert" className="rounded-xl bg-error-container p-4 text-on-error-container">{messageFor(error)}</p>}

      {currentCard && (
        <>
          <div className="grid gap-2 rounded-2xl bg-surface-container-low p-4">
            <div className="flex justify-between gap-3 text-sm text-on-surface-variant">
              <span>{t('studyProgress').replace('{current}', String(reviewed.length + 1)).replace('{total}', String(queue.length))}</span>
              <span>{t('cardsReviewed').replace('{count}', String(reviewed.length))}</span>
            </div>
            <div role="progressbar" aria-label={t('studyProgressBar')} aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(progress)} className="h-2 overflow-hidden rounded-full bg-surface-container-high">
              <div className="h-full rounded-full bg-primary transition-[width]" style={{ width: `${progress}%` }} />
            </div>
          </div>

          <article
            className="grid min-h-80 content-center gap-8 rounded-[2rem] bg-surface-container-low p-6 text-center [touch-action:pan-y] sm:p-10"
            aria-label={t('studyCard')}
            onPointerDown={onSwipeStart}
            onPointerUp={onSwipeEnd}
            onPointerCancel={() => { swipeStart.current = null; }}
          >
            <div className="grid gap-4">
              <p className="m-0 text-sm font-medium uppercase tracking-wide text-primary">{t('front')}</p>
              <h2 className="m-0 break-words text-4xl font-semibold text-on-surface sm:text-5xl">{currentCard.front}</h2>
              {answerShown && (
                <div className="mx-auto grid w-full max-w-2xl gap-2 border-t border-outline-variant pt-6">
                  <p className="m-0 text-sm font-medium uppercase tracking-wide text-on-surface-variant">{t('answer')}</p>
                  <p className="m-0 break-words text-2xl text-on-surface">{currentCard.back}</p>
                </div>
              )}
            </div>
            {!answerShown ? (
              <div className="grid justify-items-center gap-2">
                <Button type="button" onClick={() => setAnswerShown(true)}>{t('showAnswer')}</Button>
                <p className="m-0 text-sm text-on-surface-variant">{t('showAnswerShortcut')}</p>
              </div>
            ) : (
              <div className="grid gap-3">
                <div role="group" aria-label={t('ratingGroup')} className="grid grid-cols-2 gap-2 expanded:grid-cols-4">
                  {([1, 2, 3, 4] as const).map((rating) => (
                    <button
                      key={rating}
                      type="button"
                      disabled={busy || pendingReview !== null}
                      onClick={() => void rate(rating)}
                      className={`flex min-h-12 flex-col items-center justify-center rounded-2xl px-3 py-2 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary disabled:opacity-50 ${ratingClasses[rating]}`}
                    >
                      <span className="font-medium">{t(ratingLabels[rating])}</span>
                      <span className="text-sm opacity-80">{formatInterval(currentCard.previews[rating].interval)}</span>
                    </button>
                  ))}
                </div>
                {pendingReview && <Button type="button" variant="tonal" disabled={busy} onClick={() => void retryPendingReview()}>{t(busy ? 'retryingReview' : 'retryReview')}</Button>}
                <p className="m-0 text-sm text-on-surface-variant">{t('ratingShortcut')}</p>
                <p className="m-0 text-sm text-on-surface-variant">{t('swipeRatingHint')}</p>
              </div>
            )}
          </article>
        </>
      )}

      {complete && activeSession?.hasMore && (
        <div className="rounded-3xl bg-surface-container-low p-5">
          <p className="m-0 mb-3 text-on-surface-variant">{t('moreStudyCardsAvailable')}</p>
          <Button type="button" disabled={loadingMore} onClick={() => void loadMore()}>{t(loadingMore ? 'loadingMoreStudyCards' : 'loadMoreStudyCards')}</Button>
        </div>
      )}
      {complete && !activeSession?.hasMore && <div role="status" className="rounded-3xl bg-secondary-container p-6 text-on-secondary-container"><h2 className="m-0 text-2xl font-semibold">{t('studyComplete')}</h2><p className="mb-0">{t(queue.length === 0 ? 'noCardsToStudy' : 'studyCompleteDescription')}</p></div>}
    </section>
  );
}
