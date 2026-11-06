import { readFileSync } from 'node:fs';
import { runInNewContext } from 'node:vm';
import { describe, expect, it } from 'vitest';
import {
  createThemeCache,
  loadThemeCache,
  parseThemeCache,
  persistThemeCache,
  readThemeCache,
  type ThemeStorage,
} from './cache';
import {
  DEFAULT_THEME_SETTINGS,
  THEME_CACHE_KEY,
  THEME_CACHE_REVISION,
  THEME_CACHE_VERSION,
  normalizeSeedColor,
  resolveThemeMode,
  validateThemeSettings,
} from './settings';
import { THEME_ROLE_NAMES } from './palette';

describe('theme settings validation', () => {
  it.each([
    ['#abc', '#AABBCC'],
    ['ABCDEF', '#ABCDEF'],
    ['  #3a4527  ', '#3A4527'],
  ])('normalizes valid HEX seed %s', (input, expected) => {
    expect(normalizeSeedColor(input)).toBe(expected);
  });

  it.each(['', '#12', '#12345', '#12345678', 'rgb(0, 0, 0)', '##fff']) (
    'rejects invalid HEX seed %s',
    (seed) => {
      expect(normalizeSeedColor(seed)).toBeNull();
    },
  );

  it('normalizes and validates all typed setting fields', () => {
    expect(validateThemeSettings({
      seed: '#abc',
      variant: 'expressive',
      contrastLevel: 0.5,
      mode: 'system',
    })).toEqual({
      seed: '#AABBCC',
      variant: 'expressive',
      contrastLevel: 0.5,
      mode: 'system',
    });

    expect(validateThemeSettings(DEFAULT_THEME_SETTINGS)).toEqual(
      DEFAULT_THEME_SETTINGS,
    );
    expect(validateThemeSettings(null)).toBeNull();
    expect(validateThemeSettings({ ...DEFAULT_THEME_SETTINGS, variant: 'teal' }))
      .toBeNull();
    expect(validateThemeSettings({ ...DEFAULT_THEME_SETTINGS, contrastLevel: 2 }))
      .toBeNull();
    expect(validateThemeSettings({ ...DEFAULT_THEME_SETTINGS, mode: 'auto' }))
      .toBeNull();
  });
});

describe('theme cache and fallback', () => {
  it('persists settings and both mode-specific palettes under a versioned key', () => {
    const cache = createThemeCache({
      ...DEFAULT_THEME_SETTINGS,
      seed: '#006874',
      mode: 'dark',
    });
    const values = new Map<string, string>();
    const storage: ThemeStorage = {
      getItem: (key) => values.get(key) ?? null,
      setItem: (key, value) => values.set(key, value),
    };

    expect(cache.version).toBe(THEME_CACHE_VERSION);
    expect(cache.revision).toBe(THEME_CACHE_REVISION);
    expect(persistThemeCache(cache, storage)).toBe(true);
    expect(values.has(THEME_CACHE_KEY)).toBe(true);
    expect(readThemeCache(storage)).toEqual(cache);
    expect(cache.palettes.light.surface).not.toBe(cache.palettes.dark.surface);
  });

  it('rejects malformed, stale, incomplete, and unsafe cache data', () => {
    const cache = createThemeCache(DEFAULT_THEME_SETTINGS);
    const serialized = JSON.stringify(cache);
    const parsed = JSON.parse(serialized) as Record<string, unknown>;
    const palettes = parsed.palettes as Record<string, Record<string, string>>;

    expect(parseThemeCache('{broken')).toBeNull();
    expect(parseThemeCache(null)).toBeNull();
    expect(parseThemeCache(JSON.stringify({ ...parsed, version: 0 }))).toBeNull();
    expect(parseThemeCache(JSON.stringify({ ...parsed, version: 1 }))).toBeNull();
    expect(parseThemeCache(JSON.stringify({ ...parsed, revision: 0 }))).toBeNull();

    const incomplete = structuredClone(parsed);
    delete (incomplete.palettes as Record<string, Record<string, string>>).light.primary;
    expect(parseThemeCache(JSON.stringify(incomplete))).toBeNull();

    const unsafeColor = structuredClone(parsed);
    (unsafeColor.palettes as Record<string, Record<string, string>>).dark.primary =
      'url(javascript:alert(1))';
    expect(parseThemeCache(JSON.stringify(unsafeColor))).toBeNull();

    const invalidSettings = structuredClone(parsed);
    (invalidSettings.settings as Record<string, unknown>).seed = 'not-a-color';
    expect(parseThemeCache(JSON.stringify(invalidSettings))).toBeNull();

    const unknownSetting = structuredClone(parsed);
    (unknownSetting.settings as Record<string, unknown>).future = true;
    expect(parseThemeCache(JSON.stringify(unknownSetting))).toBeNull();

    const unknownEnvelopeField = { ...parsed, future: true };
    expect(parseThemeCache(JSON.stringify(unknownEnvelopeField))).toBeNull();
    expect(palettes.light.primary).toMatch(/^#[\da-f]{6}$/i);
  });

  it('keeps inline bootstrap cache validation synchronized with the React loader', () => {
    const html = readFileSync(new URL('../../index.html', import.meta.url), 'utf8');
    const version = html.match(
      /<meta name="retain-theme-cache-version" content="(\d+)" \/>/,
    )?.[1];
    const revision = html.match(
      /<meta name="retain-theme-cache-revision" content="(\d+)" \/>/,
    )?.[1];
    const roles = html.match(/const roles = (\[[^\n]*\]);/)?.[1];

    expect(version).toBe(String(THEME_CACHE_VERSION));
    expect(revision).toBe(String(THEME_CACHE_REVISION));
    expect(roles).toBeDefined();
    expect(JSON.parse(roles ?? 'null')).toEqual(THEME_ROLE_NAMES);
    expect(html).toContain(`const cacheKey = '${THEME_CACHE_KEY}'`);
    expect(html).toContain('value.version === cacheVersion');
    expect(html).toContain('value.revision === cacheRevision');
  });

  it('rejects revision 1 palettes in bootstrap and rebuilds them with saved settings', () => {
    const settings = {
      seed: '#006874', variant: 'expressive', contrastLevel: 0.5, mode: 'dark',
    } as const;
    const expected = createThemeCache(settings);
    const stale = structuredClone(expected);
    stale.palettes.dark['rating-again'] = '#123456';
    const serialized = JSON.stringify({ ...stale, revision: 1 });
    let stored = serialized;
    const storage: ThemeStorage = {
      getItem: () => stored,
      setItem: (_key, value) => { stored = value; },
    };
    expect(parseThemeCache(serialized)).toBeNull();

    const html = readFileSync(new URL('../../index.html', import.meta.url), 'utf8');
    const script = html.match(/<script>\s*([\s\S]*?)<\/script>/)?.[1];
    const defaults = html.match(/<script id="theme-default-palette" type="application\/json">(.*?)<\/script>/)?.[1];
    expect(script).toBeDefined();
    expect(defaults).toBeDefined();
    const properties: Record<string, string> = {};
    const root = { dataset: {}, style: { colorScheme: '', setProperty: (key: string, value: string) => { properties[key] = value; } } };
    runInNewContext(script ?? '', {
      window: { localStorage: storage, matchMedia: () => ({ matches: false }) },
      document: {
        documentElement: root,
        querySelector: (selector: string) => selector.includes('cache-version')
          ? { content: String(THEME_CACHE_VERSION) }
          : selector.includes('cache-revision')
            ? { content: String(THEME_CACHE_REVISION) }
            : { setAttribute: () => undefined },
        getElementById: () => ({ textContent: defaults }),
      },
    });
    expect(properties['--md-sys-color-rating-again']).toBe(
      createThemeCache(DEFAULT_THEME_SETTINGS).palettes.light['rating-again'],
    );
    expect(properties['--md-sys-color-rating-again']).not.toBe('#123456');

    const migrated = loadThemeCache(storage);
    expect(migrated).toEqual(expected);
    expect(persistThemeCache(migrated, storage)).toBe(true);
    expect(parseThemeCache(stored)).toEqual(expected);
  });

  it('falls back to the generated default settings if storage is missing or broken', () => {
    expect(loadThemeCache(null).settings).toEqual(DEFAULT_THEME_SETTINGS);
    expect(readThemeCache({
      getItem() {
        throw new Error('Storage is blocked');
      },
      setItem() {
        throw new Error('Storage is blocked');
      },
    })).toBeNull();
    expect(persistThemeCache(createThemeCache(DEFAULT_THEME_SETTINGS), {
      getItem: () => null,
      setItem() {
        throw new Error('Storage is full');
      },
    })).toBe(false);
  });
});

describe('system color scheme', () => {
  it('resolves system mode on each current system preference', () => {
    expect(resolveThemeMode('system', false)).toBe('light');
    expect(resolveThemeMode('system', true)).toBe('dark');
    expect(resolveThemeMode('light', true)).toBe('light');
    expect(resolveThemeMode('dark', false)).toBe('dark');

    const cache = createThemeCache(DEFAULT_THEME_SETTINGS);
    expect(cache.palettes.light.surface).not.toBe(cache.palettes.dark.surface);
  });
});
