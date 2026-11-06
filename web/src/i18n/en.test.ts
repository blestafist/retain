import { describe, expect, it } from 'vitest';
import { en, t } from './en';

describe('English translations', () => {
  it('returns the string for a typed translation key', () => {
    expect(t('welcomeHeading')).toBe(en.welcomeHeading);
  });
});

it('resolves all showcase component headings', () => {
  for (const key of ['connectedGroup', 'fabMenu', 'wavyProgress', 'loadingIndicator', 'floatingToolbar', 'splitButton', 'responsiveNav', 'snackbar', 'sheet', 'shapes'] as const) {
    expect(t(key)).toBeTruthy();
  }
});
