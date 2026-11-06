import type { ReactNode } from 'react';
import { t, type TranslationKey } from '../i18n/en';
import { target, type Action } from './shared';

export function FloatingToolbar({ actions, label = 'sessionActions' }: { actions: Action[]; label?: TranslationKey }) {
  return <div role="toolbar" aria-label={t(label)} className="inline-flex flex-wrap gap-1 rounded-full bg-surface-container-high p-1 text-on-surface shadow-lg">{actions.map((action) => <button key={action.id} type="button" onClick={action.onSelect} disabled={action.disabled} aria-label={t(action.label)} title={t(action.label)} className={`${target} flex items-center justify-center rounded-full px-3 hover:bg-secondary-container disabled:opacity-50`}>{action.icon ?? t(action.label)}</button>)}</div>;
}
export type NavigationItem = { id: string; label: TranslationKey; icon: ReactNode; href: string };
export function Navigation({ items, current, label = 'navigation' }: { items: NavigationItem[]; current: string; label?: TranslationKey }) {
  return <nav aria-label={t(label)} className="flex w-full justify-around bg-surface-container px-2 py-1 expanded:w-24 expanded:flex-col expanded:justify-start expanded:gap-2 expanded:py-4">{items.map((item) => <a key={item.id} href={item.href} aria-current={item.id === current ? 'page' : undefined} className={`${target} flex flex-1 flex-col items-center justify-center gap-1 rounded-2xl px-2 py-1 text-on-surface expanded:flex-none`}><span className={`flex h-8 min-w-16 items-center justify-center transition-[border-radius] motion-reduce:transition-none ${item.id === current ? 'rounded-full bg-secondary-container text-on-secondary-container' : 'rounded-lg'}`}>{item.icon}</span><span className="text-xs">{t(item.label)}</span></a>)}</nav>;
}
