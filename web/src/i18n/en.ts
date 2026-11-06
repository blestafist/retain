export const en = {
  appName: 'Retain',
  showcase: 'UI showcase', showcaseIntro: 'Explore the design system and adjust the live theme.',
  appearance: 'Theme controls', presets: 'Preset seeds', customColor: 'Custom color', hexColor: 'HEX color', invalidHex: 'Enter a valid three or six digit HEX color.',
  schemeStyle: 'Scheme style', contrast: 'Contrast', mode: 'Mode', standard: 'Standard', medium: 'Medium', high: 'High', system: 'System', light: 'Light', dark: 'Dark',
  fidelity: 'Fidelity', tonalSpot: 'Tonal Spot', vibrant: 'Vibrant', expressive: 'Expressive', monochrome: 'Monochrome',
  swamp: 'Swamp', ocean: 'Ocean', terracotta: 'Terracotta', iris: 'Iris', marigold: 'Marigold', berry: 'Berry', glacier: 'Glacier', lichen: 'Lichen',
  contrastWarning: 'Some theme colors do not meet WCAG AA contrast.', raiseContrast: 'Raise contrast',
  preview: 'Preview card', previewTerm: 'Wanderlust', previewDescription: 'A strong desire to travel and explore.', chart: 'Chart series',
  connectedGroup: 'Connected button group', fabMenu: 'FAB menu', wavyProgress: 'Wavy progress', loadingIndicator: 'Loading indicator', floatingToolbar: 'Floating toolbar', splitButton: 'Split button', responsiveNav: 'Responsive navigation', snackbar: 'Snackbar', sheet: 'Bottom sheet / side sheet', shapes: 'Decorative shapes',
  showSnackbar: 'Show snackbar', snackbarMessage: 'Your action was completed.', openSheet: 'Open sheet', sheetContent: 'Suggestions appear here for review.',
  home: 'Home', decks: 'Decks', settings: 'Settings', chartDescription: 'Three example line series: primary, tertiary and secondary.',

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
