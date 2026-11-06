import { useEffect, useState, type CSSProperties } from 'react';
import { t, type TranslationKey } from '../i18n/en';
import { useTheme } from '../theme/context';
import { contrastRatio } from '../theme/contrast';
import { THEME_ROLE_NAMES, type ThemeRoleMap } from '../theme/palette';
import { normalizeSeedColor, THEME_PRESETS, THEME_VARIANTS, type ThemeSettings, type ThemeVariant, type ThemeMode, type ThemeContrastLevel } from '../theme/settings';
import { Button, ConnectedButtonGroup, DecorativeShape, FabMenu, FloatingToolbar, LoadingIndicator, Navigation, Sheet, SnackbarProvider, SplitButton, useSnackbar, WavyProgress } from '../ui';

const field = 'min-w-0 min-h-12 rounded-xl border border-outline bg-surface-container-low px-3 text-on-surface focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary';
const section = 'min-w-0 rounded-3xl bg-surface-container-low p-5';
const heading = 'mb-3 text-lg font-medium';
const pairs = [['on-surface', 'surface'], ['on-primary', 'primary'], ['on-primary-container', 'primary-container'], ['on-secondary-container', 'secondary-container'], ['on-tertiary-container', 'tertiary-container'], ['on-error-container', 'error-container']] as const;
export function hasLowContrast(palettes: Record<'light' | 'dark', ThemeRoleMap>) {
  return Object.values(palettes).some((palette) => pairs.some(([foreground, background]) => contrastRatio(palette[foreground], palette[background]) < 4.5));
}
function Controls() {
  const { settings, setThemeSettings, palettes } = useTheme();
  const [hex, setHex] = useState(settings.seed);
  useEffect(() => { setHex(settings.seed); setInvalid(false); }, [settings.seed]);
  const update = (patch: Partial<ThemeSettings>) => setThemeSettings({ ...settings, ...patch });
  const [invalid, setInvalid] = useState(false);
  const commitHex = () => {
    const seed = normalizeSeedColor(hex);
    if (!seed) { setInvalid(true); return; }
    setInvalid(false);
    setHex(seed);
    update({ seed });
  };
  return <section className={section} aria-labelledby="theme-controls"><h2 id="theme-controls" className={heading}>{t('appearance')}</h2>
    <fieldset><legend className="mb-2 font-medium">{t('presets')}</legend><div className="flex flex-wrap gap-2">{THEME_PRESETS.map((preset) => <button key={preset.id} type="button" onClick={() => { setHex(preset.seed); setInvalid(false); update({ seed: preset.seed }); }} aria-pressed={settings.seed === preset.seed} className={`${field} aria-pressed:bg-secondary-container aria-pressed:text-on-secondary-container`}>{t(preset.id as TranslationKey)}</button>)}</div></fieldset>
    <div className="mt-4 grid gap-4 medium:grid-cols-2 expanded:grid-cols-4">
      <label className="grid gap-1">{t('customColor')}<input className={`${field} w-full cursor-pointer`} type="color" value={settings.seed} onChange={(event) => { setHex(event.target.value); setInvalid(false); update({ seed: event.target.value }); }} /></label>
      <label className="grid gap-1">{t('hexColor')}<input className={field} value={hex} aria-invalid={invalid} aria-describedby={invalid ? 'invalid-hex' : undefined} onChange={(event) => { setHex(event.target.value); setInvalid(false); }} onBlur={commitHex} onKeyDown={(event) => { if (event.key === 'Enter') commitHex(); }} /></label>
      <label className="grid gap-1">{t('schemeStyle')}<select className={field} value={settings.variant} onChange={(event) => update({ variant: event.target.value as ThemeVariant })}>{THEME_VARIANTS.map((variant) => <option key={variant} value={variant}>{t(variant)}</option>)}</select></label>
      <label className="grid gap-1">{t('contrast')}<select className={field} value={settings.contrastLevel} onChange={(event) => update({ contrastLevel: Number(event.target.value) as ThemeContrastLevel })}>{([['standard', 0], ['medium', 0.5], ['high', 1]] as const).map(([key, value]) => <option key={key} value={value}>{t(key)}</option>)}</select></label>
      <label className="grid gap-1">{t('mode')}<select className={field} value={settings.mode} onChange={(event) => update({ mode: event.target.value as ThemeMode })}>{(['light', 'dark', 'system'] as const).map((mode) => <option key={mode} value={mode}>{t(mode)}</option>)}</select></label>
    </div>
    {invalid && <p id="invalid-hex" role="alert" className="mt-2 text-error">{t('invalidHex')}</p>}
    {hasLowContrast(palettes) && <div role="alert" className="mt-4 flex flex-wrap items-center gap-3 rounded-xl bg-error-container p-4 text-on-error-container"><p>{t('contrastWarning')}</p><Button variant="tonal" onClick={() => update({ contrastLevel: settings.contrastLevel === 0 ? 0.5 : 1 })} disabled={settings.contrastLevel === 1}>{t('raiseContrast')}</Button></div>}
  </section>;
}
function PreviewContent({ mode, portalContainer }: { portalContainer: HTMLElement | null; mode: 'light' | 'dark' }) {
  const [current, setCurrent] = useState('home');
  const [rating, setRating] = useState('good');
  const show = useSnackbar();
  const notify = () => show({ message: 'snackbarMessage' });
  const ratings = (['again', 'hard', 'good', 'easy'] as const).map((id) => ({ id, rating: id, label: id, onSelect: () => setRating(id) }));
  const actions = (['addWord', 'importDeck', 'createDeck'] as const).map((id) => ({ id, label: id, onSelect: notify }));
  const toolbar = (['undo', 'speak', 'edit', 'askAssistant'] as const).map((id) => ({ id, label: id, onSelect: notify }));
  return <><h2 className="mb-5 text-2xl font-semibold">{t(mode)}</h2><div className="grid min-w-0 gap-5">
    <section id={`${mode}-home`} className={section}><h3 className={heading}>{t('preview')}</h3><p className="text-3xl font-semibold">{t('previewTerm')}</p><p className="mt-2 text-on-surface-variant">{t('previewDescription')}</p><h4 className="mt-5 mb-2 font-medium">{t('connectedGroup')}</h4><ConnectedButtonGroup actions={ratings} selected={rating} label="connectedGroup" /></section>
    <section id={`${mode}-decks`} className={section}><h3 className={heading}>{t('chart')}</h3><svg role="img" aria-label={t('chartDescription')} viewBox="0 0 300 100" className="h-28 w-full" preserveAspectRatio="none"><path d="M0 80 Q70 80 120 50 T300 12" fill="none" stroke="var(--md-sys-color-primary)" strokeWidth="3" /><path d="M0 90 Q70 55 130 60 T300 30" fill="none" stroke="var(--md-sys-color-tertiary)" strokeWidth="3" /><path d="M0 60 Q80 85 150 45 T300 50" fill="none" stroke="var(--md-sys-color-secondary)" strokeWidth="3" /></svg></section>
    <section className={section}><h3 className={heading}>{t('wavyProgress')}</h3><WavyProgress value={65} /><h3 className={`${heading} mt-4`}>{t('loadingIndicator')}</h3><LoadingIndicator /></section>
    <section className={section}><h3 className={heading}>{t('fabMenu')}</h3><FabMenu actions={actions} portalContainer={portalContainer} /><h3 className={`${heading} mt-4`}>{t('splitButton')}</h3><SplitButton portalContainer={portalContainer} action={{ id: 'study', label: 'study', onSelect: notify }} options={actions} /></section>
    <section className={section}><h3 className={heading}>{t('floatingToolbar')}</h3><FloatingToolbar actions={toolbar} /></section>
    <section id={`${mode}-settings`} className={section}><h3 className={heading}>{t('responsiveNav')}</h3><Navigation current={current} onNavigate={setCurrent} items={(['home', 'decks', 'settings'] as const).map((id) => ({ id, label: id, href: `#${mode}-${id}`, icon: <DecorativeShape shape="clover" className="h-5 w-5" /> }))} /></section>
    <section className={section}><h3 className={heading}>{t('snackbar')}</h3><Button onClick={notify}>{t('showSnackbar')}</Button><h3 className={`${heading} mt-4`}>{t('sheet')}</h3><Sheet portalContainer={portalContainer} trigger={<Button variant="tonal">{t('openSheet')}</Button>} title="suggestions" description="suggestionsDescription"><p>{t('sheetContent')}</p></Sheet></section>
    <section className={section}><h3 className={heading}>{t('shapes')}</h3><div className="flex gap-4 text-tertiary">{(['cookie', 'clover', 'sunny'] as const).map((shape) => <DecorativeShape key={shape} shape={shape} className="h-12 w-12" />)}</div></section>
  </div></>;
}
function Preview({ mode, palette }: { mode: 'light' | 'dark'; palette: ThemeRoleMap }) {
  const [container, setContainer] = useState<HTMLElement | null>(null);
  const style = Object.fromEntries(THEME_ROLE_NAMES.map((role) => [`--md-sys-color-${role}`, palette[role]])) as CSSProperties;
  return <article ref={setContainer} data-theme-preview={mode} style={{ ...style, colorScheme: mode }} className="min-w-0 rounded-3xl bg-surface p-4 text-on-surface medium:p-6"><SnackbarProvider><PreviewContent mode={mode} portalContainer={container} /></SnackbarProvider></article>;
}
export default function Showcase() {
  const { palettes } = useTheme();
  return <main className="mx-auto max-w-screen-2xl p-4 medium:p-8"><header className="mb-6"><p className="text-sm font-medium text-primary">{t('appName')}</p><h1 className="text-3xl font-semibold">{t('showcase')}</h1><p className="mt-2 text-on-surface-variant">{t('showcaseIntro')}</p></header><Controls /><div className="mt-6 grid gap-4 expanded:grid-cols-2"><Preview mode="light" palette={palettes.light} /><Preview mode="dark" palette={palettes.dark} /></div></main>;
}
