import type { ReactNode } from 'react';
import type { TranslationKey } from '../i18n/en';

export type Action = { id: string; label: TranslationKey; icon?: ReactNode; onSelect: () => void; disabled?: boolean };
export const focus = 'focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary';
export const target = `min-h-12 min-w-12 ${focus}`;
export const menuItem = `flex min-h-12 cursor-pointer items-center gap-3 rounded-xl px-4 text-on-surface outline-none data-[highlighted]:bg-secondary-container data-[highlighted]:text-on-secondary-container data-[disabled]:opacity-50 ${focus}`;

export function Chevron() {
  return <svg aria-hidden="true" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="m6 9 6 6 6-6" /></svg>;
}
export function Plus() {
  return <svg aria-hidden="true" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M12 5v14M5 12h14" /></svg>;
}
