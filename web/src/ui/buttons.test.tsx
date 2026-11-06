import type { ComponentProps } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ConnectedButtonGroup } from './buttons';

const state = vi.hoisted(() => ({ reduced: false, props: [] as Record<string, unknown>[] }));
vi.mock('motion/react', () => ({
  useReducedMotion: () => state.reduced,
  motion: { button: (props: Record<string, unknown>) => { state.props.push(props); return null; } },
}));

type RatingAction = ComponentProps<typeof ConnectedButtonGroup>['actions'][number];
// @ts-expect-error Rating semantics are required even when the label names a rating.
const missingRating: RatingAction = { id: 'again', label: 'again', onSelect() {} };
void missingRating;

describe('ConnectedButtonGroup', () => {
  beforeEach(() => { state.props = []; state.reduced = false; });
  it('always uses explicit semantic rating colors', () => {
    renderToStaticMarkup(<ConnectedButtonGroup label="sessionActions" actions={[
      ...(['again', 'hard', 'good', 'easy'] as const).map((rating) => ({ id: rating, label: rating, rating, onSelect() {} })),
    ]} />);
    for (const [index, rating] of ['again', 'hard', 'good', 'easy'].entries()) {
      expect(state.props[index].className).toContain(`bg-rating-${rating}-container`);
      expect(state.props[index].className).toContain(`text-on-rating-${rating}-container`);
    }
  });
  it('does not animate shape when reduced motion is requested', () => {
    state.reduced = true;
    renderToStaticMarkup(<ConnectedButtonGroup label="sessionActions" selected="good" actions={[{ id: 'good', label: 'good', rating: 'good', onSelect() {} }]} />);
    expect(state.props[0].animate).toBeUndefined();
    expect(state.props[0].whileTap).toBeUndefined();
    expect(state.props[0].style).toEqual({ borderRadius: '1.5rem' });
  });
});
