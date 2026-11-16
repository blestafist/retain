import { readFileSync, statSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const appCss = readFileSync(new URL('../app.css', import.meta.url), 'utf8');
const typographyCss = readFileSync(new URL('./typography.css', import.meta.url), 'utf8');
const tailwindConfig = readFileSync(new URL('../../tailwind.config.js', import.meta.url), 'utf8');

describe('typography CSS tokens', () => {
  it('uses Google Sans Flex for brand, body, controls, and the Tailwind preflight family', () => {
    for (const role of ['brand', 'plain']) {
      expect(typographyCss).toContain(`--md-ref-typeface-${role}: 'Google Sans Flex Variable', 'Roboto Flex Variable', sans-serif;`);
    }
    expect(tailwindConfig).toMatch(/sans:\s*\[\s*['"]var\(--md-ref-typeface-plain\)['"]\s*\]/);
  });

  it('ships only Latin optical-size/weight and a Cyrillic fallback, within a 210 kB budget', () => {
    const fonts = [...typographyCss.matchAll(/url\('([^']+)'\)/g)].map((match) => match[1]);
    expect(fonts).toEqual([
      '@fontsource-variable/google-sans-flex/files/google-sans-flex-latin-ext-opsz-normal.woff2',
      '@fontsource-variable/google-sans-flex/files/google-sans-flex-latin-opsz-normal.woff2',
      '@fontsource-variable/roboto-flex/files/roboto-flex-cyrillic-wght-normal.woff2',
    ]);
    expect(typographyCss).not.toMatch(/@import/);
    const totalBytes = fonts.reduce((total, font) => total + statSync(new URL(`../../node_modules/${font}`, import.meta.url)).size, 0);
    expect(totalBytes).toBeLessThan(210_000);
    expect(typographyCss).toContain('U+0400-045F');
    expect(typographyCss).toContain('U+0100-02BA');
    expect(typographyCss.match(/font-display: swap;/g)).toHaveLength(3);
    expect(typographyCss).toContain('font-optical-sizing: auto;');
  });

  it('defines every referenced MD3 typescale token', () => {
    const referencedTokens = [...appCss.matchAll(/var\((--md-sys-typescale-[\w-]+)/g)]
      .map((match) => match[1]);
    const definedTokens = new Set(
      [...typographyCss.matchAll(/(--md-sys-typescale-[\w-]+)\s*:/g)]
        .map((match) => match[1]),
    );

    expect(referencedTokens.length).toBeGreaterThan(0);
    expect([...new Set(referencedTokens)].filter((token) => !definedTokens.has(token)))
      .toEqual([]);
  });
});
