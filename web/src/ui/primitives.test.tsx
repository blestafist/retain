import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { ConnectedButtonGroup, DecorativeShape, Navigation, WavyProgress } from './index';

describe('UI primitives', () => {
  it('exposes clamped progress and an accessible label', () => {
    const html = renderToStaticMarkup(<WavyProgress value={120} />);
    expect(html).toContain('aria-valuenow="100"');
    expect(html).toContain('aria-label="Progress"');
  });
  it('marks selection and navigation current page', () => {
    const group = renderToStaticMarkup(<ConnectedButtonGroup label="sessionActions" selected="good" actions={[{ id: 'good', label: 'good', rating: 'good', onSelect() {} }]} />);
    expect(group).toContain('aria-pressed="true"');
    const nav = renderToStaticMarkup(<Navigation current="study" items={[{ id: 'study', label: 'study', href: '/study', icon: null }]} />);
    expect(nav).toContain('aria-current="page"');
  });
  it('hides decorative shapes from assistive technology', () => {
    expect(renderToStaticMarkup(<DecorativeShape shape="clover" />)).toContain('aria-hidden="true"');
  });
});
