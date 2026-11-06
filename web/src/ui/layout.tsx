import { useState, type KeyboardEvent, type ReactNode } from 'react';
import { t, type TranslationKey } from '../i18n/en';
import { target, type Action } from './shared';

export function FloatingToolbar({ actions, label = 'sessionActions', orientation = 'horizontal' }: { actions: Action[]; label?: TranslationKey; orientation?: 'horizontal' | 'vertical' }) {
  const [focused, setFocused] = useState<string>();
  const enabled = actions.filter((action) => !action.disabled);
  const active = enabled.some((action) => action.id === focused) ? focused : enabled[0]?.id;
  function onKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    const buttons = Array.from(event.currentTarget.querySelectorAll<HTMLButtonElement>('button:not(:disabled)'));
    const index = buttons.indexOf(document.activeElement as HTMLButtonElement);
    if (index < 0 || !buttons.length) return;
    const nextKey = orientation === 'vertical' ? 'ArrowDown' : 'ArrowRight';
    const previousKey = orientation === 'vertical' ? 'ArrowUp' : 'ArrowLeft';
    let next: number;
    if (event.key === 'Home') next = 0;
    else if (event.key === 'End') next = buttons.length - 1;
    else if (event.key === nextKey) next = (index + 1) % buttons.length;
    else if (event.key === previousKey) next = (index - 1 + buttons.length) % buttons.length;
    else return;
    event.preventDefault();
    buttons[next].focus();
  }
  return <div role="toolbar" aria-label={t(label)} aria-orientation={orientation} onKeyDown={onKeyDown} className={`inline-flex flex-wrap gap-1 rounded-full bg-surface-container-high p-1 text-on-surface shadow-lg ${orientation === 'vertical' ? 'flex-col' : ''}`}>{actions.map((action) => <button key={action.id} type="button" onClick={action.onSelect} onFocus={() => setFocused(action.id)} tabIndex={action.id === active ? 0 : -1} disabled={action.disabled} aria-label={t(action.label)} title={t(action.label)} className={`${target} flex items-center justify-center rounded-full px-3 hover:bg-secondary-container disabled:opacity-50`}>{action.icon ?? t(action.label)}</button>)}</div>;
}
export type NavigationItem = { id: string; label: TranslationKey; icon: ReactNode; href: string };
export function Navigation({ items, current, label = 'navigation' }: { items: NavigationItem[]; current: string; label?: TranslationKey }) {
  return <nav aria-label={t(label)} className="flex w-full justify-around bg-surface-container px-2 py-1 expanded:w-24 expanded:flex-col expanded:justify-start expanded:gap-2 expanded:py-4">{items.map((item) => <a key={item.id} href={item.href} aria-current={item.id === current ? 'page' : undefined} className={`${target} flex flex-1 flex-col items-center justify-center gap-1 rounded-2xl px-2 py-1 text-on-surface expanded:flex-none`}><span className={`flex h-8 min-w-16 items-center justify-center transition-[border-radius] motion-reduce:transition-none ${item.id === current ? 'rounded-full bg-secondary-container text-on-secondary-container' : 'rounded-lg'}`}>{item.icon}</span><span className="text-xs">{t(item.label)}</span></a>)}</nav>;
}
