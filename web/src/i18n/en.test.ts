import { describe, expect, it } from 'vitest';
import { en, t } from './en';

describe('English translations', () => {
  it('returns the string for a typed translation key', () => {
    expect(t('welcomeHeading')).toBe(en.welcomeHeading);
  });
});
