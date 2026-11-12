import { useEffect, useState, type FormEvent } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { ApiError, patchMeSettings, type MeResponse, type UserSettings, type UserSettingsPatch } from '../api/client';
import { meQueryKey } from '../auth/session';
import { updateCachedSettings } from '../offline/database';
import { t } from '../i18n/en';
import { Button } from '../ui/Button';

interface SettingsDraft {
  newPerDay: string;
  maxReviewsPerDay: string;
  requestRetention: string;
  maximumInterval: string;
  learningSteps: string;
  relearningSteps: string;
  ttsAuto: boolean;
  timezone: string;
  dayStartHour: string;
  reminderEnabled: boolean;
  reminderTime: string;
  themeMode: UserSettings['theme_mode'];
}

type SettingsField = keyof SettingsDraft;
type FieldErrors = Partial<Record<SettingsField, string>>;

function createDraft(settings: UserSettings): SettingsDraft {
  return {
    newPerDay: String(settings.new_per_day),
    maxReviewsPerDay: String(settings.max_reviews_per_day),
    requestRetention: String(settings.request_retention),
    maximumInterval: String(settings.maximum_interval),
    learningSteps: settings.learning_steps,
    relearningSteps: settings.relearning_steps,
    ttsAuto: settings.tts_auto,
    timezone: settings.timezone,
    dayStartHour: String(settings.day_start_hour),
    reminderEnabled: settings.reminder_time !== null,
    reminderTime: settings.reminder_time ?? '',
    themeMode: settings.theme_mode,
  };
}

function parseNonNegativeInteger(value: string): number | null {
  if (!/^\d+$/.test(value)) return null;
  const parsed = Number(value);
  return Number.isSafeInteger(parsed) ? parsed : null;
}

function parseLearningStep(value: string): number | null {
  const units: Record<string, number> = {
    ns: 0.000001, us: 0.001, 'µs': 0.001, 'μs': 0.001, ms: 1,
    s: 1000, m: 60_000, h: 3_600_000,
  };
  const token = /((?:\d+(?:\.\d*)?|\.\d+))(ns|us|µs|μs|ms|s|m|h)/gy;
  let offset = 0;
  let milliseconds = 0;
  while (offset < value.length) {
    token.lastIndex = offset;
    const match = token.exec(value);
    if (!match) return null;
    milliseconds += Number(match[1]) * units[match[2]!];
    if (!Number.isSafeInteger(milliseconds)) return null;
    offset = token.lastIndex;
  }
  return milliseconds > 0 ? milliseconds : null;
}

function validLearningSteps(value: string): boolean {
  const steps = value.trim().split(/\s+/).map(parseLearningStep);
  return steps.length > 0 && steps.every((step, index) => (
    step !== null && (index === 0 || step > (steps[index - 1] ?? Number.POSITIVE_INFINITY))
  ));
}

function validTimezone(value: string): boolean {
  if (!value) return false;
  try {
    new Intl.DateTimeFormat('en', { timeZone: value }).format();
    return true;
  } catch {
    return false;
  }
}

function getChanges(settings: UserSettings, draft: SettingsDraft): { patch: UserSettingsPatch; errors: FieldErrors } {
  const patch: UserSettingsPatch = {};
  const errors: FieldErrors = {};
  const newPerDay = parseNonNegativeInteger(draft.newPerDay);
  const maxReviewsPerDay = parseNonNegativeInteger(draft.maxReviewsPerDay);
  const maximumInterval = parseNonNegativeInteger(draft.maximumInterval);
  const requestRetention = Number(draft.requestRetention);
  const dayStartHour = parseNonNegativeInteger(draft.dayStartHour);
  const timezone = draft.timezone.trim();
  const reminderTime = draft.reminderTime;

  if (newPerDay === null) errors.newPerDay = t('invalidDailyLimit');
  else if (newPerDay !== settings.new_per_day) patch.new_per_day = newPerDay;

  if (maxReviewsPerDay === null) errors.maxReviewsPerDay = t('invalidDailyLimit');
  else if (maxReviewsPerDay !== settings.max_reviews_per_day) patch.max_reviews_per_day = maxReviewsPerDay;

  if (!Number.isFinite(requestRetention) || requestRetention <= 0 || requestRetention >= 1) {
    errors.requestRetention = t('invalidRequestRetention');
  } else if (requestRetention !== settings.request_retention) {
    patch.request_retention = requestRetention;
  }

  if (maximumInterval === null || maximumInterval === 0) errors.maximumInterval = t('invalidMaximumInterval');
  else if (maximumInterval !== settings.maximum_interval) patch.maximum_interval = maximumInterval;

  if (!validLearningSteps(draft.learningSteps)) errors.learningSteps = t('invalidLearningSteps');
  else if (draft.learningSteps.trim() !== settings.learning_steps) patch.learning_steps = draft.learningSteps.trim();

  if (!validLearningSteps(draft.relearningSteps)) errors.relearningSteps = t('invalidLearningSteps');
  else if (draft.relearningSteps.trim() !== settings.relearning_steps) patch.relearning_steps = draft.relearningSteps.trim();

  if (!validTimezone(timezone)) errors.timezone = t('invalidTimezone');
  else if (timezone !== settings.timezone) patch.timezone = timezone;

  if (dayStartHour === null || dayStartHour > 23) errors.dayStartHour = t('invalidDayStartHour');
  else if (dayStartHour !== settings.day_start_hour) patch.day_start_hour = dayStartHour;

  if (draft.reminderEnabled) {
    if (!/^(?:[01]\d|2[0-3]):[0-5]\d$/.test(reminderTime)) errors.reminderTime = t('invalidReminderTime');
    else if (reminderTime !== settings.reminder_time) patch.reminder_time = reminderTime;
  } else if (settings.reminder_time !== null) {
    patch.reminder_time = null;
  }

  if (draft.ttsAuto !== settings.tts_auto) patch.tts_auto = draft.ttsAuto;
  if (draft.themeMode !== settings.theme_mode) patch.theme_mode = draft.themeMode;

  return { patch, errors };
}

function errorMessage(error: unknown): string {
  return error instanceof ApiError && error.serverMessage ? error.serverMessage : t('requestFailed');
}

const fieldClass = 'min-h-12 w-full rounded-xl border border-outline bg-surface-container-lowest px-4 text-on-surface focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary';
const fieldLabelClass = 'grid gap-2 text-on-surface';

export function SettingsPage({ settings }: { settings: UserSettings }) {
  const queryClient = useQueryClient();
  const [draft, setDraft] = useState(() => createDraft(settings));
  const [showValidation, setShowValidation] = useState(false);
  const [saved, setSaved] = useState(false);
  const mutation = useMutation({
    mutationFn: patchMeSettings,
    onSuccess: async (nextSettings) => {
      try {
        await updateCachedSettings(nextSettings);
      } catch {
        // Settings are persisted by the server; offline profile caching is best-effort.
      }
      queryClient.setQueryData<MeResponse | null>(meQueryKey, (current) => (
        current ? { ...current, settings: nextSettings } : current
      ));
      setDraft(createDraft(nextSettings));
      setShowValidation(false);
      setSaved(true);
    },
  });

  useEffect(() => {
    setDraft(createDraft(settings));
  }, [settings]);

  const { patch, errors } = getChanges(settings, draft);
  const hasChanges = Object.keys(patch).length > 0;
  const hasErrors = Object.keys(errors).length > 0;

  function updateDraft(field: SettingsField, value: string | boolean) {
    setDraft((current) => ({ ...current, [field]: value }));
    setSaved(false);
    mutation.reset();
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setShowValidation(true);
    if (hasErrors || !hasChanges) return;
    mutation.mutate(patch);
  }

  function describedBy(id: string, error: string | undefined): string {
    return error ? `${id}-hint ${id}-error` : `${id}-hint`;
  }

  return (
    <div className="mx-auto grid w-full max-w-4xl gap-6">
      <div className="grid gap-2">
        <h1 id="settings-title">{t('settingsHeading')}</h1>
        <p className="max-w-2xl text-on-surface-variant">{t('settingsDescription')}</p>
      </div>

      <form aria-labelledby="settings-title" noValidate onSubmit={submit} className="grid gap-4">
        <section aria-labelledby="learning-settings-heading" className="grid gap-5 rounded-3xl border border-outline-variant bg-surface-container-low p-5 medium:p-7">
          <div className="grid gap-1">
            <h2 id="learning-settings-heading" className="text-xl font-medium">{t('learningSettings')}</h2>
            <p className="text-on-surface-variant">{t('learningSettingsDescription')}</p>
          </div>
          <div className="grid gap-5 medium:grid-cols-2">
            <div className={fieldLabelClass}>
              <label htmlFor="new-per-day" className="font-medium">{t('newCardsPerDay')}</label>
              <input
                id="new-per-day"
                type="number"
                inputMode="numeric"
                min="0"
                step="1"
                required
                disabled={mutation.isPending}
                value={draft.newPerDay}
                aria-invalid={showValidation && errors.newPerDay ? 'true' : undefined}
                aria-describedby={describedBy('new-per-day', showValidation ? errors.newPerDay : undefined)}
                onChange={(event) => updateDraft('newPerDay', event.target.value)}
                className={fieldClass}
              />
              <p id="new-per-day-hint" className="m-0 text-sm text-on-surface-variant">{t('newCardsPerDayHint')}</p>
              {showValidation && errors.newPerDay && <span id="new-per-day-error" className="text-sm text-error">{errors.newPerDay}</span>}
            </div>
            <div className={fieldLabelClass}>
              <label htmlFor="max-reviews-per-day" className="font-medium">{t('maxReviewsPerDay')}</label>
              <input
                id="max-reviews-per-day"
                type="number"
                inputMode="numeric"
                min="0"
                step="1"
                required
                disabled={mutation.isPending}
                value={draft.maxReviewsPerDay}
                aria-invalid={showValidation && errors.maxReviewsPerDay ? 'true' : undefined}
                aria-describedby={describedBy('max-reviews-per-day', showValidation ? errors.maxReviewsPerDay : undefined)}
                onChange={(event) => updateDraft('maxReviewsPerDay', event.target.value)}
                className={fieldClass}
              />
              <p id="max-reviews-per-day-hint" className="m-0 text-sm text-on-surface-variant">{t('maxReviewsPerDayHint')}</p>
              {showValidation && errors.maxReviewsPerDay && <span id="max-reviews-per-day-error" className="text-sm text-error">{errors.maxReviewsPerDay}</span>}
            </div>
          </div>
          <div className="grid gap-5 medium:grid-cols-2">
            <div className={fieldLabelClass}>
              <label htmlFor="request-retention" className="font-medium">{t('desiredRetention')}</label>
              <input
                id="request-retention"
                type="number"
                inputMode="decimal"
                min="0.01"
                max="0.99"
                step="0.01"
                required
                disabled={mutation.isPending}
                value={draft.requestRetention}
                aria-invalid={showValidation && errors.requestRetention ? 'true' : undefined}
                aria-describedby={describedBy('request-retention', showValidation ? errors.requestRetention : undefined)}
                onChange={(event) => updateDraft('requestRetention', event.target.value)}
                className={fieldClass}
              />
              <p id="request-retention-hint" className="m-0 text-sm text-on-surface-variant">{t('desiredRetentionHint')}</p>
              {showValidation && errors.requestRetention && <span id="request-retention-error" className="text-sm text-error">{errors.requestRetention}</span>}
            </div>
            <div className={fieldLabelClass}>
              <label htmlFor="maximum-interval" className="font-medium">{t('maximumInterval')}</label>
              <input
                id="maximum-interval"
                type="number"
                inputMode="numeric"
                min="1"
                step="1"
                required
                disabled={mutation.isPending}
                value={draft.maximumInterval}
                aria-invalid={showValidation && errors.maximumInterval ? 'true' : undefined}
                aria-describedby={describedBy('maximum-interval', showValidation ? errors.maximumInterval : undefined)}
                onChange={(event) => updateDraft('maximumInterval', event.target.value)}
                className={fieldClass}
              />
              <p id="maximum-interval-hint" className="m-0 text-sm text-on-surface-variant">{t('maximumIntervalHint')}</p>
              {showValidation && errors.maximumInterval && <span id="maximum-interval-error" className="text-sm text-error">{errors.maximumInterval}</span>}
            </div>
          </div>
          <div className="grid gap-5 medium:grid-cols-2">
            {([
              ['learning-steps', 'learningSteps', 'learningStepsHint'],
              ['relearning-steps', 'relearningSteps', 'relearningStepsHint'],
            ] as const).map(([id, field, hint]) => (
              <div key={id} className={fieldLabelClass}>
                <label htmlFor={id} className="font-medium">{t(field)}</label>
                <input
                  id={id}
                  type="text"
                  required
                  disabled={mutation.isPending}
                  value={draft[field]}
                  aria-invalid={showValidation && errors[field] ? 'true' : undefined}
                  aria-describedby={describedBy(id, showValidation ? errors[field] : undefined)}
                  onChange={(event) => updateDraft(field, event.target.value)}
                  className={fieldClass}
                />
                <p id={`${id}-hint`} className="m-0 text-sm text-on-surface-variant">{t(hint)}</p>
                {showValidation && errors[field] && <span id={`${id}-error`} className="text-sm text-error">{errors[field]}</span>}
              </div>
            ))}
          </div>
        </section>

        <section aria-labelledby="schedule-settings-heading" className="grid gap-5 rounded-3xl border border-outline-variant bg-surface-container-low p-5 medium:p-7">
          <div className="grid gap-1">
            <h2 id="schedule-settings-heading" className="text-xl font-medium">{t('studySchedule')}</h2>
            <p className="text-on-surface-variant">{t('studyScheduleDescription')}</p>
          </div>
          <div className="grid gap-5 medium:grid-cols-2">
            <div className={fieldLabelClass}>
              <label htmlFor="timezone" className="font-medium">{t('timezone')}</label>
              <input
                id="timezone"
                type="text"
                autoComplete="off"
                required
                disabled={mutation.isPending}
                value={draft.timezone}
                aria-invalid={showValidation && errors.timezone ? 'true' : undefined}
                aria-describedby={describedBy('timezone', showValidation ? errors.timezone : undefined)}
                onChange={(event) => updateDraft('timezone', event.target.value)}
                className={fieldClass}
              />
              <p id="timezone-hint" className="m-0 text-sm text-on-surface-variant">{t('timezoneHint')}</p>
              {showValidation && errors.timezone && <span id="timezone-error" className="text-sm text-error">{errors.timezone}</span>}
            </div>
            <div className={fieldLabelClass}>
              <label htmlFor="day-start-hour" className="font-medium">{t('dayStartHour')}</label>
              <input
                id="day-start-hour"
                type="number"
                inputMode="numeric"
                min="0"
                max="23"
                step="1"
                required
                disabled={mutation.isPending}
                value={draft.dayStartHour}
                aria-invalid={showValidation && errors.dayStartHour ? 'true' : undefined}
                aria-describedby={describedBy('day-start-hour', showValidation ? errors.dayStartHour : undefined)}
                onChange={(event) => updateDraft('dayStartHour', event.target.value)}
                className={fieldClass}
              />
              <p id="day-start-hour-hint" className="m-0 text-sm text-on-surface-variant">{t('dayStartHourHint')}</p>
              {showValidation && errors.dayStartHour && <span id="day-start-hour-error" className="text-sm text-error">{errors.dayStartHour}</span>}
            </div>
          </div>
        </section>

        <section aria-labelledby="reminder-settings-heading" className="grid gap-5 rounded-3xl border border-outline-variant bg-surface-container-low p-5 medium:p-7">
          <div className="grid gap-1">
            <h2 id="reminder-settings-heading" className="text-xl font-medium">{t('reminderSettings')}</h2>
            <p className="text-on-surface-variant">{t('reminderSettingsDescription')}</p>
          </div>
          <div className="grid gap-3">
            <label htmlFor="reminders-enabled" className="flex min-h-12 cursor-pointer items-center gap-3 rounded-xl text-on-surface focus-within:outline focus-within:outline-2 focus-within:outline-offset-2 focus-within:outline-primary">
              <input
                id="reminders-enabled"
                type="checkbox"
                aria-describedby="reminders-enabled-hint"
                disabled={mutation.isPending}
                checked={draft.reminderEnabled}
                onChange={(event) => updateDraft('reminderEnabled', event.target.checked)}
                className="h-5 w-5 accent-primary"
              />
              <span>{t('dailyReminders')}</span>
            </label>
            <p id="reminders-enabled-hint" className="m-0 ps-8 text-sm text-on-surface-variant">{t('dailyRemindersHint')}</p>
            {draft.reminderEnabled && (
              <div className={fieldLabelClass}>
                <label htmlFor="reminder-time" className="font-medium">{t('reminderTime')}</label>
                <input
                  id="reminder-time"
                  type="time"
                  step="60"
                  required
                  disabled={mutation.isPending}
                  value={draft.reminderTime}
                  aria-invalid={showValidation && errors.reminderTime ? 'true' : undefined}
                  aria-describedby={describedBy('reminder-time', showValidation ? errors.reminderTime : undefined)}
                  onChange={(event) => updateDraft('reminderTime', event.target.value)}
                  className={fieldClass}
                />
                <p id="reminder-time-hint" className="m-0 text-sm text-on-surface-variant">{t('reminderTimeHint')}</p>
                {showValidation && errors.reminderTime && <span id="reminder-time-error" className="text-sm text-error">{errors.reminderTime}</span>}
              </div>
            )}
          </div>
        </section>

        <section aria-labelledby="appearance-settings-heading" className="grid gap-5 rounded-3xl border border-outline-variant bg-surface-container-low p-5 medium:p-7">
          <div className="grid gap-1">
            <h2 id="appearance-settings-heading" className="text-xl font-medium">{t('appearanceSettings')}</h2>
            <p className="text-on-surface-variant">{t('appearanceSettingsDescription')}</p>
          </div>
          <div className={fieldLabelClass}>
            <label htmlFor="theme-mode" className="font-medium">{t('themeMode')}</label>
            <select
              id="theme-mode"
              disabled={mutation.isPending}
              value={draft.themeMode}
              onChange={(event) => updateDraft('themeMode', event.target.value as UserSettings['theme_mode'])}
              className={fieldClass}
            >
              {(['light', 'dark', 'system'] as const).map((mode) => <option key={mode} value={mode}>{t(mode)}</option>)}
            </select>
            <p className="m-0 text-sm text-on-surface-variant">{t('themeModeHint')}</p>
          </div>
        </section>

        <section aria-labelledby="pronunciation-settings-heading" className="grid gap-4 rounded-3xl border border-outline-variant bg-surface-container-low p-5 medium:p-7">
          <div className="grid gap-1">
            <h2 id="pronunciation-settings-heading" className="text-xl font-medium">{t('pronunciationSettings')}</h2>
            <p className="text-on-surface-variant">{t('pronunciationSettingsDescription')}</p>
          </div>
          <div className="grid gap-1">
            <label htmlFor="tts-auto" className="flex min-h-12 cursor-pointer items-center gap-3 rounded-xl text-on-surface focus-within:outline focus-within:outline-2 focus-within:outline-offset-2 focus-within:outline-primary">
              <input
                id="tts-auto"
                type="checkbox"
                aria-describedby="tts-auto-hint"
                disabled={mutation.isPending}
                checked={draft.ttsAuto}
                onChange={(event) => updateDraft('ttsAuto', event.target.checked)}
                className="h-5 w-5 accent-primary"
              />
              <span>{t('ttsAuto')}</span>
            </label>
            <p id="tts-auto-hint" className="m-0 ps-8 text-sm text-on-surface-variant">{t('ttsAutoHint')}</p>
          </div>
        </section>

        {mutation.isError && <p role="alert" className="rounded-xl border-s-4 border-error bg-error-container p-4 text-on-error-container">{errorMessage(mutation.error)}</p>}
        {saved && <p role="status" aria-live="polite" className="text-on-surface-variant">{t('settingsSaved')}</p>}
        <div className="flex flex-wrap items-center gap-4">
          <Button type="submit" disabled={mutation.isPending || (!hasChanges && !hasErrors)}>
            {t(mutation.isPending ? 'savingSettings' : 'saveSettings')}
          </Button>
          {showValidation && hasErrors && <p role="alert" className="text-error">{t('settingsValidationError')}</p>}
        </div>
      </form>
    </div>
  );
}
