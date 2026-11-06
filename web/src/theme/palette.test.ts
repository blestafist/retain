import { Hct, argbFromHex } from '@material/material-color-utilities';
import { describe, expect, it } from 'vitest';
import {
  contrastRatio,
  meetsWcagAA,
  relativeLuminance,
} from './contrast';
import {
  createThemePalette,
  THEME_ROLE_NAMES,
  type ThemeRoleName,
} from './palette';
import {
  DEFAULT_THEME_SETTINGS,
  THEME_PRESETS,
  THEME_VARIANTS,
  type ThemeSettings,
} from './settings';

const CONTRAST_LEVELS = [0, 0.5, 1] as const;
const MODES = ['light', 'dark'] as const;

const TEXT_CONTRAST_PAIRS = [
  ['on-background', 'background'],
  ['on-surface', 'surface'],
  ['on-surface-variant', 'surface-variant'],
  ['inverse-on-surface', 'inverse-surface'],
  ['on-primary', 'primary'],
  ['on-primary-container', 'primary-container'],
  ['on-primary-fixed', 'primary-fixed'],
  ['on-primary-fixed-variant', 'primary-fixed'],
  ['on-secondary', 'secondary'],
  ['on-secondary-container', 'secondary-container'],
  ['on-secondary-fixed', 'secondary-fixed'],
  ['on-secondary-fixed-variant', 'secondary-fixed'],
  ['on-tertiary', 'tertiary'],
  ['on-tertiary-container', 'tertiary-container'],
  ['on-tertiary-fixed', 'tertiary-fixed'],
  ['on-tertiary-fixed-variant', 'tertiary-fixed'],
  ['on-error', 'error'],
  ['on-error-container', 'error-container'],
] as const satisfies readonly (readonly [ThemeRoleName, ThemeRoleName])[];

const SEMANTIC_CONTRAST_PAIRS = [
  ['on-rating-again', 'rating-again'],
  ['on-rating-again-container', 'rating-again-container'],
  ['on-rating-hard', 'rating-hard'],
  ['on-rating-hard-container', 'rating-hard-container'],
  ['on-rating-good', 'rating-good'],
  ['on-rating-good-container', 'rating-good-container'],
  ['on-rating-easy', 'rating-easy'],
  ['on-rating-easy-container', 'rating-easy-container'],
] as const satisfies readonly (readonly [ThemeRoleName, ThemeRoleName])[];

function settingsFor(
  options: Partial<ThemeSettings> = {},
): ThemeSettings {
  return { ...DEFAULT_THEME_SETTINGS, ...options };
}

describe('MD3 palette generation', () => {
  it.each(THEME_VARIANTS)(
    'generates every role for %s at all contrast levels and modes',
    (variant) => {
      for (const contrastLevel of CONTRAST_LEVELS) {
        for (const mode of MODES) {
          const palette = createThemePalette(
            settingsFor({ variant, contrastLevel }),
            mode,
          );

          expect(Object.keys(palette).sort()).toEqual(
            [...THEME_ROLE_NAMES].sort(),
          );
          expect(Object.values(palette).every((color) => /^#[\da-f]{6}$/i.test(color)))
            .toBe(true);
          expect(palette.primary).not.toBe(palette.surface);
        }
      }
    },
  );

  it('generates curated preset palettes deterministically across styles and modes', () => {
    for (const preset of THEME_PRESETS) {
      for (const variant of THEME_VARIANTS) {
        for (const mode of MODES) {
          const settings = settingsFor({
            seed: preset.seed,
            variant,
            contrastLevel: 0.5,
          });
          const palette = createThemePalette(settings, mode);

          expect(palette).toEqual(createThemePalette(settings, mode));
          expect(palette.primary).toMatch(/^#[\da-f]{6}$/i);
        }
      }
    }
  });

  it('keeps text and semantic foreground/background pairs at WCAG AA', () => {
    for (const preset of THEME_PRESETS) {
      for (const variant of THEME_VARIANTS) {
        for (const contrastLevel of CONTRAST_LEVELS) {
          for (const mode of MODES) {
            const palette = createThemePalette(
              settingsFor({
                seed: preset.seed,
                variant,
                contrastLevel,
              }),
              mode,
            );

            for (const [foreground, background] of TEXT_CONTRAST_PAIRS) {
              expect(
                meetsWcagAA(palette[foreground], palette[background]),
                `${preset.id}/${variant}/${contrastLevel}/${mode}: ${foreground} on ${background} (${contrastRatio(palette[foreground], palette[background]).toFixed(2)}:1)`,
              ).toBe(true);
            }

            for (const [foreground, background] of SEMANTIC_CONTRAST_PAIRS) {
              expect(
                meetsWcagAA(palette[foreground], palette[background]),
                `${preset.id}/${mode}: ${foreground} on ${background}`,
              ).toBe(true);
            }
          }
        }
      }
    }
  });

  it('harmonizes rating hues without making their meaning depend on the seed', () => {
    const hue = (color: string) => Hct.fromInt(argbFromHex(color)).hue;
    const semanticSeeds = [
      ...THEME_PRESETS.map((preset) => preset.seed),
      '#808080',
      '#FF0000',
      '#0000FF',
      '#FFFFFF',
    ];

    for (const seed of semanticSeeds) {
      for (const mode of MODES) {
        const palette = createThemePalette(settingsFor({ seed }), mode);
        expect(hue(palette['rating-again'])).toBeLessThan(40);
        expect(hue(palette['rating-hard'])).toBeGreaterThan(35);
        expect(hue(palette['rating-hard'])).toBeLessThan(75);
        expect(hue(palette['rating-good'])).toBeGreaterThan(80);
        expect(hue(palette['rating-good'])).toBeLessThan(170);
        expect(hue(palette['rating-easy'])).toBeGreaterThan(190);
        expect(hue(palette['rating-easy'])).toBeLessThan(300);
        expect(contrastRatio(palette['on-rating-again'], palette['rating-again']))
          .toBeGreaterThanOrEqual(4.5);
      }
    }
  });
});

describe('WCAG contrast helpers', () => {
  it('calculates known luminance and contrast values', () => {
    expect(relativeLuminance('#000')).toBe(0);
    expect(relativeLuminance('#fff')).toBe(1);
    expect(contrastRatio('#000000', '#FFFFFF')).toBe(21);
    expect(meetsWcagAA('#767676', '#FFFFFF')).toBe(true);
    expect(meetsWcagAA('#777777', '#FFFFFF')).toBe(false);
  });

  it('rejects invalid colors instead of silently reporting contrast', () => {
    expect(() => relativeLuminance('transparent')).toThrow(TypeError);
    expect(() => contrastRatio('#000000', '#12')).toThrow(TypeError);
  });
});
