import * as Dialog from '@radix-ui/react-dialog';
import * as Toast from '@radix-ui/react-toast';
import { createContext, useCallback, useContext, useState, type ReactNode } from 'react';
import { t, type TranslationKey } from '../i18n/en';
import { target } from './shared';

type SnackbarMessage = { id: number; message: TranslationKey; action?: { label: TranslationKey; onSelect: () => void } };
type SnackbarContextValue = (message: Omit<SnackbarMessage, 'id'>) => void;
const SnackbarContext = createContext<SnackbarContextValue | null>(null);
let nextId = 0;
export function SnackbarProvider({ children }: { children: ReactNode }) {
  const [messages, setMessages] = useState<SnackbarMessage[]>([]);
  const show = useCallback((message: Omit<SnackbarMessage, 'id'>) => setMessages((old) => [...old, { ...message, id: ++nextId }]), []);
  return <SnackbarContext.Provider value={show}><Toast.Provider swipeDirection="right">{children}{messages.map((message) => <Toast.Root key={message.id} onOpenChange={(open) => { if (!open) setMessages((old) => old.filter((item) => item.id !== message.id)); }} className="flex items-center gap-3 rounded-xl bg-inverse-surface p-3 text-inverse-on-surface shadow-lg"><Toast.Description className="flex-1">{t(message.message)}</Toast.Description>{message.action && <Toast.Action altText={t(message.action.label)} onClick={message.action.onSelect} className={`${target} rounded-full px-3 text-inverse-primary`}>{t(message.action.label)}</Toast.Action>}<Toast.Close aria-label={t('dismiss')} className={`${target} rounded-full px-3`}>{t('dismiss')}</Toast.Close></Toast.Root>)}<Toast.Viewport label={t('notifications')} className="fixed bottom-0 right-0 z-50 flex w-full max-w-md flex-col gap-2 p-4 outline-none" /></Toast.Provider></SnackbarContext.Provider>;
}
export function useSnackbar(): SnackbarContextValue {
  const context = useContext(SnackbarContext);
  if (!context) throw new Error('useSnackbar must be used within SnackbarProvider');
  return context;
}
export function Sheet({ trigger, title, description, children }: { trigger: ReactNode; title: TranslationKey; description: TranslationKey; children: ReactNode }) {
  return <Dialog.Root><Dialog.Trigger asChild>{trigger}</Dialog.Trigger><Dialog.Portal><Dialog.Overlay className="fixed inset-0 z-40 bg-scrim opacity-50" /><Dialog.Content className="fixed inset-x-0 bottom-0 z-50 max-h-[90vh] overflow-auto rounded-t-3xl bg-surface-container p-6 text-on-surface shadow-xl focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary expanded:inset-y-0 expanded:left-auto expanded:right-0 expanded:w-full expanded:max-w-md expanded:max-h-none expanded:rounded-l-3xl expanded:rounded-r-none"><div className="flex items-start justify-between gap-4"><div><Dialog.Title className="text-xl font-medium">{t(title)}</Dialog.Title><Dialog.Description className="mt-2 text-on-surface-variant">{t(description)}</Dialog.Description></div><Dialog.Close aria-label={t('close')} className={`${target} rounded-full px-3`}>{t('close')}</Dialog.Close></div><div className="mt-6">{children}</div></Dialog.Content></Dialog.Portal></Dialog.Root>;
}
