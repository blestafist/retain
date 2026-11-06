export const en = {
  appName: 'Retain',
  skipToMain: 'Skip to main content',
  welcomeHeading: 'A calmer way to learn vocabulary',
  placeholderDescription:
    'Your vocabulary learning space is getting ready. Please check back soon.',
} as const;

export type TranslationKey = keyof typeof en;

export function t(key: TranslationKey): string {
  return en[key];
}
