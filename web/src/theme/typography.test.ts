import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const appCss = readFileSync(new URL('../app.css', import.meta.url), 'utf8');
const typographyCss = readFileSync(new URL('./typography.css', import.meta.url), 'utf8');

describe('typography CSS tokens', () => {
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
