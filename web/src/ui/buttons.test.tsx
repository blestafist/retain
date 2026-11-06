import { renderToStaticMarkup } from 'react-dom/server';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ConnectedButtonGroup } from './buttons';

const state = vi.hoisted(() => ({ reduced: false, props: [] as Record<string, unknown>[] }));
vi.mock('motion/react', () => ({
  useReducedMotion: () => state.reduced,
  motion: { button: (props: Record<string, unknown>) => { state.props.push(props); return null; } },
}));

describe('ConnectedButtonGroup', () => {
  beforeEach(() => { state.props = []; state.reduced = false; });
  it('uses semantic rating colors without changing the generic fallback', () => {
    renderToStaticMarkup(<ConnectedButtonGroup label="sessionActions" actions={[
      ...(['again', 'hard', 'good', 'easy'] as const).map((rating) => ({ id: rating, label: rating, rating, onSelect() {} })),
      { id: 'other', label: 'study', onSelect() {} },
    ]} />);
    for (const [index, rating] of ['again', 'hard', 'good', 'easy'].entries()) {
      expect(state.props[index].className).toContain(`bg-rating-${rating}-container`);
      expect(state.props[index].className).toContain(`text-on-rating-${rating}-container`);
    }
    expect(state.props[4].className).toContain('bg-secondary-container text-on-secondary-container');
  });
  it('does not animate shape when reduced motion is requested', () => {
    state.reduced = true;
    renderToStaticMarkup(<ConnectedButtonGroup label="sessionActions" selected="good" actions={[{ id: 'good', label: 'good', onSelect() {} }]} />);
    expect(state.props[0].animate).toBeUndefined();
    expect(state.props[0].whileTap).toBeUndefined();
    expect(state.props[0].style).toEqual({ borderRadius: '1.5rem' });
  });
});
