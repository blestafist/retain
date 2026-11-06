import {
  THEME_CACHE_KEY,
  THEME_CACHE_REVISION,
  THEME_CACHE_VERSION,
  DEFAULT_THEME_SETTINGS,
  validateThemeSettings,
  type ThemeSettings,
} from './settings';
import {
  createThemePalettes,
  THEME_ROLE_NAMES,
  type ThemePalettes,
  type ThemeRoleMap,
} from './palette';

export interface ThemeCache {
  version: typeof THEME_CACHE_VERSION;
  revision: typeof THEME_CACHE_REVISION;
  settings: ThemeSettings;
  palettes: ThemePalettes;
}

export interface ThemeStorage {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
}

const HEX_COLOR = /^#[\da-f]{6}$/i;
const CACHE_KEYS = ['version', 'revision', 'settings', 'palettes'];
const SETTINGS_KEYS = ['seed', 'variant', 'contrastLevel', 'mode'];
const PALETTE_KEYS = ['light', 'dark'];

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function hasExactKeys(
  value: unknown,
  keys: readonly string[],
): value is Record<string, unknown> {
  return isRecord(value) &&
    Object.keys(value).length === keys.length &&
    keys.every((key) => Object.prototype.hasOwnProperty.call(value, key));
}

function isRoleMap(value: unknown): value is ThemeRoleMap {
  if (!isRecord(value) || Object.keys(value).length !== THEME_ROLE_NAMES.length) {
    return false;
  }

  return THEME_ROLE_NAMES.every((role) => {
    const color = value[role];
    return typeof color === 'string' && HEX_COLOR.test(color);
  });
}

function safeLocalStorage(): ThemeStorage | null {
  try {
    return typeof window === 'undefined' ? null : window.localStorage;
  } catch {
    return null;
  }
}

export function createThemeCache(settings: ThemeSettings): ThemeCache {
  const validSettings = validateThemeSettings(settings);
  if (validSettings === null) {
    throw new TypeError('Invalid theme settings.');
  }

  return {
    version: THEME_CACHE_VERSION,
    revision: THEME_CACHE_REVISION,
    settings: validSettings,
    palettes: createThemePalettes(validSettings),
  };
}

function parseCompatibleThemeCache(serialized: string | null): (Omit<ThemeCache, 'revision'> & { revision: number }) | null {
  if (serialized === null) {
    return null;
  }

  try {
    const candidate: unknown = JSON.parse(serialized);
    if (
      !hasExactKeys(candidate, CACHE_KEYS) ||
      candidate.version !== THEME_CACHE_VERSION ||
      typeof candidate.revision !== 'number' ||
      !Number.isInteger(candidate.revision) ||
      candidate.revision < 1 || candidate.revision > THEME_CACHE_REVISION
    ) {
      return null;
    }

    const settings = hasExactKeys(candidate.settings, SETTINGS_KEYS)
      ? validateThemeSettings(candidate.settings)
      : null;
    const palettes = candidate.palettes;
    if (
      settings === null ||
      !hasExactKeys(palettes, PALETTE_KEYS) ||
      !isRoleMap(palettes.light) ||
      !isRoleMap(palettes.dark)
    ) {
      return null;
    }

    return {
      version: THEME_CACHE_VERSION,
      revision: candidate.revision,
      settings,
      palettes: {
        light: palettes.light,
        dark: palettes.dark,
      },
    };
  } catch {
    return null;
  }
}

export function parseThemeCache(serialized: string | null): ThemeCache | null {
  const cache = parseCompatibleThemeCache(serialized);
  return cache?.revision === THEME_CACHE_REVISION
    ? { ...cache, revision: THEME_CACHE_REVISION }
    : null;
}

export function readThemeCache(
  storage: ThemeStorage | null = safeLocalStorage(),
): ThemeCache | null {
  try {
    return parseThemeCache(storage?.getItem(THEME_CACHE_KEY) ?? null);
  } catch {
    return null;
  }
}

export function loadThemeCache(
  storage: ThemeStorage | null = safeLocalStorage(),
): ThemeCache {
  try {
    const serialized = storage?.getItem(THEME_CACHE_KEY) ?? null;
    const cache = parseCompatibleThemeCache(serialized);
    if (cache !== null) {
      return cache.revision === THEME_CACHE_REVISION
        ? { ...cache, revision: THEME_CACHE_REVISION }
        : createThemeCache(cache.settings);
    }
  } catch {
    // Invalid or unavailable storage uses the default theme.
  }
  return createThemeCache(DEFAULT_THEME_SETTINGS);
}

export function persistThemeCache(
  cache: ThemeCache,
  storage: ThemeStorage | null = safeLocalStorage(),
): boolean {
  try {
    if (storage === null) {
      return false;
    }

    storage.setItem(THEME_CACHE_KEY, JSON.stringify(cache));
    return true;
  } catch {
    return false;
  }
}
