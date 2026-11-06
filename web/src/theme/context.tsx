import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useLayoutEffect,
  useMemo,
  useState,
  useSyncExternalStore,
  type ReactNode,
} from 'react';
import {
  createThemeCache,
  loadThemeCache,
  persistThemeCache,
  type ThemeCache,
} from './cache';
import { THEME_ROLE_NAMES, type ThemeRoleMap } from './palette';
import {
  resolveThemeMode,
  validateThemeSettings,
  type ResolvedThemeMode,
  type ThemeSettings,
} from './settings';

const SYSTEM_COLOR_SCHEME = '(prefers-color-scheme: dark)';

export interface ThemeContextValue {
  settings: ThemeSettings;
  palettes: ThemeCache['palettes'];
  activePalette: ThemeRoleMap;
  resolvedMode: ResolvedThemeMode;
  setThemeSettings: (settings: ThemeSettings) => void;
}

const ThemeContext = createContext<ThemeContextValue | null>(null);

function subscribeToSystemColorScheme(onChange: () => void): () => void {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') {
    return () => undefined;
  }

  const query = window.matchMedia(SYSTEM_COLOR_SCHEME);
  if (typeof query.addEventListener === 'function') {
    query.addEventListener('change', onChange);
    return () => query.removeEventListener('change', onChange);
  }

  query.addListener(onChange);
  return () => query.removeListener(onChange);
}

function getSystemColorScheme(): boolean {
  return typeof window !== 'undefined' &&
    typeof window.matchMedia === 'function' &&
    window.matchMedia(SYSTEM_COLOR_SCHEME).matches;
}

function applyThemePalette(
  palette: ThemeRoleMap,
  mode: ResolvedThemeMode,
): void {
  const root = document.documentElement;
  root.dataset.themeMode = mode;
  root.style.colorScheme = mode;

  for (const role of THEME_ROLE_NAMES) {
    root.style.setProperty(`--md-sys-color-${role}`, palette[role]);
  }

  document
    .querySelector<HTMLMetaElement>('meta[name="theme-color"]')
    ?.setAttribute('content', palette.surface);
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [cache, setCache] = useState(loadThemeCache);
  const systemIsDark = useSyncExternalStore(
    subscribeToSystemColorScheme,
    getSystemColorScheme,
    () => false,
  );
  const resolvedMode = resolveThemeMode(cache.settings.mode, systemIsDark);

  useLayoutEffect(() => {
    applyThemePalette(cache.palettes[resolvedMode], resolvedMode);
  }, [cache.palettes, resolvedMode]);

  useEffect(() => {
    persistThemeCache(cache);
  }, [cache]);

  const setThemeSettings = useCallback((nextSettings: ThemeSettings) => {
    const validSettings = validateThemeSettings(nextSettings);
    if (validSettings !== null) {
      setCache(createThemeCache(validSettings));
    }
  }, []);

  const value = useMemo<ThemeContextValue>(() => ({
    settings: cache.settings,
    palettes: cache.palettes,
    activePalette: cache.palettes[resolvedMode],
    resolvedMode,
    setThemeSettings,
  }), [cache, resolvedMode, setThemeSettings]);

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>;
}

export function useTheme(): ThemeContextValue {
  const theme = useContext(ThemeContext);
  if (theme === null) {
    throw new Error('useTheme must be used within a ThemeProvider.');
  }

  return theme;
}
