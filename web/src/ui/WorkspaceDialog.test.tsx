// @vitest-environment jsdom
import { useEffect, useRef, useState } from 'react';
import { cleanup, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { t } from '../i18n/en';
import { WorkspaceDialog } from './WorkspaceDialog';

function DialogExample({ onMount = () => {}, explicitReturnFocus = true }: {
  onMount?: () => void;
  explicitReturnFocus?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const opener = useRef<HTMLButtonElement>(null);
  return (
    <>
      <button ref={opener} type="button" onClick={() => setOpen(true)}>{t('addNote')}</button>
      <WorkspaceDialog open={open} onOpenChange={setOpen} title={t('addNote')} returnFocusRef={explicitReturnFocus ? opener : undefined}>
        <DialogFields onMount={onMount} onCancel={() => setOpen(false)} />
      </WorkspaceDialog>
    </>
  );
}

function DialogFields({ onMount, onCancel }: { onMount: () => void; onCancel: () => void }) {
  useEffect(onMount, [onMount]);
  return (
    <>
      <label htmlFor="dialog-term">{t('term')}</label>
      <input id="dialog-term" />
      <button type="button" onClick={onCancel}>{t('cancel')}</button>
    </>
  );
}

afterEach(cleanup);

describe('WorkspaceDialog', () => {
  it('mounts task content only while open and exposes a labelled modal', async () => {
    const user = userEvent.setup();
    const onMount = vi.fn();
    render(<DialogExample onMount={onMount} />);

    expect(onMount).not.toHaveBeenCalled();
    expect(screen.queryByRole('dialog', { hidden: true })).toBeNull();
    expect(screen.queryByLabelText(t('term'))).toBeNull();
    const opener = screen.getByRole('button', { name: t('addNote') });
    await user.click(opener);

    const dialog = screen.getByRole('dialog', { name: t('addNote') });
    expect(within(dialog).getByRole('heading', { name: t('addNote') })).toBeTruthy();
    expect(within(dialog).getByLabelText(t('term'))).toBeTruthy();
    expect(onMount).toHaveBeenCalledOnce();
    expect(opener.closest('[aria-hidden="true"]')).not.toBeNull();
    await user.click(within(dialog).getByRole('button', { name: t('close') }));

    await waitFor(() => expect(screen.queryByRole('dialog', { hidden: true })).toBeNull());
    expect(screen.queryByLabelText(t('term'))).toBeNull();
    await waitFor(() => expect(document.activeElement).toBe(opener));
  });

  it('keeps Tab and Shift+Tab inside the task until Escape restores the opener focus', async () => {
    const user = userEvent.setup();
    render(<DialogExample />);
    const opener = screen.getByRole('button', { name: t('addNote') });
    opener.focus();
    await user.keyboard('{Enter}');

    const dialog = screen.getByRole('dialog', { name: t('addNote') });
    const closeButton = within(dialog).getByRole('button', { name: t('close') });
    const cancelButton = within(dialog).getByRole('button', { name: t('cancel') });
    const term = within(dialog).getByLabelText(t('term'));
    await waitFor(() => expect(document.activeElement).toBe(term));
    await user.tab({ shift: true });
    expect(document.activeElement).toBe(closeButton);
    await user.tab({ shift: true });
    expect(document.activeElement).toBe(cancelButton);
    await user.tab();
    expect(document.activeElement).toBe(closeButton);
    await user.tab();
    expect(document.activeElement).toBe(term);
    await user.keyboard('{Escape}');

    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    await waitFor(() => expect(document.activeElement).toBe(opener));
  });

  it('restores the opener without an explicit focus ref when the parent closes the task', async () => {
    const user = userEvent.setup();
    render(<DialogExample explicitReturnFocus={false} />);
    const opener = screen.getByRole('button', { name: t('addNote') });
    await user.click(opener);
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: t('cancel') }));

    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    await waitFor(() => expect(document.activeElement).toBe(opener));
  });
});
