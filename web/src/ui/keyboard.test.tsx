// @vitest-environment jsdom
import { cleanup, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it } from 'vitest';
import { t } from '../i18n/en';
import { FabMenu } from './buttons';
import { Sheet } from './feedback';
import { FloatingToolbar } from './layout';

afterEach(cleanup);
const actions = [
  { id: 'undo', label: 'undo' as const, onSelect() {} },
  { id: 'speak', label: 'speak' as const, onSelect() {}, disabled: true },
  { id: 'edit', label: 'edit' as const, onSelect() {} },
  { id: 'ask', label: 'askAssistant' as const, onSelect() {} },
];

describe('keyboard interactions', () => {
  it('moves a single toolbar tab stop, skipping disabled actions and wrapping', async () => {
    const user = userEvent.setup();
    render(<FloatingToolbar actions={actions} />);
    const undo = screen.getByRole('button', { name: t('undo') });
    const edit = screen.getByRole('button', { name: t('edit') });
    const ask = screen.getByRole('button', { name: t('askAssistant') });
    await user.tab();
    expect(document.activeElement).toBe(undo);
    await user.keyboard('{ArrowRight}');
    expect(document.activeElement).toBe(edit);
    expect(edit.tabIndex).toBe(0);
    expect(undo.tabIndex).toBe(-1);
    await user.keyboard('{End}');
    expect(document.activeElement).toBe(ask);
    await user.keyboard('{ArrowRight}');
    expect(document.activeElement).toBe(undo);
    await user.keyboard('{ArrowLeft}{Home}');
    expect(document.activeElement).toBe(undo);
  });
  it('uses up and down for vertical toolbars', async () => {
    const user = userEvent.setup();
    render(<FloatingToolbar actions={actions} orientation="vertical" />);
    await user.tab();
    await user.keyboard('{ArrowRight}');
    expect(document.activeElement).toBe(screen.getByRole('button', { name: t('undo') }));
    await user.keyboard('{ArrowDown}');
    expect(document.activeElement).toBe(screen.getByRole('button', { name: t('edit') }));
    await user.keyboard('{ArrowUp}');
    expect(document.activeElement).toBe(screen.getByRole('button', { name: t('undo') }));
  });
  it('opens and closes a dropdown with the keyboard', async () => {
    const user = userEvent.setup();
    render(<FabMenu actions={actions} />);
    await user.tab();
    await user.keyboard('{Enter}');
    expect(await screen.findByRole('menu')).toBeTruthy();
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('menu')).toBeNull();
    expect(document.activeElement).toBe(screen.getByRole('button', { name: t('openActions') }));
  });
  it('opens and closes a dialog with the keyboard', async () => {
    const user = userEvent.setup();
    render(<Sheet trigger={<button>{t('suggestions')}</button>} title="suggestions" description="suggestionsDescription">{t('study')}</Sheet>);
    await user.tab();
    await user.keyboard('{Enter}');
    expect(await screen.findByRole('dialog')).toBeTruthy();
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(document.activeElement).toBe(screen.getByRole('button', { name: t('suggestions') }));
  });
});
