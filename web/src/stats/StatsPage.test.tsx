// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { t } from '../i18n/en';
import { StatsPage } from './StatsPage';

const fetchMock = vi.fn<typeof fetch>();
const now = new Date();
const year = now.getFullYear();

function response(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function fixture(path: string): unknown {
  if (path === '/api/v1/stats/overview') {
    return {
      timezone: 'UTC',
      day_start_hour: 4,
      streak_days: 5,
      today: { reviews: 14, new_cards: 2, review_cards: 12, recalled_reviews: 11, retention: 11 / 12, duration_ms: 36000 },
      cards: { total: 90, new: 10, learning: 5, review: 70, relearning: 5, due: 13, young: 22, mature: 48, leeches: 1 },
      reviews: { total: 440, total_duration_ms: 1320000, average_duration_ms: 3000 },
      known_right_now: 61.4,
      desired_retention: 0.9,
    };
  }
  if (path.startsWith('/api/v1/stats/forecast?')) {
    return {
      days: 30,
      due: Array.from({ length: 30 }, (_, index) => ({
        date: new Date(Date.UTC(year, 0, index + 1)).toISOString().slice(0, 10),
        cards: index === 0 ? 5 : index === 4 ? 2 : 0,
      })),
    };
  }
  if (path.startsWith('/api/v1/stats/retention?')) {
    const period = new URL(path, window.location.origin).searchParams.get('period') ?? 'week';
    return {
      period,
      desired_retention: 0.9,
      points: period === 'month' ? [
        { period: `${year}-01`, review_cards: 10, recalled_reviews: 9, actual_retention: 0.9 },
        { period: `${year}-02`, review_cards: 20, recalled_reviews: 17, actual_retention: 0.85 },
      ] : [
        { period: `${year}-W01`, review_cards: 12, recalled_reviews: 11, actual_retention: 11 / 12 },
        { period: `${year}-W02`, review_cards: 8, recalled_reviews: 7, actual_retention: 0.875 },
      ],
    };
  }
  if (path.startsWith('/api/v1/stats/heatmap?')) {
    const selectedYear = Number(new URL(path, window.location.origin).searchParams.get('year'));
    const dayCount = (selectedYear % 4 === 0 && (selectedYear % 100 !== 0 || selectedYear % 400 === 0)) ? 366 : 365;
    return {
      year: selectedYear,
      days: Array.from({ length: dayCount }, (_, index) => ({
        date: new Date(Date.UTC(selectedYear, 0, index + 1)).toISOString().slice(0, 10),
        reviews: index === 0 ? 4 : 0,
        duration_ms: index === 0 ? 12000 : 0,
      })),
      vocabulary_growth: [
        { month: `${selectedYear}-01`, language: 'de', learned: 2 },
        { month: `${selectedYear}-02`, language: 'en', learned: 3 },
      ],
    };
  }
  if (path === '/api/v1/stats/distribution') {
    return {
      states: [
        { state: 0, total: 10, young: 0, mature: 0 },
        { state: 1, total: 5, young: 3, mature: 2 },
        { state: 2, total: 70, young: 22, mature: 48 },
        { state: 3, total: 5, young: 4, mature: 1 },
      ],
      stability: [{ bucket: '0–1', count: 8 }, { bucket: '1–3', count: 12 }],
      difficulty: [{ bucket: '1–3', count: 14 }, { bucket: '3–5', count: 8 }],
      leeches: [{ card_id: 42, term: 'die Katze', direction: 'forward', lapses: 8 }],
      deck_median_stability: [{ deck_id: 7, deck_name: 'Chapter 1', median_stability: 9.5 }],
    };
  }
  if (path === '/api/v1/stats/cards/42/curve') {
    return {
      card_id: 42,
      term: 'die Katze',
      direction: 'forward',
      state: 2,
      stability: 9.5,
      difficulty: 4,
      last_review: `${year}-01-01T12:00:00Z`,
      retrievability: 0.94,
      reviews: [],
      curve: [
        { at: `${year}-01-01T12:00:00Z`, retrievability: 1 },
        { at: `${year}-01-03T12:00:00Z`, retrievability: 0.5 },
        { at: `${year}-01-05T12:00:00Z`, retrievability: 0 },
      ],
    };
  }
  throw new Error(`Unexpected request: ${path}`);
}

function renderPage(timezone?: string) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={queryClient}><StatsPage timezone={timezone} /></QueryClientProvider>);
}

beforeEach(() => {
  fetchMock.mockReset();
  fetchMock.mockImplementation(async (input) => {
    const url = String(input);
    return response(fixture(url));
  });
  vi.stubGlobal('fetch', fetchMock);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('StatsPage', () => {
  it('shows overview metrics, accessible charts, distributions, and a selected card curve', async () => {
    const user = userEvent.setup();
    renderPage();

    expect(await screen.findByText('61.4')).toBeTruthy();
    expect(screen.getByText(t('statsReviewsToday'))).toBeTruthy();
    expect(screen.getByText(t('statsAverageAnswerTime'))).toBeTruthy();
    expect(screen.getByText('3 sec')).toBeTruthy();
    expect(screen.getByText(t('statsTotalReviewTime'), { selector: 'dt' })).toBeTruthy();
    expect(screen.getByText('22 min')).toBeTruthy();
    expect(screen.getByRole('img', { name: t('statsRetentionChartLabel') })).toBeTruthy();
    expect(screen.getByRole('img', { name: t('statsForecastChartLabel') })).toBeTruthy();
    expect(await screen.findByRole('list', { name: t('statsHeatmapLabel').replace('{year}', String(year)) })).toBeTruthy();
    const growthTable = screen.getByRole('table', { name: t('statsVocabularyGrowthTable') });
    expect(growthTable.textContent).toContain(t('german'));
    expect(growthTable.textContent).toContain(t('english'));
    expect(growthTable.textContent).toContain('January');
    const timingTable = screen.getByRole('table', { name: t('statsReviewTimingTable') });
    expect(timingTable.textContent).toContain('12 sec');
    expect(screen.getByText(t('statsStabilityDistribution'))).toBeTruthy();
    expect(screen.getByText(t('statsDifficultyDistribution'))).toBeTruthy();
    expect(screen.getByText('Chapter 1')).toBeTruthy();

    await user.click(screen.getByRole('button', { name: /die Katze/ }));
    const curveChart = await screen.findByRole('img', { name: t('statsCurveChartLabel').replace('{term}', 'die Katze') });
    expect(curveChart.querySelector('.stats-line-actual')?.getAttribute('d')).toBe('M 54 16 L 381 115 L 708 214');
    const curveTable = screen.getByRole('table', { name: t('statsCurveDataTable') });
    expect(curveTable.textContent).toContain('January 1');
    expect(curveTable.textContent).toContain('January 3');
    expect(curveTable.textContent).toContain('January 5');
    expect(curveTable.textContent).toContain('100%');
    expect(curveTable.textContent).toContain('50%');
    expect(curveTable.textContent).toContain('0%');
    expect(fetchMock.mock.calls.map(([url]) => String(url))).toContain('/api/v1/stats/cards/42/curve');
  });

  it('uses the configured timezone year for heatmap options across the year boundary', async () => {
    const RealDate = Date;
    const fixedNow = RealDate.parse('2027-01-01T00:30:00.000Z');
    class FixedDate extends RealDate {
      constructor(value?: string | number | Date) {
        super(value === undefined ? fixedNow : value instanceof RealDate ? value.getTime() : value);
      }

      static now() {
        return fixedNow;
      }
    }
    vi.stubGlobal('Date', FixedDate);

    const browserYear = new RealDate(fixedNow).getFullYear();
    const timezone = browserYear === 2027 ? 'America/Adak' : 'Pacific/Kiritimati';
    const configuredYear = Number(new Intl.DateTimeFormat('en', { year: 'numeric', timeZone: timezone }).format(new RealDate(fixedNow)));
    renderPage(timezone);

    const yearSelect = await screen.findByLabelText(t('statsHeatmapYear')) as HTMLSelectElement;
    expect(yearSelect.options[0]?.value).toBe(String(configuredYear));
    expect(configuredYear).not.toBe(browserYear);
    await waitFor(() => expect(fetchMock.mock.calls.map(([url]) => String(url))).toContain(`/api/v1/stats/heatmap?year=${configuredYear}`));
  });

  it('changes retention period and recovers from a section error by retrying', async () => {
    const user = userEvent.setup();
    let retentionCalls = 0;
    fetchMock.mockImplementation(async (input) => {
      const url = String(input);
      if (url === '/api/v1/stats/overview') return response(fixture(url));
      if (url.startsWith('/api/v1/stats/forecast?')) return response(fixture(url));
      if (url.startsWith('/api/v1/stats/retention?')) {
        retentionCalls += 1;
        if (retentionCalls === 1) return response({ error: { code: 'unavailable', message: 'Analytics are unavailable.' } }, 503);
        return response(fixture(url));
      }
      return response(fixture(url));
    });
    renderPage();

    expect((await screen.findByRole('alert')).textContent).toContain('Analytics are unavailable.');
    await user.click(screen.getAllByRole('button', { name: t('statsTryAgain') })[0]!);
    expect(await screen.findByRole('img', { name: t('statsRetentionChartLabel') })).toBeTruthy();
    await user.selectOptions(screen.getByLabelText(t('statsRetentionPeriod')), 'month');
    await waitFor(() => expect(fetchMock.mock.calls.map(([url]) => String(url))).toContain('/api/v1/stats/retention?period=month'));
  });

  it('shows empty states when no review analytics exist yet', async () => {
    fetchMock.mockImplementation(async (input) => {
      const url = String(input);
      if (url === '/api/v1/stats/overview') return response(fixture(url));
      if (url.startsWith('/api/v1/stats/forecast?')) {
        return response({
          days: 30,
          due: Array.from({ length: 30 }, (_, index) => ({
            date: new Date(Date.UTC(year, 0, index + 1)).toISOString().slice(0, 10),
            cards: 0,
          })),
        });
      }
      if (url.startsWith('/api/v1/stats/retention?')) return response({ period: 'week', desired_retention: 0.9, points: [] });
      if (url.startsWith('/api/v1/stats/heatmap?')) {
        const selectedYear = Number(new URL(url, window.location.origin).searchParams.get('year'));
        return response({ year: selectedYear, days: [], vocabulary_growth: [] });
      }
      return response({ states: [], stability: [], difficulty: [], leeches: [], deck_median_stability: [] });
    });
    renderPage();

    expect(await screen.findByText(t('statsNoRetention'))).toBeTruthy();
    expect(await screen.findByRole('img', { name: t('statsForecastChartLabel') })).toBeTruthy();
    expect(screen.getByText(t('statsForecastTotal').replace('{count}', '0'))).toBeTruthy();
    expect(await screen.findByText(t('statsNoActivity'))).toBeTruthy();
    await waitFor(() => expect(screen.getAllByText(t('statsNoDistribution'))).toHaveLength(3));
    expect(screen.getByText(t('statsNoDeckStability'))).toBeTruthy();
    expect(screen.getByText(t('statsNoHardCards'))).toBeTruthy();
  });
});
