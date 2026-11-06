import * as DropdownMenu from '@radix-ui/react-dropdown-menu';
import { motion, useReducedMotion } from 'motion/react';
import { t, type TranslationKey } from '../i18n/en';
import { getMotionTransition } from '../theme/motion';
import { Chevron, Plus, menuItem, target, type Action } from './shared';

const button = `${target} inline-flex items-center justify-center gap-2 bg-primary px-5 text-on-primary disabled:opacity-50`;
const popup = 'z-50 min-w-48 rounded-2xl bg-surface-container-high p-2 text-on-surface shadow-lg';

export function ConnectedButtonGroup({ actions, selected, label }: { actions: Action[]; selected?: string; label: TranslationKey }) {
  const reduced = !!useReducedMotion();
  return <div role="group" aria-label={t(label)} className="flex gap-0.5">{actions.map((action) => <motion.button key={action.id} type="button" disabled={action.disabled} aria-pressed={selected === action.id} onClick={action.onSelect} className={`${target} flex flex-1 items-center justify-center gap-2 bg-secondary-container px-3 text-on-secondary-container disabled:opacity-50`} animate={{ borderRadius: selected === action.id ? '1.5rem' : '0.5rem' }} whileTap={reduced ? undefined : { borderRadius: '0.375rem' }} transition={getMotionTransition('spatial', 'fast', reduced)}>{action.icon}{t(action.label)}</motion.button>)}</div>;
}

function MenuItems({ actions }: { actions: Action[] }) {
  return actions.map((action) => <DropdownMenu.Item key={action.id} disabled={action.disabled} onSelect={action.onSelect} className={menuItem}>{action.icon}{t(action.label)}</DropdownMenu.Item>);
}

export function FabMenu({ actions, label = 'openActions' }: { actions: Action[]; label?: TranslationKey }) {
  return <DropdownMenu.Root><DropdownMenu.Trigger aria-label={t(label)} className={`${target} flex h-14 w-14 items-center justify-center rounded-2xl bg-primary-container text-on-primary-container shadow-lg`}><Plus /></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content sideOffset={8} align="end" className={popup}><MenuItems actions={actions} /></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>;
}

export function SplitButton({ action, options, menuLabel = 'chooseMode' }: { action: Action; options: Action[]; menuLabel?: TranslationKey }) {
  return <div className="inline-flex gap-0.5"><button type="button" disabled={action.disabled} onClick={action.onSelect} className={`${button} rounded-l-full rounded-r-md`}>{action.icon}{t(action.label)}</button><DropdownMenu.Root><DropdownMenu.Trigger aria-label={t(menuLabel)} className={`${button} rounded-l-md rounded-r-full px-3`}><Chevron /></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content sideOffset={8} align="end" className={popup}><MenuItems actions={options} /></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root></div>;
}
