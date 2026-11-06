import {
  Blend,
  Hct,
  SchemeExpressive,
  SchemeFidelity,
  SchemeMonochrome,
  SchemeTonalSpot,
  SchemeVibrant,
  TonalPalette,
  argbFromHex,
  hexFromArgb,
  type DynamicColor,
  type DynamicScheme,
  type MaterialDynamicColors,
} from '@material/material-color-utilities';
import { contrastingForeground } from './contrast';
import {
  normalizeSeedColor,
  type ResolvedThemeMode,
  type ThemeSettings,
} from './settings';

export const THEME_ROLE_NAMES = [
  'background',
  'on-background',
  'surface',
  'on-surface',
  'surface-variant',
  'on-surface-variant',
  'surface-dim',
  'surface-bright',
  'surface-container-lowest',
  'surface-container-low',
  'surface-container',
  'surface-container-high',
  'surface-container-highest',
  'inverse-surface',
  'inverse-on-surface',
  'surface-tint',
  'shadow',
  'scrim',
  'outline',
  'outline-variant',
  'primary',
  'on-primary',
  'primary-container',
  'on-primary-container',
  'inverse-primary',
  'primary-fixed',
  'primary-fixed-dim',
  'on-primary-fixed',
  'on-primary-fixed-variant',
  'secondary',
  'on-secondary',
  'secondary-container',
  'on-secondary-container',
  'secondary-fixed',
  'secondary-fixed-dim',
  'on-secondary-fixed',
  'on-secondary-fixed-variant',
  'tertiary',
  'on-tertiary',
  'tertiary-container',
  'on-tertiary-container',
  'tertiary-fixed',
  'tertiary-fixed-dim',
  'on-tertiary-fixed',
  'on-tertiary-fixed-variant',
  'error',
  'on-error',
  'error-container',
  'on-error-container',
  'rating-again',
  'on-rating-again',
  'rating-again-container',
  'on-rating-again-container',
  'rating-hard',
  'on-rating-hard',
  'rating-hard-container',
  'on-rating-hard-container',
  'rating-good',
  'on-rating-good',
  'rating-good-container',
  'on-rating-good-container',
  'rating-easy',
  'on-rating-easy',
  'rating-easy-container',
  'on-rating-easy-container',
] as const;

export type ThemeRoleName = (typeof THEME_ROLE_NAMES)[number];
export type ThemeRoleMap = Record<ThemeRoleName, string>;
export type ThemePalettes = Record<ResolvedThemeMode, ThemeRoleMap>;

type DynamicRole = readonly [
  name: string,
  getColor: (colors: MaterialDynamicColors) => DynamicColor,
];

const DYNAMIC_ROLES: readonly DynamicRole[] = [
  ['background', (colors) => colors.background()],
  ['onBackground', (colors) => colors.onBackground()],
  ['surface', (colors) => colors.surface()],
  ['onSurface', (colors) => colors.onSurface()],
  ['surfaceVariant', (colors) => colors.surfaceVariant()],
  ['onSurfaceVariant', (colors) => colors.onSurfaceVariant()],
  ['surfaceDim', (colors) => colors.surfaceDim()],
  ['surfaceBright', (colors) => colors.surfaceBright()],
  ['surfaceContainerLowest', (colors) => colors.surfaceContainerLowest()],
  ['surfaceContainerLow', (colors) => colors.surfaceContainerLow()],
  ['surfaceContainer', (colors) => colors.surfaceContainer()],
  ['surfaceContainerHigh', (colors) => colors.surfaceContainerHigh()],
  ['surfaceContainerHighest', (colors) => colors.surfaceContainerHighest()],
  ['inverseSurface', (colors) => colors.inverseSurface()],
  ['inverseOnSurface', (colors) => colors.inverseOnSurface()],
  ['surfaceTint', (colors) => colors.surfaceTint()],
  ['shadow', (colors) => colors.shadow()],
  ['scrim', (colors) => colors.scrim()],
  ['outline', (colors) => colors.outline()],
  ['outlineVariant', (colors) => colors.outlineVariant()],
  ['primary', (colors) => colors.primary()],
  ['onPrimary', (colors) => colors.onPrimary()],
  ['primaryContainer', (colors) => colors.primaryContainer()],
  ['onPrimaryContainer', (colors) => colors.onPrimaryContainer()],
  ['inversePrimary', (colors) => colors.inversePrimary()],
  ['primaryFixed', (colors) => colors.primaryFixed()],
  ['primaryFixedDim', (colors) => colors.primaryFixedDim()],
  ['onPrimaryFixed', (colors) => colors.onPrimaryFixed()],
  ['onPrimaryFixedVariant', (colors) => colors.onPrimaryFixedVariant()],
  ['secondary', (colors) => colors.secondary()],
  ['onSecondary', (colors) => colors.onSecondary()],
  ['secondaryContainer', (colors) => colors.secondaryContainer()],
  ['onSecondaryContainer', (colors) => colors.onSecondaryContainer()],
  ['secondaryFixed', (colors) => colors.secondaryFixed()],
  ['secondaryFixedDim', (colors) => colors.secondaryFixedDim()],
  ['onSecondaryFixed', (colors) => colors.onSecondaryFixed()],
  ['onSecondaryFixedVariant', (colors) => colors.onSecondaryFixedVariant()],
  ['tertiary', (colors) => colors.tertiary()],
  ['onTertiary', (colors) => colors.onTertiary()],
  ['tertiaryContainer', (colors) => colors.tertiaryContainer()],
  ['onTertiaryContainer', (colors) => colors.onTertiaryContainer()],
  ['tertiaryFixed', (colors) => colors.tertiaryFixed()],
  ['tertiaryFixedDim', (colors) => colors.tertiaryFixedDim()],
  ['onTertiaryFixed', (colors) => colors.onTertiaryFixed()],
  ['onTertiaryFixedVariant', (colors) => colors.onTertiaryFixedVariant()],
  ['error', (colors) => colors.error()],
  ['onError', (colors) => colors.onError()],
  ['errorContainer', (colors) => colors.errorContainer()],
  ['onErrorContainer', (colors) => colors.onErrorContainer()],
];

const RATING_SEEDS = {
  hard: '#B35A00',
  good: '#386A20',
  easy: '#005AC1',
} as const;

function createDynamicScheme(
  settings: ThemeSettings,
  mode: ResolvedThemeMode,
): DynamicScheme {
  const normalizedSeed = normalizeSeedColor(settings.seed);
  if (normalizedSeed === null) {
    throw new TypeError(`Invalid theme seed: ${settings.seed}`);
  }

  const sourceColor = Hct.fromInt(argbFromHex(normalizedSeed));
  const isDark = mode === 'dark';

  switch (settings.variant) {
    case 'fidelity':
      return new SchemeFidelity(sourceColor, isDark, settings.contrastLevel);
    case 'tonalSpot':
      return new SchemeTonalSpot(sourceColor, isDark, settings.contrastLevel);
    case 'vibrant':
      return new SchemeVibrant(sourceColor, isDark, settings.contrastLevel);
    case 'expressive':
      return new SchemeExpressive(sourceColor, isDark, settings.contrastLevel);
    case 'monochrome':
      return new SchemeMonochrome(sourceColor, isDark, settings.contrastLevel);
  }
}

function toCssHex(argb: number): string {
  return hexFromArgb(argb).toUpperCase();
}

function kebabCase(value: string): string {
  return value.replace(/[A-Z]/g, (letter) => `-${letter.toLowerCase()}`);
}

function addRatingRoles(
  roles: Partial<ThemeRoleMap>,
  settings: ThemeSettings,
  mode: ResolvedThemeMode,
): void {
  const seed = argbFromHex(settings.seed);
  const isDark = mode === 'dark';

  for (const [rating, fixedSeed] of Object.entries(RATING_SEEDS)) {
    const harmonized = Hct.fromInt(Blend.harmonize(argbFromHex(fixedSeed), seed));
    const ratingPalette = TonalPalette.fromHueAndChroma(
      harmonized.hue,
      harmonized.chroma,
    );
    const color = toCssHex(ratingPalette.tone(isDark ? 80 : 40));
    const container = toCssHex(ratingPalette.tone(isDark ? 30 : 90));

    roles[`rating-${rating}` as ThemeRoleName] = color;
    roles[`on-rating-${rating}` as ThemeRoleName] = contrastingForeground(color);
    roles[`rating-${rating}-container` as ThemeRoleName] = container;
    roles[`on-rating-${rating}-container` as ThemeRoleName] =
      contrastingForeground(container);
  }
}

export function createThemePalette(
  settings: ThemeSettings,
  mode: ResolvedThemeMode,
): ThemeRoleMap {
  const scheme = createDynamicScheme(settings, mode);
  const roles: Partial<ThemeRoleMap> = {};

  for (const [name, getColor] of DYNAMIC_ROLES) {
    roles[kebabCase(name) as ThemeRoleName] = toCssHex(
      scheme.getArgb(getColor(scheme.colors)),
    );
  }

  roles['rating-again'] = roles.error;
  roles['on-rating-again'] = roles['on-error'];
  roles['rating-again-container'] = roles['error-container'];
  roles['on-rating-again-container'] = roles['on-error-container'];
  addRatingRoles(roles, settings, mode);

  return roles as ThemeRoleMap;
}

export function createThemePalettes(settings: ThemeSettings): ThemePalettes {
  return {
    light: createThemePalette(settings, 'light'),
    dark: createThemePalette(settings, 'dark'),
  };
}
