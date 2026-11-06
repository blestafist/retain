export const THEME_CACHE_KEY = 'retain.theme.cache.v1';
export const THEME_CACHE_VERSION = 1;

export const THEME_VARIANTS = [
  'fidelity',
  'tonalSpot',
  'vibrant',
  'expressive',
  'monochrome',
] as const;

export type ThemeVariant = (typeof THEME_VARIANTS)[number];
export type ThemeMode = 'light' | 'dark' | 'system';
export type ResolvedThemeMode = Exclude<ThemeMode, 'system'>;
export type ThemeContrastLevel = 0 | 0.5 | 1;

export interface ThemeSettings {
  seed: string;
  variant: ThemeVariant;
  contrastLevel: ThemeContrastLevel;
  mode: ThemeMode;
}

export interface ThemePreset {
  readonly id: string;
  readonly name: string;
  readonly seed: string;
}

export const DEFAULT_THEME_SETTINGS: Readonly<ThemeSettings> = {
  seed: '#3A4527',
  variant: 'fidelity',
  contrastLevel: 0,
  mode: 'system',
};

export const THEME_PRESETS: readonly ThemePreset[] = [
  { id: 'swamp', name: 'Swamp', seed: '#3A4527' },
  { id: 'ocean', name: 'Ocean', seed: '#006874' },
  { id: 'terracotta', name: 'Terracotta', seed: '#8C4A35' },
  { id: 'iris', name: 'Iris', seed: '#5B548A' },
  { id: 'marigold', name: 'Marigold', seed: '#765B00' },
  { id: 'berry', name: 'Berry', seed: '#8D496F' },
  { id: 'glacier', name: 'Glacier', seed: '#426277' },
  { id: 'lichen', name: 'Lichen', seed: '#52634A' },
];

const HEX_COLOR = /^#?(?:[\da-f]{3}|[\da-f]{6})$/i;

export function normalizeSeedColor(value: unknown): string | null {
  if (typeof value !== 'string') {
    return null;
  }

  const trimmed = value.trim();
  if (!HEX_COLOR.test(trimmed)) {
    return null;
  }

  const digits = trimmed.startsWith('#') ? trimmed.slice(1) : trimmed;
  const expanded = digits.length === 3
    ? [...digits].map((digit) => `${digit}${digit}`).join('')
    : digits;

  return `#${expanded.toUpperCase()}`;
}

export function validateThemeSettings(value: unknown): ThemeSettings | null {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    return null;
  }

  const candidate = value as Record<string, unknown>;
  const seed = normalizeSeedColor(candidate.seed);
  const variant = candidate.variant;
  const contrastLevel = candidate.contrastLevel;
  const mode = candidate.mode;

  if (
    seed === null ||
    typeof variant !== 'string' ||
    !THEME_VARIANTS.some((knownVariant) => knownVariant === variant) ||
    (contrastLevel !== 0 && contrastLevel !== 0.5 && contrastLevel !== 1) ||
    (mode !== 'light' && mode !== 'dark' && mode !== 'system')
  ) {
    return null;
  }

  return {
    seed,
    variant: variant as ThemeVariant,
    contrastLevel,
    mode,
  };
}

export function resolveThemeMode(
  mode: ThemeMode,
  systemIsDark: boolean,
): ResolvedThemeMode {
  if (mode === 'system') {
    return systemIsDark ? 'dark' : 'light';
  }

  return mode;
}
