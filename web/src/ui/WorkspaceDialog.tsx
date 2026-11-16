import * as Dialog from '@radix-ui/react-dialog';
import { motion, useReducedMotion } from 'motion/react';
import { useRef, type ReactNode } from 'react';
import { t } from '../i18n/en';
import { getMotionTransition } from '../theme/motion';
import './workspace-dialog.css';

/** A focused workspace task; Radix handles modal focus, Escape and restoration. */
export function WorkspaceDialog({ open, onOpenChange, title, children, returnFocusRef }: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  children: ReactNode;
  returnFocusRef?: { readonly current: HTMLElement | null };
}) {
  const opener = useRef<HTMLElement | null>(null);
  const content = useRef<HTMLDivElement>(null);
  const reduced = !!useReducedMotion();
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="workspace-dialog-scrim" />
        <Dialog.Content
          asChild
          ref={content}
          className="workspace-dialog"
          aria-describedby={undefined}
          onOpenAutoFocus={(event) => {
            opener.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
            const field = content.current?.querySelector<HTMLElement>('input:not([disabled]):not([type="hidden"]), textarea:not([disabled]), select:not([disabled])');
            if (field) {
              event.preventDefault();
              field.focus();
            }
          }}
          onCloseAutoFocus={(event) => {
            event.preventDefault();
            const target = returnFocusRef?.current ?? opener.current;
            if (target?.isConnected) target.focus();
          }}
        >
          <motion.div initial={reduced ? { opacity: 0 } : { opacity: 0, scale: 0.98, y: 8 }} animate={{ opacity: 1, scale: 1, y: 0 }} transition={getMotionTransition(reduced ? 'effects' : 'spatial', 'fast', reduced)}>
          <header className="workspace-dialog-header">
            <Dialog.Title>{title}</Dialog.Title>
            <Dialog.Close className="workspace-icon-button" aria-label={t('close')}>
              <svg aria-hidden="true" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round"><path d="m6 6 12 12M6 18 18 6" /></svg>
            </Dialog.Close>
          </header>
          <div className="workspace-dialog-body">{children}</div>
          </motion.div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
