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
  THEME_PRESETS,
  normalizeSeedColor,
  resolveThemeMode,
  validateThemeSettings,
} from './settings';
import { THEME_ROLE_NAMES } from './palette';

function bootstrapTheme(serialized: string, systemIsDark = false) {
  const html = readFileSync(new URL('../../index.html', import.meta.url), 'utf8');
  const script = html.match(/<script>\s*([\s\S]*?)<\/script>/)?.[1];
  const defaults = html.match(/<script id="theme-default-palette" type="application\/json">(.*?)<\/script>/)?.[1];
  const properties: Record<string, string> = {};
  const root = { dataset: {}, style: { colorScheme: '', setProperty: (key: string, value: string) => { properties[key] = value; } } };
  let themeColor = '';
  expect(script).toBeDefined();
  expect(defaults).toBeDefined();
  runInNewContext(script ?? '', {
    window: { localStorage: { getItem: () => serialized }, matchMedia: () => ({ matches: systemIsDark }) },
    document: {
      documentElement: root,
      querySelector: (selector: string) => selector.includes('cache-version')
        ? { content: String(THEME_CACHE_VERSION) }
        : selector.includes('cache-revision')
          ? { content: String(THEME_CACHE_REVISION) }
          : { setAttribute: (_key: string, value: string) => { themeColor = value; } },
      getElementById: () => ({ textContent: defaults }),
    },
  });
  return { properties, root, themeColor };
}

describe('theme settings validation', () => {
  it('defaults to balanced blue and keeps Swamp available as a custom preset', () => {
    expect(DEFAULT_THEME_SETTINGS).toEqual({
      seed: '#2563EB', variant: 'tonalSpot', contrastLevel: 0, mode: 'system',
    });
    expect(THEME_PRESETS[0]).toEqual({ id: 'blue', name: 'Blue', seed: '#2563EB' });
    expect(THEME_PRESETS).toContainEqual({ id: 'swamp', name: 'Swamp', seed: '#3A4527' });
  });

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
    expect(html).toContain('value.revision <= cacheRevision');
  });

  it.each(['light', 'dark'] as const)('uses revision 1 %s palette before React and migrates it', (mode) => {
    const settings = {
      seed: '#006874', variant: 'expressive', contrastLevel: 0.5, mode,
    } as const;
    const expected = createThemeCache(settings);
    const stale = structuredClone(expected);
    stale.palettes[mode]['rating-again'] = '#123456';
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
    expect(root.style.colorScheme).toBe(mode);
    for (const role of THEME_ROLE_NAMES) {
      expect(properties[`--md-sys-color-${role}`]).toBe(stale.palettes[mode][role]);
    }
    expect(properties['--md-sys-color-rating-again']).toBe('#123456');

    const migrated = loadThemeCache(storage);
    expect(migrated).toEqual(expected);
    expect(persistThemeCache(migrated, storage)).toBe(true);
    expect(parseThemeCache(stored)).toEqual(expected);
  });

  it.each(['light', 'dark', 'system'] as const)('refreshes only an older default pair and preserves %s mode and contrast', (mode) => {
    const settings = { seed: '#3A4527', variant: 'fidelity', contrastLevel: 0.5, mode } as const;
    const stale = JSON.stringify({ ...createThemeCache(settings), revision: 2 });
    const storage: ThemeStorage = { getItem: () => stale, setItem: () => undefined };
    const expected = createThemeCache({ ...settings, seed: '#2563EB', variant: 'tonalSpot' });

    expect(parseThemeCache(stale)).toBeNull();
    expect(loadThemeCache(storage)).toEqual(expected);

    const bootstrap = bootstrapTheme(stale, true);
    const resolvedMode = mode === 'light' ? 'light' : 'dark';
    const defaultPalette = createThemeCache(DEFAULT_THEME_SETTINGS).palettes[resolvedMode];
    expect(bootstrap.root.style.colorScheme).toBe(resolvedMode);
    expect(bootstrap.themeColor).toBe(defaultPalette.surface);
    expect(bootstrap.properties['--md-sys-color-primary']).toBe(defaultPalette.primary);
  });

  it.each([
    { seed: '#3A4527', variant: 'tonalSpot' },
    { seed: '#006874', variant: 'fidelity' },
  ] as const)('preserves older custom seed/style $seed/$variant on first paint and load', (custom) => {
    const settings = { ...DEFAULT_THEME_SETTINGS, ...custom, mode: 'dark', contrastLevel: 1 } as const;
    const expected = createThemeCache(settings);
    const stale = JSON.stringify({ ...expected, revision: 2 });

    expect(loadThemeCache({ getItem: () => stale, setItem: () => undefined })).toEqual(expected);
    const bootstrap = bootstrapTheme(stale);
    expect(bootstrap.root.style.colorScheme).toBe('dark');
    for (const role of THEME_ROLE_NAMES) {
      expect(bootstrap.properties[`--md-sys-color-${role}`]).toBe(expected.palettes.dark[role]);
    }
  });

  it('preserves a newly selected Swamp/Fidelity theme in the current revision', () => {
    const expected = createThemeCache({ ...DEFAULT_THEME_SETTINGS, seed: '#3A4527', variant: 'fidelity', mode: 'dark' });
    const serialized = JSON.stringify(expected);
    expect(loadThemeCache({ getItem: () => serialized, setItem: () => undefined })).toEqual(expected);
    expect(bootstrapTheme(serialized).properties['--md-sys-color-primary']).toBe(expected.palettes.dark.primary);
  });

  it('uses the generated default for an incompatible stale cache', () => {
    const stale = createThemeCache({ ...DEFAULT_THEME_SETTINGS, seed: '#006874', mode: 'dark' });
    const serialized = JSON.stringify({ ...stale, version: 999, revision: 1 });
    const storage: ThemeStorage = { getItem: () => serialized, setItem: () => undefined };
    const html = readFileSync(new URL('../../index.html', import.meta.url), 'utf8');
    const script = html.match(/<script>\s*([\s\S]*?)<\/script>/)?.[1];
    const defaults = html.match(/<script id="theme-default-palette" type="application\/json">(.*?)<\/script>/)?.[1];
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
    expect(root.style.colorScheme).toBe('light');
    expect(properties['--md-sys-color-primary']).toBe(createThemeCache(DEFAULT_THEME_SETTINGS).palettes.light.primary);
    expect(loadThemeCache(storage)).toEqual(createThemeCache(DEFAULT_THEME_SETTINGS));
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
