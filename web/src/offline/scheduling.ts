import type { UserSettings } from '../api/client';
import type { QueueCard, StudyPreview } from '../api/study';
import { previewCard, reviewCard, type CardRating, type FsrsCard, type FsrsSettings } from '../srs/scheduler';

export function offlineFsrsSettings(settings: UserSettings): FsrsSettings {
  return {
    request_retention: settings.request_retention,
    maximum_interval: settings.maximum_interval,
    learning_steps: settings.learning_steps,
    relearning_steps: settings.relearning_steps,
    fsrs_weights: settings.fsrs_weights,
  };
}

export function applyLocalReview(card: QueueCard, rating: CardRating, now: Date, settings: UserSettings): QueueCard {
  const schedulerSettings = offlineFsrsSettings(settings);
  const result = reviewCard(toFsrsCard(card), rating, now, schedulerSettings);
  const updated: QueueCard = {
    ...card,
    state: result.card.state,
    due: result.card.due.toISOString(),
    stability: result.card.stability > 0 ? result.card.stability : undefined,
    difficulty: result.card.difficulty > 0 ? result.card.difficulty : undefined,
    elapsed_days: result.card.elapsed_days,
    scheduled_days: result.card.scheduled_days,
    step: result.card.step,
    reps: result.card.reps,
    lapses: result.card.lapses,
    last_review: result.card.last_review?.toISOString(),
    previews: {} as Record<CardRating, StudyPreview>,
  };
  return withPreviews(updated, now, settings);
}

export function withPreviews(card: QueueCard, now: Date, settings: UserSettings): QueueCard {
  const previews = previewCard(toFsrsCard(card), now, offlineFsrsSettings(settings));
  const updated: QueueCard = { ...card, previews: {} as Record<CardRating, StudyPreview> };
  for (const previewRating of [1, 2, 3, 4] as const) {
    const preview = previews[previewRating].card;
    updated.previews[previewRating] = {
      state: preview.state,
      due: preview.due.toISOString(),
      scheduled_days: preview.scheduled_days,
      interval: goDuration(preview.due.getTime() - now.getTime()),
    };
  }
  return updated;
}

export function toFsrsCard(card: QueueCard): FsrsCard {
  return {
    due: new Date(card.due),
    stability: card.stability ?? 0,
    difficulty: card.difficulty ?? 0,
    elapsed_days: card.elapsed_days,
    scheduled_days: card.scheduled_days,
    step: card.step,
    reps: card.reps,
    lapses: card.lapses,
    state: card.state as FsrsCard['state'],
    last_review: card.last_review ? new Date(card.last_review) : null,
  };
}

function goDuration(milliseconds: number): string {
  if (!Number.isFinite(milliseconds) || milliseconds <= 0) return '0s';
  const ns = Math.round(milliseconds * 1_000_000);
  if (ns < 1_000) return `${ns}ns`;
  if (ns < 1_000_000) return `${trimDecimal(ns / 1_000)}us`;
  if (ns < 1_000_000_000) return `${trimDecimal(ns / 1_000_000)}ms`;
  const seconds = ns / 1_000_000_000;
  if (seconds < 60) return `${trimDecimal(seconds)}s`;
  const whole = Math.floor(seconds);
  const remainder = seconds - whole;
  const minutes = Math.floor(whole / 60);
  const restSeconds = whole % 60 + remainder;
  if (minutes < 60) return `${minutes}m${trimDecimal(restSeconds)}s`;
  const hours = Math.floor(minutes / 60);
  const restMinutes = minutes % 60;
  return `${hours}h${restMinutes}m${trimDecimal(restSeconds)}s`;
}

function trimDecimal(value: number): string {
  return value.toFixed(3).replace(/(?:\.0+|(?:(\.\d*?)0+))$/, '$1');
}
