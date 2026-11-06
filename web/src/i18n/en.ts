export const en = {
  appName: 'Retain',
  openActions: 'Open actions',
  chooseMode: 'Choose study mode',
  loading: 'Loading',
  progress: 'Progress',
  sessionActions: 'Session actions',
  navigation: 'Main navigation',
  dismiss: 'Dismiss',
  notifications: 'Notifications',
  close: 'Close',
  again: 'Again',
  hard: 'Hard',
  good: 'Good',
  easy: 'Easy',
  study: 'Study',
  addWord: 'Add word',
  importDeck: 'Import deck',
  createDeck: 'Create deck',
  undo: 'Undo',
  speak: 'Speak',
  edit: 'Edit',
  askAssistant: 'Ask assistant',
  suggestions: 'Suggestions',
  suggestionsDescription: 'Review suggestions before accepting them.',
  skipToMain: 'Skip to main content',
  welcomeHeading: 'A calmer way to learn vocabulary',
  placeholderDescription:
    'Your vocabulary learning space is getting ready. Please check back soon.',
} as const;

export type TranslationKey = keyof typeof en;

export function t(key: TranslationKey): string {
  return en[key];
}
