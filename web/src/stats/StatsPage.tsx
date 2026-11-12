import { useState, type ReactNode } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { ApiError } from '../api/client';
import {
  getStatsCardCurve,
  getStatsDistribution,
  getStatsForecast,
  getStatsHeatmap,
  getStatsOverview,
  getStatsRetention,
  type StatsCardCurve,
  type StatsDistribution,
  type StatsForecast,
  type StatsHeatmap,
  type StatsOverview,
  type StatsRetention,
} from '../api/stats';
import { t } from '../i18n/en';
import { Button } from '../ui/Button';

const staleTime = 5 * 60 * 1000;
type RetentionPeriod = StatsRetention['period'];

function substitute(template: string, values: Record<string, string>): string {
  return Object.entries(values).reduce((result, [key, value]) => result.replaceAll(`{${key}}`, value), template);
}

function formatCount(value: number): string {
  return new Intl.NumberFormat('en').format(value);
}

function formatPercent(value: number): string {
  return new Intl.NumberFormat('en', { style: 'percent', maximumFractionDigits: 0 }).format(value);
}

function formatDecimal(value: number): string {
  return new Intl.NumberFormat('en', { maximumFractionDigits: 1 }).format(value);
}

function formatDuration(milliseconds: number): string {
  const totalSeconds = Math.round(milliseconds / 1000);
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  if (minutes === 0) return substitute(t('statsDurationSeconds'), { count: formatCount(seconds) });
  if (seconds === 0) return substitute(t('statsDurationMinutes'), { count: formatCount(minutes) });
  return substitute(t('statsDurationMinutesSeconds'), {
    minutes: formatCount(minutes),
    seconds: formatCount(seconds),
  });
}

function formatDate(value: string, options: Intl.DateTimeFormatOptions = { month: 'short', day: 'numeric' }): string {
  return new Intl.DateTimeFormat('en', options).format(new Date(`${value.slice(0, 10)}T00:00:00`));
}

function formatMonth(value: string): string {
  return new Intl.DateTimeFormat('en', { year: 'numeric', month: 'long', timeZone: 'UTC' })
    .format(new Date(`${value}-01T00:00:00Z`));
}

function languageLabel(language: string): string {
  if (language === 'de') return t('german');
  if (language === 'en') return t('english');
  if (language === 'pl') return t('polish');
  if (language === 'ru') return t('russian');
  return language;
}

function errorMessage(error: unknown): string {
  return error instanceof ApiError && error.serverMessage ? error.serverMessage : t('statsUnavailable');
}

function ChartLoading() {
  return <p role="status" className="m-0 text-on-surface-variant">{t('statsLoading')}</p>;
}

function ChartError({ error, retry }: { error: unknown; retry: () => void }) {
  return (
    <div role="alert" className="space-y-3 rounded-2xl bg-error-container p-4 text-on-error-container">
      <p className="m-0">{errorMessage(error)}</p>
      <Button type="button" variant="tonal" onClick={retry}>{t('statsTryAgain')}</Button>
    </div>
  );
}

function EmptyState({ children }: { children: string }) {
  return <p className="m-0 rounded-2xl bg-surface-container p-4 text-on-surface-variant">{children}</p>;
}

function SectionCard({ id, title, description, children }: {
  id: string;
  title: string;
  description?: string;
  children: ReactNode;
}) {
  return (
    <section className="stats-card space-y-4" aria-labelledby={id}>
      <header className="space-y-1">
        <h2 id={id} className="m-0 text-xl font-semibold text-on-surface">{title}</h2>
        {description && <p className="m-0 text-sm text-on-surface-variant">{description}</p>}
      </header>
      {children}
    </section>
  );
}

function OverviewCards({ overview }: { overview: StatsOverview }) {
  const metrics = [
    {
      label: t('statsReviewsToday'),
      value: formatCount(overview.today.reviews),
      detail: substitute(t('statsReviewsTodayDetail'), {
        newCards: formatCount(overview.today.new_cards),
        reviewCards: formatCount(overview.today.review_cards),
      }),
    },
    {
      label: t('statsRetentionToday'),
      value: overview.today.review_cards > 0 ? formatPercent(overview.today.retention) : t('statsNotAvailable'),
      detail: overview.today.review_cards > 0
        ? substitute(t('statsRetentionTodayDetail'), {
          recalled: formatCount(overview.today.recalled_reviews),
          total: formatCount(overview.today.review_cards),
        })
        : t('statsNoRetention'),
    },
    {
      label: t('statsDueToday'),
      value: formatCount(overview.cards.due),
      detail: t('statsDueTodayDetail'),
    },
    {
      label: t('statsKnownRightNow'),
      value: formatDecimal(overview.known_right_now),
      detail: substitute(t('statsKnownRightNowDetail'), { count: formatCount(overview.cards.review) }),
    },
    {
      label: t('statsStreak'),
      value: substitute(t('statsStreakDays'), { count: formatCount(overview.streak_days) }),
      detail: t('statsStreakDetail'),
    },
    {
      label: t('statsAverageAnswerTime'),
      value: formatDuration(overview.reviews.average_duration_ms),
      detail: substitute(t('statsReviewTimingDetail'), { count: formatCount(overview.reviews.total) }),
    },
    {
      label: t('statsTotalReviewTime'),
      value: formatDuration(overview.reviews.total_duration_ms),
      detail: substitute(t('statsReviewTimingDetail'), { count: formatCount(overview.reviews.total) }),
    },
  ];

  return (
    <section className="stats-metrics" aria-label={t('statsHeading')}>
      {metrics.map((metric) => (
        <article key={metric.label} className="stats-metric rounded-3xl bg-surface-container-low p-5">
          <dl className="m-0 space-y-2">
            <dt className="text-sm font-medium text-on-surface-variant">{metric.label}</dt>
            <dd className="m-0 text-3xl font-semibold tabular-nums text-on-surface">{metric.value}</dd>
          </dl>
          <p className="m-0 mt-3 text-sm text-on-surface-variant">{metric.detail}</p>
        </article>
      ))}
    </section>
  );
}

function retentionLabel(period: RetentionPeriod): string {
  if (period === 'month') return t('statsPeriodMonth');
  if (period === 'year') return t('statsPeriodYear');
  return t('statsPeriodWeek');
}

function retentionPath(points: Array<{ x: number; value: number }>, width: number, height: number, left = 56): string {
  if (points.length === 0) return '';
  const right = width - 12;
  const top = 16;
  const bottom = height - 36;
  return points.map((point, index) => {
    const x = points.length === 1 ? (left + right) / 2 : left + (right - left) * point.x;
    const y = bottom - (bottom - top) * point.value;
    return `${index === 0 ? 'M' : 'L'} ${x} ${y}`;
  }).join(' ');
}

function RetentionChart({ data }: { data: StatsRetention }) {
  const width = 720;
  const height = 250;
  const left = 56;
  const right = width - 12;
  const top = 16;
  const bottom = height - 36;
  const actual = data.points.flatMap((point, index) => point.review_cards > 0 ? [{ point, index }] : []);
  const points = actual.map(({ point, index }) => ({
    x: data.points.length <= 1 ? 0 : index / (data.points.length - 1),
    value: point.actual_retention,
  }));
  const targetY = bottom - (bottom - top) * data.desired_retention;
  const visibleLabels = data.points.flatMap((point, index) => {
    const interval = Math.max(1, Math.ceil(data.points.length / 6));
    return index % interval === 0 || index === data.points.length - 1 ? [{ point, index }] : [];
  });
  const chartTitle = t('statsRetentionChartLabel');

  return (
    <>
      <div className="stats-chart-legend" aria-hidden="true">
        <span><i className="stats-legend-dot stats-legend-primary" />{t('statsActualRetention')}</span>
        <span><i className="stats-legend-dot stats-legend-secondary" />{t('statsDesiredRetention')}</span>
      </div>
      <div className="stats-svg-wrap">
        <svg className="stats-line-chart" viewBox={`0 0 ${width} ${height}`} role="img" aria-label={chartTitle}>
          <title>{chartTitle}</title>
          {[0, 0.5, 1].map((value) => {
            const y = bottom - (bottom - top) * value;
            return (
              <g key={value}>
                <line x1={left} x2={right} y1={y} y2={y} className="stats-chart-gridline" />
                <text x={left - 10} y={y + 4} textAnchor="end" className="stats-chart-axis-label">{formatPercent(value)}</text>
              </g>
            );
          })}
          <line x1={left} x2={right} y1={targetY} y2={targetY} className="stats-line-target" />
          <path d={retentionPath(points, width, height, left)} className="stats-line-actual" />
          {actual.map(({ point, index }) => {
            const x = data.points.length <= 1 ? (left + right) / 2 : left + ((right - left) * index) / (data.points.length - 1);
            const y = bottom - (bottom - top) * point.actual_retention;
            const tooltip = substitute(t('statsRetentionTooltip'), {
              period: point.period,
              actual: formatPercent(point.actual_retention),
              reviews: formatCount(point.review_cards),
              desired: formatPercent(data.desired_retention),
            });
            return <circle key={point.period} cx={x} cy={y} r="4" className="stats-chart-point"><title>{tooltip}</title></circle>;
          })}
          {visibleLabels.map(({ point, index }) => {
            const x = data.points.length <= 1 ? (left + right) / 2 : left + ((right - left) * index) / (data.points.length - 1);
            return <text key={point.period} x={x} y={height - 8} textAnchor="middle" className="stats-chart-axis-label">{point.period}</text>;
          })}
        </svg>
      </div>
      <table className="sr-only">
        <caption>{t('statsRetentionTable')}</caption>
        <thead><tr><th scope="col">{t('statsPeriod')}</th><th scope="col">{t('statsActualRetention')}</th><th scope="col">{t('statsReviews')}</th><th scope="col">{t('statsRecalled')}</th></tr></thead>
        <tbody>{data.points.map((point) => (
          <tr key={point.period}>
            <th scope="row">{point.period}</th>
            <td>{point.review_cards > 0 ? formatPercent(point.actual_retention) : t('statsNoRetention')}</td>
            <td>{formatCount(point.review_cards)}</td>
            <td>{formatCount(point.recalled_reviews)}</td>
          </tr>
        ))}</tbody>
      </table>
    </>
  );
}

function RetentionSection({ period, setPeriod }: { period: RetentionPeriod; setPeriod: (period: RetentionPeriod) => void }) {
  const query = useQuery({
    queryKey: ['stats', 'retention', period],
    queryFn: () => getStatsRetention(period),
    retry: false,
    staleTime,
  });
  const data = query.data;
  const hasActivity = data?.points.some((point) => point.review_cards > 0) === true;
  const description = substitute(t('statsRetentionDescription'), { period: retentionLabel(period).toLowerCase() });

  return (
    <SectionCard id="stats-retention-heading" title={t('statsRetentionTitle')} description={description}>
      <div className="stats-control-row">
        <label htmlFor="stats-retention-period" className="text-sm font-medium text-on-surface">{t('statsRetentionPeriod')}</label>
        <select
          id="stats-retention-period"
          value={period}
          onChange={(event) => setPeriod(event.target.value as RetentionPeriod)}
          className="min-h-12 rounded-xl border border-outline bg-surface-container-lowest px-4 text-base text-on-surface focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary"
        >
          {(['week', 'month', 'year'] as const).map((item) => <option key={item} value={item}>{retentionLabel(item)}</option>)}
        </select>
      </div>
      {query.isPending ? <ChartLoading /> : null}
      {query.isError ? <ChartError error={query.error} retry={() => { void query.refetch(); }} /> : null}
      {data && (hasActivity ? <RetentionChart data={data} /> : <EmptyState>{t('statsNoRetention')}</EmptyState>)}
    </SectionCard>
  );
}

function ForecastChart({ data }: { data: StatsForecast }) {
  const width = 720;
  const height = 220;
  const left = 12;
  const right = width - 8;
  const top = 12;
  const bottom = height - 34;
  const max = Math.max(1, ...data.due.map((item) => item.cards));
  const slot = (right - left) / Math.max(1, data.due.length);
  const barWidth = Math.max(2, slot * 0.68);
  const total = data.due.reduce((sum, item) => sum + item.cards, 0);
  const labelIndexes = data.due.flatMap((item, index) => index % 5 === 0 || index === data.due.length - 1 ? [{ item, index }] : []);
  const chartLabel = t('statsForecastChartLabel');

  return (
    <>
      <p className="m-0 font-medium text-on-surface">{substitute(t('statsForecastTotal'), { count: formatCount(total) })}</p>
      <div className="stats-svg-wrap">
        <svg className="stats-bar-chart" viewBox={`0 0 ${width} ${height}`} role="img" aria-label={chartLabel}>
          <title>{chartLabel}</title>
          <line x1={left} x2={right} y1={bottom} y2={bottom} className="stats-chart-gridline" />
          {data.due.map((item, index) => {
            const barHeight = ((bottom - top) * item.cards) / max;
            const x = left + slot * index + (slot - barWidth) / 2;
            const y = bottom - barHeight;
            const tooltip = substitute(t('statsForecastTooltip'), { date: formatDate(item.date, { year: 'numeric', month: 'short', day: 'numeric' }), count: formatCount(item.cards) });
            return (
              <rect key={item.date} x={x} y={y} width={barWidth} height={Math.max(item.cards > 0 ? 1 : 0, barHeight)} rx="2" className="stats-bar-primary">
                <title>{tooltip}</title>
              </rect>
            );
          })}
          {labelIndexes.map(({ item, index }) => (
            <text key={item.date} x={left + slot * index + slot / 2} y={height - 7} textAnchor="middle" className="stats-chart-axis-label">{formatDate(item.date)}</text>
          ))}
        </svg>
      </div>
      <table className="sr-only">
        <caption>{t('statsForecastTable')}</caption>
        <thead><tr><th scope="col">{t('statsDate')}</th><th scope="col">{t('statsDueCards')}</th></tr></thead>
        <tbody>{data.due.map((item) => <tr key={item.date}><th scope="row">{formatDate(item.date, { year: 'numeric', month: 'long', day: 'numeric' })}</th><td>{formatCount(item.cards)}</td></tr>)}</tbody>
      </table>
    </>
  );
}

function ForecastSection() {
  const query = useQuery({ queryKey: ['stats', 'forecast', 30], queryFn: () => getStatsForecast(30), retry: false, staleTime });
  const data = query.data;
  return (
    <SectionCard id="stats-forecast-heading" title={t('statsForecastTitle')} description={t('statsForecastDescription')}>
      {query.isPending ? <ChartLoading /> : null}
      {query.isError ? <ChartError error={query.error} retry={() => { void query.refetch(); }} /> : null}
      {data && (data.due.length > 0 ? <ForecastChart data={data} /> : <EmptyState>{t('statsNoForecast')}</EmptyState>)}
    </SectionCard>
  );
}

function heatmapLevel(reviews: number, maximum: number): number {
  if (reviews === 0) return 0;
  const ratio = reviews / Math.max(1, maximum);
  if (ratio <= 0.25) return 1;
  if (ratio <= 0.5) return 2;
  if (ratio <= 0.75) return 3;
  return 4;
}

function monthColumns(year: number, firstWeekday: number): Array<{ label: string; column: number; key: string }> {
  return Array.from({ length: 12 }, (_, month) => {
    const date = new Date(Date.UTC(year, month, 1));
    const dayOfYear = Math.floor((date.getTime() - Date.UTC(year, 0, 1)) / 86_400_000);
    return {
      label: new Intl.DateTimeFormat('en', { month: 'short', timeZone: 'UTC' }).format(date),
      column: Math.floor((firstWeekday + dayOfYear) / 7) + 1,
      key: `${year}-${month}`,
    };
  });
}

function ActivityHeatmap({ data }: { data: StatsHeatmap }) {
  const firstWeekday = new Date(Date.UTC(data.year, 0, 1)).getUTCDay();
  const max = Math.max(0, ...data.days.map((day) => day.reviews));
  const blankCells = Array.from({ length: firstWeekday }, (_, index) => index);
  const months = monthColumns(data.year, firstWeekday);
  const weekdays = [
    t('statsWeekdaySunday'), t('statsWeekdayMonday'), t('statsWeekdayTuesday'), t('statsWeekdayWednesday'),
    t('statsWeekdayThursday'), t('statsWeekdayFriday'), t('statsWeekdaySaturday'),
  ];
  const daysWithActivity = data.days.some((day) => day.reviews > 0);
  const label = substitute(t('statsHeatmapLabel'), { year: String(data.year) });

  return (
    <>
      {!daysWithActivity && <EmptyState>{t('statsNoActivity')}</EmptyState>}
      <div className="stats-heatmap-scroll" aria-label={label}>
        <div className="stats-heatmap-months" aria-hidden="true">
          {months.map((month) => <span key={month.key} style={{ gridColumn: `${month.column} / span 4` }}>{month.label}</span>)}
        </div>
        <div className="stats-heatmap-layout">
          <ol className="stats-weekdays" aria-hidden="true">{weekdays.map((day) => <li key={day}>{day}</li>)}</ol>
          <ul className="stats-heatmap-grid" aria-label={label}>
            {blankCells.map((index) => <li key={`blank-${index}`} aria-hidden="true" />)}
            {data.days.map((day) => {
              const count = formatCount(day.reviews);
              const tooltip = substitute(t('statsActivityTooltip'), {
                date: formatDate(day.date, { year: 'numeric', month: 'long', day: 'numeric' }),
                count,
                duration: formatDuration(day.duration_ms),
              });
              return <li key={day.date} className={`stats-heatmap-cell stats-heatmap-level-${heatmapLevel(day.reviews, max)}`} aria-label={tooltip} title={tooltip} />;
            })}
          </ul>
        </div>
      </div>
      <div className="stats-heatmap-legend" aria-label={t('statsActivityTitle')}>
        <span>{t('statsActivityLess')}</span>
        {[0, 1, 2, 3, 4].map((level) => <i key={level} className={`stats-heatmap-cell stats-heatmap-level-${level}`} aria-hidden="true" />)}
        <span>{t('statsActivityMore')}</span>
      </div>
      <table className="sr-only">
        <caption>{t('statsReviewTimingTable')}</caption>
        <thead><tr><th scope="col">{t('statsDate')}</th><th scope="col">{t('statsReviews')}</th><th scope="col">{t('statsTotalReviewTime')}</th></tr></thead>
        <tbody>{data.days.map((day) => (
          <tr key={day.date}>
            <th scope="row">{formatDate(day.date, { year: 'numeric', month: 'long', day: 'numeric' })}</th>
            <td>{formatCount(day.reviews)}</td>
            <td>{formatDuration(day.duration_ms)}</td>
          </tr>
        ))}</tbody>
      </table>
      <div className="space-y-3">
        <h3 className="m-0 text-lg font-semibold text-on-surface">{t('statsVocabularyGrowthTitle')}</h3>
        {data.vocabulary_growth.length === 0 ? <EmptyState>{t('statsNoVocabularyGrowth')}</EmptyState> : (
          <div className="overflow-x-auto">
            <table className="w-full border-collapse text-left text-sm text-on-surface">
              <caption className="sr-only">{t('statsVocabularyGrowthTable')}</caption>
              <thead>
                <tr className="border-b border-outline text-on-surface-variant">
                  <th scope="col" className="px-3 py-2 font-medium">{t('statsDate')}</th>
                  <th scope="col" className="px-3 py-2 font-medium">{t('statsLanguage')}</th>
                  <th scope="col" className="px-3 py-2 text-right font-medium">{t('statsLearned')}</th>
                </tr>
              </thead>
              <tbody>{data.vocabulary_growth.map((item) => (
                <tr key={`${item.month}-${item.language}`} className="border-b border-outline-variant">
                  <th scope="row" className="px-3 py-2 font-normal">{formatMonth(item.month)}</th>
                  <td className="px-3 py-2">{languageLabel(item.language)}</td>
                  <td className="px-3 py-2 text-right tabular-nums">{formatCount(item.learned)}</td>
                </tr>
              ))}</tbody>
            </table>
          </div>
        )}
      </div>
    </>
  );
}

function HeatmapSection({ year, currentYear, setYear }: { year: number; currentYear: number; setYear: (year: number) => void }) {
  const query = useQuery({ queryKey: ['stats', 'heatmap', year], queryFn: () => getStatsHeatmap(year), retry: false, staleTime });
  const availableYears = Array.from({ length: 5 }, (_, index) => currentYear - index);
  return (
    <SectionCard id="stats-activity-heading" title={t('statsActivityTitle')} description={t('statsActivityDescription')}>
      <div className="stats-control-row">
        <label htmlFor="stats-heatmap-year" className="text-sm font-medium text-on-surface">{t('statsHeatmapYear')}</label>
        <select
          id="stats-heatmap-year"
          value={year}
          onChange={(event) => setYear(Number(event.target.value))}
          className="min-h-12 rounded-xl border border-outline bg-surface-container-lowest px-4 text-base text-on-surface focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary"
        >
          {availableYears.map((item) => <option key={item} value={item}>{item}</option>)}
        </select>
      </div>
      {query.isPending ? <ChartLoading /> : null}
      {query.isError ? <ChartError error={query.error} retry={() => { void query.refetch(); }} /> : null}
      {query.data && <ActivityHeatmap data={query.data} />}
    </SectionCard>
  );
}

function distributionStates(data: StatsDistribution): StatsDistribution['states'] {
  const titles = [t('statsStateNew'), t('statsStateLearning'), t('statsStateReview'), t('statsStateRelearning')];
  return titles.map((_, state) => data.states.find((item) => item.state === state) ?? { state, total: 0, young: 0, mature: 0 });
}

function StateDistributionChart({ data }: { data: StatsDistribution }) {
  const rows = distributionStates(data);
  const max = Math.max(1, ...rows.map((row) => row.total));
  const names = [t('statsStateNew'), t('statsStateLearning'), t('statsStateReview'), t('statsStateRelearning')];
  if (rows.every((row) => row.total === 0)) return <EmptyState>{t('statsNoDistribution')}</EmptyState>;

  return (
    <div className="space-y-4">
      {rows.map((row) => (
        <div key={row.state} className="space-y-2">
          <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
            <span className="font-medium text-on-surface">{names[row.state]}</span>
            <span className="text-sm tabular-nums text-on-surface-variant">{substitute(t('statsCardCount'), { count: formatCount(row.total) })}</span>
          </div>
          <div className="stats-meter-track" role="progressbar" aria-label={names[row.state]} aria-valuemin={0} aria-valuemax={max} aria-valuenow={row.total}>
            <span className={`stats-state-fill stats-state-fill-${row.state}`} style={{ width: `${(row.total / max) * 100}%` }} />
          </div>
          <p className="m-0 text-sm text-on-surface-variant">
            {t('statsYoung')}: {formatCount(row.young)} · {t('statsMature')}: {formatCount(row.mature)}
          </p>
        </div>
      ))}
    </div>
  );
}

function Histogram({ bins }: { bins: Array<{ bucket: string; count: number }> }) {
  if (bins.length === 0 || bins.every((bin) => bin.count === 0)) return <EmptyState>{t('statsNoDistribution')}</EmptyState>;
  const max = Math.max(1, ...bins.map((bin) => bin.count));
  return (
    <ul className="stats-histogram">
      {bins.map((bin) => (
        <li key={bin.bucket}>
          <span className="stats-histogram-label">{bin.bucket}</span>
          <div className="stats-meter-track" role="progressbar" aria-label={substitute(t('statsHistogramBarLabel'), { bucket: bin.bucket, count: formatCount(bin.count) })} aria-valuemin={0} aria-valuemax={max} aria-valuenow={bin.count}>
            <span className="stats-histogram-fill" style={{ width: `${(bin.count / max) * 100}%` }} />
          </div>
          <span className="stats-histogram-count">{formatCount(bin.count)}</span>
        </li>
      ))}
    </ul>
  );
}

function DeckStability({ data }: { data: StatsDistribution }) {
  if (data.deck_median_stability.length === 0) return <EmptyState>{t('statsNoDeckStability')}</EmptyState>;
  const max = Math.max(1, ...data.deck_median_stability.map((deck) => deck.median_stability));
  return (
    <ul className="stats-deck-stability">
      {data.deck_median_stability.map((deck) => (
        <li key={deck.deck_id}>
          <span className="stats-deck-name">{deck.deck_name}</span>
          <div className="stats-meter-track" role="progressbar" aria-label={`${deck.deck_name}: ${substitute(t('statsDays'), { count: formatDecimal(deck.median_stability) })}`} aria-valuemin={0} aria-valuemax={max} aria-valuenow={deck.median_stability}>
            <span className="stats-deck-fill" style={{ width: `${(deck.median_stability / max) * 100}%` }} />
          </div>
          <span className="stats-deck-value">{substitute(t('statsDays'), { count: formatDecimal(deck.median_stability) })}</span>
        </li>
      ))}
    </ul>
  );
}

function HardCards({ data, selectedCardId, onSelect }: {
  data: StatsDistribution;
  selectedCardId: number | null;
  onSelect: (cardId: number) => void;
}) {
  if (data.leeches.length === 0) return <EmptyState>{t('statsNoHardCards')}</EmptyState>;
  return (
    <ul className="stats-hard-cards">
      {data.leeches.map((card) => (
        <li key={card.card_id}>
          <button
            type="button"
            aria-pressed={selectedCardId === card.card_id}
            onClick={() => onSelect(card.card_id)}
            className={`stats-hard-card-button ${selectedCardId === card.card_id ? 'stats-hard-card-selected' : ''}`}
          >
            <span className="font-medium text-on-surface">{card.term}</span>
            <span className="text-sm text-on-surface-variant">{substitute(t('statsLapses'), { count: formatCount(card.lapses) })}</span>
          </button>
        </li>
      ))}
    </ul>
  );
}

function CurveChart({ data }: { data: StatsCardCurve }) {
  if (data.curve.length === 0) return <EmptyState>{t('statsNoCurve')}</EmptyState>;
  const width = 720;
  const height = 250;
  const left = 54;
  const right = width - 12;
  const top = 16;
  const bottom = height - 36;
  const points = data.curve.map((point, index) => ({
    x: data.curve.length <= 1 ? 0 : index / (data.curve.length - 1),
    value: point.retrievability,
  }));
  const chartLabel = substitute(t('statsCurveChartLabel'), { term: data.term });
  return (
    <>
      <p className="m-0 text-sm text-on-surface-variant">
        {t('statsCurrentRetrievability')}: {formatPercent(data.retrievability)}
        {data.stability !== undefined && <> · {t('statsStability')}: {substitute(t('statsDays'), { count: formatDecimal(data.stability) })}</>}
        {data.difficulty !== undefined && <> · {t('statsDifficulty')}: {formatDecimal(data.difficulty)}</>}
      </p>
      <div className="stats-svg-wrap">
        <svg className="stats-line-chart" viewBox={`0 0 ${width} ${height}`} role="img" aria-label={chartLabel}>
          <title>{chartLabel}</title>
          {[0, 0.5, 1].map((value) => {
            const y = bottom - (bottom - top) * value;
            return (
              <g key={value}>
                <line x1={left} x2={right} y1={y} y2={y} className="stats-chart-gridline" />
                <text x={left - 10} y={y + 4} textAnchor="end" className="stats-chart-axis-label">{formatPercent(value)}</text>
              </g>
            );
          })}
          <path d={retentionPath(points, width, height, left)} className="stats-line-actual" />
          {data.curve.map((point, index) => {
            const x = data.curve.length <= 1 ? (left + right) / 2 : left + ((right - left) * index) / (data.curve.length - 1);
            const y = bottom - (bottom - top) * point.retrievability;
            const tooltip = substitute(t('statsCurvePointTooltip'), {
              date: formatDate(point.at, { year: 'numeric', month: 'long', day: 'numeric' }),
              retention: formatPercent(point.retrievability),
            });
            return <circle key={`${point.at}-${index}`} cx={x} cy={y} r="3" className="stats-chart-point"><title>{tooltip}</title></circle>;
          })}
          {data.curve.length > 1 && <>
            <text x={left} y={height - 8} textAnchor="start" className="stats-chart-axis-label">{formatDate(data.curve[0]!.at)}</text>
            <text x={right} y={height - 8} textAnchor="end" className="stats-chart-axis-label">{formatDate(data.curve[data.curve.length - 1]!.at)}</text>
          </>}
        </svg>
      </div>
      <table className="sr-only">
        <caption>{t('statsCurveDataTable')}</caption>
        <thead><tr><th scope="col">{t('statsDate')}</th><th scope="col">{t('statsRecallProbability')}</th></tr></thead>
        <tbody>{data.curve.map((point, index) => (
          <tr key={`${point.at}-${index}`}>
            <th scope="row">{formatDate(point.at, { year: 'numeric', month: 'long', day: 'numeric' })}</th>
            <td>{formatPercent(point.retrievability)}</td>
          </tr>
        ))}</tbody>
      </table>
    </>
  );
}

function CurveSection({ cardId, term }: { cardId: number; term: string }) {
  const query = useQuery({ queryKey: ['stats', 'curve', cardId], queryFn: () => getStatsCardCurve(cardId), retry: false, staleTime });
  const description = substitute(t('statsCardCurveDescription'), { term: query.data?.term ?? term });
  return (
    <SectionCard id="stats-curve-heading" title={t('statsCardCurve')} description={description}>
      {query.isPending ? <ChartLoading /> : null}
      {query.isError ? <ChartError error={query.error} retry={() => { void query.refetch(); }} /> : null}
      {query.data && <CurveChart data={query.data} />}
    </SectionCard>
  );
}

function DistributionSection({ data, selectedCardId, onSelect }: {
  data: StatsDistribution;
  selectedCardId: number | null;
  onSelect: (cardId: number) => void;
}) {
  const stateRows = distributionStates(data);
  const stateTotal = stateRows.reduce((sum, row) => sum + row.total, 0);
  return (
    <>
      <SectionCard id="stats-distribution-heading" title={t('statsDistributionTitle')} description={t('statsDistributionDescription')}>
        <div className="stats-distribution-grid">
          <div className="space-y-3">
            <h3 className="m-0 text-base font-semibold text-on-surface">{t('statsCardStates')}</h3>
            <StateDistributionChart data={data} />
            {stateTotal > 0 && <p className="m-0 text-sm text-on-surface-variant">{substitute(t('statsCardCount'), { count: formatCount(stateTotal) })}</p>}
          </div>
          <div className="space-y-3">
            <h3 className="m-0 text-base font-semibold text-on-surface">{t('statsStabilityDistribution')}</h3>
            <Histogram bins={data.stability} />
          </div>
          <div className="space-y-3">
            <h3 className="m-0 text-base font-semibold text-on-surface">{t('statsDifficultyDistribution')}</h3>
            <Histogram bins={data.difficulty} />
          </div>
        </div>
      </SectionCard>
      <SectionCard id="stats-deck-stability-heading" title={t('statsDeckMedianStability')} description={t('statsDeckMedianDescription')}>
        <DeckStability data={data} />
      </SectionCard>
      <SectionCard id="stats-hard-cards-heading" title={t('statsHardCards')} description={t('statsHardCardsDescription')}>
        <HardCards data={data} selectedCardId={selectedCardId} onSelect={onSelect} />
      </SectionCard>
    </>
  );
}

export function StatsPage({ timezone = Intl.DateTimeFormat().resolvedOptions().timeZone }: { timezone?: string }) {
  const queryClient = useQueryClient();
  const currentYear = Number(new Intl.DateTimeFormat('en', { year: 'numeric', timeZone: timezone }).format(new Date()));
  const [period, setPeriod] = useState<RetentionPeriod>('week');
  const [year, setYear] = useState(currentYear);
  const [selectedCardId, setSelectedCardId] = useState<number | null>(null);
  const overviewQuery = useQuery({ queryKey: ['stats', 'overview'], queryFn: getStatsOverview, retry: false, staleTime });
  const distributionQuery = useQuery({ queryKey: ['stats', 'distribution'], queryFn: getStatsDistribution, retry: false, staleTime });
  const refreshAll = async () => queryClient.invalidateQueries({ queryKey: ['stats'] });

  return (
    <div className="stats-page">
      <header className="stats-page-header">
        <div className="space-y-2">
          <p className="m-0 text-sm font-medium uppercase tracking-wide text-primary">{t('stats')}</p>
          <h1 id="stats-page-heading" className="m-0 text-3xl font-semibold text-on-surface">{t('statsHeading')}</h1>
          <p className="m-0 max-w-2xl text-base text-on-surface-variant">{t('statsDescription')}</p>
        </div>
        <Button type="button" variant="tonal" onClick={() => { void refreshAll(); }}>{t('statsRefresh')}</Button>
      </header>

      {overviewQuery.isPending && <ChartLoading />}
      {overviewQuery.isError && <ChartError error={overviewQuery.error} retry={() => { void overviewQuery.refetch(); }} />}
      {overviewQuery.data && <OverviewCards overview={overviewQuery.data} />}

      <div className="stats-primary-grid">
        <RetentionSection period={period} setPeriod={setPeriod} />
        <ForecastSection />
      </div>
      <HeatmapSection year={year} currentYear={currentYear} setYear={setYear} />

      {distributionQuery.isPending && <ChartLoading />}
      {distributionQuery.isError && <ChartError error={distributionQuery.error} retry={() => { void distributionQuery.refetch(); }} />}
      {distributionQuery.data && (
        <DistributionSection data={distributionQuery.data} selectedCardId={selectedCardId} onSelect={setSelectedCardId} />
      )}
      {selectedCardId !== null && <CurveSection key={selectedCardId} cardId={selectedCardId} term={distributionQuery.data?.leeches.find((card) => card.card_id === selectedCardId)?.term ?? ''} />}
    </div>
  );
}
