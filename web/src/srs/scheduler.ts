import {
  fsrs,
  Rating,
  State,
  type CardInput,
  type Grade,
} from 'ts-fsrs';

export type CardState = 0 | 1 | 2 | 3;
export type CardRating = 1 | 2 | 3 | 4;

export interface FsrsSettings {
  request_retention: number;
  maximum_interval: number;
  learning_steps: string;
  relearning_steps: string;
  fsrs_weights: number[] | null;
}

export interface FsrsCard {
  due: Date;
  stability: number;
  difficulty: number;
  elapsed_days: number;
  scheduled_days: number;
  step: number;
  reps: number;
  lapses: number;
  state: CardState;
  last_review: Date | null;
}

export interface FsrsReviewLog {
  rating: CardRating;
  state: CardState;
  scheduled_days: number;
  elapsed_days: number;
  reviewed_at: Date;
}

export interface FsrsReviewResult {
  card: FsrsCard;
  log: FsrsReviewLog;
}

const DAY_MS = 24 * 60 * 60 * 1000;
const MAX_DURATION_NS = 2 ** 63;
const DECAY = -0.5;
const FACTOR = 19 / 81;
const DEFAULT_WEIGHTS = [...fsrs().parameters.w];
const DURATION_UNITS_NS: Record<string, number> = {
  ns: 1,
  us: 1_000,
  'µs': 1_000,
  'μs': 1_000,
  ms: 1_000_000,
  s: 1_000_000_000,
  m: 60_000_000_000,
  h: 3_600_000_000_000,
};

/** Schedules one review using the same FSRS-5 settings and short-step rules as the server. */
export function reviewCard(
  card: FsrsCard,
  rating: CardRating,
  now: Date,
  settings: FsrsSettings,
): FsrsReviewResult {
  if (rating < 1 || rating > 4) throw new RangeError('FSRS rating must be between 1 and 4.');
  validateCard(card, now, settings);
  const scheduler = makeScheduler(settings);
  const nativeCard = toNativeCard(card);
  if (card.state !== State.New && card.last_review) {
    // ts-fsrs v4 computes elapsed days by UTC calendar dates. Retain's Go
    // scheduler uses floored elapsed duration, so normalize only the input
    // timestamp; the result and review log still use the actual review time.
    const elapsedDays = Math.floor((now.getTime() - card.last_review.getTime()) / DAY_MS);
    nativeCard.last_review = new Date(now.getTime() - elapsedDays * DAY_MS);
  }
  const native = scheduler.next(nativeCard, now, ratingToNative(rating));
  const next = fromNativeCard(card, native.card);
  const log: FsrsReviewLog = {
    rating,
    state: native.log.state as CardState,
    scheduled_days: native.log.scheduled_days,
    elapsed_days: native.log.elapsed_days,
    reviewed_at: native.log.review,
  };

  const learningSteps = parseSteps(settings.learning_steps);
  const relearningSteps = parseSteps(settings.relearning_steps);
  switch (card.state) {
    case State.New:
      if (rating === Rating.Again) {
        scheduleStep(next, State.Learning, 0, learningSteps[0], now);
      } else if (rating === Rating.Hard) {
        scheduleStep(next, State.Learning, 0, hardStepInterval(learningSteps, 0), now);
      } else if (rating === Rating.Good) {
        const step = learningSteps.length > 1 ? 1 : 0;
        scheduleStep(next, State.Learning, step, learningSteps[step], now);
      } else {
        next.step = 0;
      }
      break;
    case State.Learning:
    case State.Relearning: {
      const steps = card.state === State.Relearning ? relearningSteps : learningSteps;
      if (rating === Rating.Again) {
        scheduleStep(next, card.state, 0, steps[0], now);
      } else if (rating === Rating.Hard) {
        scheduleStep(next, card.state, card.step, hardStepInterval(steps, card.step), now);
      } else if (rating === Rating.Good && card.step + 1 < steps.length) {
        const step = card.step + 1;
        scheduleStep(next, card.state, step, steps[step], now);
      }
      break;
    }
    case State.Review:
      if (rating === Rating.Again) {
        scheduleStep(next, State.Relearning, 0, relearningSteps[0], now);
      } else {
        next.step = 0;
      }
      break;
  }

  if (next.scheduled_days > settings.maximum_interval) {
    next.scheduled_days = settings.maximum_interval;
    next.due = new Date(now.getTime() + settings.maximum_interval * DAY_MS);
  }
  return { card: next, log };
}

/** Returns the four deterministic interval outcomes. Client-side fuzz is intentionally disabled. */
export function previewCard(
  card: FsrsCard,
  now: Date,
  settings: FsrsSettings,
): Record<CardRating, FsrsReviewResult> {
  return {
    1: reviewCard(card, 1, now, settings),
    2: reviewCard(card, 2, now, settings),
    3: reviewCard(card, 3, now, settings),
    4: reviewCard(card, 4, now, settings),
  };
}

/** Returns the current recall probability, or zero when no valid review state exists. */
export function getRetrievability(card: FsrsCard, now: Date, settings: FsrsSettings): number {
  if (card.state === State.New || !card.last_review || !Number.isFinite(card.stability)
    || card.stability <= 0 || now.getTime() < card.last_review.getTime()) {
    return 0;
  }
  makeScheduler(settings);
  const elapsedDays = (now.getTime() - card.last_review.getTime()) / DAY_MS;
  return Math.pow(1 + FACTOR * elapsedDays / card.stability, DECAY);
}

function makeScheduler(settings: FsrsSettings) {
  const weights = settings.fsrs_weights;
  if (weights !== null && weights.length !== 17 && weights.length !== 19) {
    throw new RangeError('FSRS weights must contain 17 or 19 values.');
  }
  if (weights?.some((weight) => !Number.isFinite(weight))) {
    throw new RangeError('FSRS weights must be finite.');
  }
  if (!Number.isFinite(settings.request_retention) || settings.request_retention <= 0 || settings.request_retention >= 1) {
    throw new RangeError('Request retention must be between 0 and 1.');
  }
  if (!Number.isSafeInteger(settings.maximum_interval) || settings.maximum_interval <= 0) {
    throw new RangeError('Maximum interval must be a positive safe integer.');
  }
  const fullWeights = weights === null
    ? DEFAULT_WEIGHTS
    : [...weights, ...DEFAULT_WEIGHTS.slice(weights.length)];
  const scheduler = fsrs({
    request_retention: settings.request_retention,
    maximum_interval: settings.maximum_interval,
    w: fullWeights,
    enable_fuzz: false,
    enable_short_term: true,
  });
  const w = fullWeights;

  // ts-fsrs rounds intermediate FSRS-5 values to 8 decimal places, while
  // go-fsrs/v3 uses float64 precision. Override its math primitives so the
  // shared golden vectors remain identical, while keeping ts-fsrs's state,
  // review-log, and interval orchestration.
  scheduler.forgetting_curve = (elapsedDays, stability) => (
    Math.pow(1 + FACTOR * elapsedDays / stability, DECAY)
  );
  scheduler.init_stability = (grade) => Math.max(w[grade - 1], 0.1);
  scheduler.init_difficulty = (grade) => constrainDifficulty(w[4] - Math.exp(w[5] * (grade - 1)) + 1);
  scheduler.next_difficulty = (difficulty, grade) => {
    const delta = -w[6] * (grade - Rating.Good);
    const damped = difficulty + ((10 - difficulty) * delta) / 9;
    return constrainDifficulty(w[7] * scheduler.init_difficulty(Rating.Easy) + (1 - w[7]) * damped);
  };
  scheduler.next_short_term_stability = (stability, grade) => (
    stability * Math.exp(w[17] * (grade - Rating.Good + w[18]))
  );
  scheduler.next_recall_stability = (difficulty, stability, retrievability, grade) => {
    const hardPenalty = grade === Rating.Hard ? w[15] : 1;
    const easyBonus = grade === Rating.Easy ? w[16] : 1;
    return stability * (1 + Math.exp(w[8]) * (11 - difficulty) * Math.pow(stability, -w[9])
      * (Math.exp((1 - retrievability) * w[10]) - 1) * hardPenalty * easyBonus);
  };
  scheduler.next_forget_stability = (difficulty, stability, retrievability) => (
    w[11] * Math.pow(difficulty, -w[12]) * (Math.pow(stability + 1, w[13]) - 1)
      * Math.exp((1 - retrievability) * w[14])
  );
  scheduler.next_state = (memoryState, elapsedDays, grade) => {
    if (!memoryState || (memoryState.difficulty === 0 && memoryState.stability === 0)) {
      return {
        difficulty: scheduler.init_difficulty(grade as Grade),
        stability: scheduler.init_stability(grade as Grade),
      };
    }
    const retrievability = scheduler.forgetting_curve(elapsedDays, memoryState.stability);
    let stability: number;
    if (grade === Rating.Again) {
      const minimum = memoryState.stability / Math.exp(w[17] * w[18]);
      stability = Math.min(minimum, scheduler.next_forget_stability(
        memoryState.difficulty,
        memoryState.stability,
        retrievability,
      ));
    } else {
      stability = scheduler.next_recall_stability(
        memoryState.difficulty,
        memoryState.stability,
        retrievability,
        grade as Grade,
      );
    }
    return {
      difficulty: scheduler.next_difficulty(memoryState.difficulty, grade as Grade),
      stability,
    };
  };
  scheduler.calculate_interval_modifier = (retention) => (
    (Math.pow(retention, 1 / DECAY) - 1) / FACTOR
  );
  // Refresh the constructor-computed interval modifier using the unrounded formula.
  scheduler.parameters = { request_retention: settings.request_retention };
  scheduler.next_interval = (stability) => {
    const rawInterval = (stability / FACTOR) * (Math.pow(settings.request_retention, 1 / DECAY) - 1);
    return Math.max(1, Math.min(Math.round(rawInterval), settings.maximum_interval)) as ReturnType<typeof scheduler.next_interval>;
  };
  return scheduler;
}

function validateCard(card: FsrsCard, now: Date, settings: FsrsSettings): void {
  if (!Number.isFinite(now.getTime())) throw new RangeError('Review time must be valid.');
  if (card.state < State.New || card.state > State.Relearning) throw new RangeError('Card state is invalid.');
  if (card.state !== State.New) {
    if (!Number.isFinite(card.stability) || card.stability <= 0
      || !Number.isFinite(card.difficulty) || card.difficulty < 1 || card.difficulty > 10) {
      throw new RangeError('Reviewed cards need valid stability and difficulty.');
    }
    if (!card.last_review || !Number.isFinite(card.last_review.getTime())) {
      throw new RangeError('Reviewed cards need a valid last review time.');
    }
    if (now.getTime() < card.last_review.getTime()) throw new RangeError('Review time precedes the previous review.');
    if ((card.state === State.Learning || card.state === State.Relearning)
      && (!Number.isSafeInteger(card.step) || card.step < 0
        || card.step >= parseSteps(card.state === State.Relearning ? settings.relearning_steps : settings.learning_steps).length)) {
      throw new RangeError('Learning card step is outside the configured steps.');
    }
  }
  parseSteps(settings.learning_steps);
  parseSteps(settings.relearning_steps);
}

function toNativeCard(card: FsrsCard): CardInput {
  return {
    due: card.due,
    stability: card.stability,
    difficulty: card.difficulty,
    elapsed_days: card.elapsed_days,
    scheduled_days: card.scheduled_days,
    reps: card.reps,
    lapses: card.lapses,
    state: card.state as State,
    ...(card.last_review === null ? {} : { last_review: card.last_review }),
  };
}

function fromNativeCard(previous: FsrsCard, native: CardInput): FsrsCard {
  const state = native.state as CardState;
  return {
    due: native.due instanceof Date ? native.due : new Date(native.due),
    stability: native.stability,
    difficulty: native.difficulty,
    elapsed_days: native.elapsed_days,
    scheduled_days: native.scheduled_days,
    step: state === State.Review || state === State.New ? 0 : previous.step,
    reps: native.reps,
    lapses: native.lapses,
    state,
    last_review: native.last_review ? new Date(native.last_review) : null,
  };
}

function scheduleStep(card: FsrsCard, state: CardState, step: number, durationMs: number, now: Date): void {
  card.state = state;
  card.step = step;
  card.scheduled_days = Math.floor(durationMs / DAY_MS);
  card.due = new Date(now.getTime() + durationMs);
}

function hardStepInterval(steps: number[], step: number): number {
  const current = steps[step];
  const next = steps[step + 1];
  return next === undefined ? current + Math.trunc(current / 2) : current + Math.trunc((next - current) / 2);
}

function constrainDifficulty(value: number): number {
  return Math.min(Math.max(value, 1), 10);
}

function parseSteps(value: string): number[] {
  const durations = value.trim().split(/\s+/).flatMap((part) => parseDuration(part));
  if (durations.length === 0 || durations.some((step, index) => step <= 0 || (index > 0 && step <= durations[index - 1]))) {
    throw new RangeError('Learning steps must be positive and strictly increasing.');
  }
  return durations;
}

function parseDuration(value: string): number[] {
  const token = /((?:\d+(?:\.\d*)?|\.\d+))(ns|us|µs|μs|ms|s|m|h)/gy;
  let offset = 0;
  let durationNs = 0;
  while (offset < value.length) {
    token.lastIndex = offset;
    const match = token.exec(value);
    if (!match) throw new RangeError(`Invalid learning step duration: ${value}`);
    durationNs += Number(match[1]) * DURATION_UNITS_NS[match[2]];
    if (!Number.isFinite(durationNs) || durationNs >= MAX_DURATION_NS) {
      throw new RangeError(`Learning step duration is too large: ${value}`);
    }
    offset = token.lastIndex;
  }
  const durationMs = durationNs / 1_000_000;
  if (!Number.isSafeInteger(durationMs)) {
    throw new RangeError(`Learning step must be an exact millisecond duration: ${value}`);
  }
  return [durationMs];
}

function ratingToNative(rating: CardRating): Grade {
  switch (rating) {
    case 1: return Rating.Again;
    case 2: return Rating.Hard;
    case 3: return Rating.Good;
    case 4: return Rating.Easy;
    default: throw new RangeError('FSRS rating must be between 1 and 4.');
  }
}
