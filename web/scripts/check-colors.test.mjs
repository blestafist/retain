import { test } from 'node:test';
import assert from 'node:assert/strict';
import { violations } from './check-colors.mjs';

test('rejects hard-coded colors in JSX, styles and classes', () => {
  for (const fixture of [
    '<path fill="red" />', "<path stroke={'blue'} />", "style={{ backgroundColor: 'red' }}",
    "style={{ color: 'red' }}", 'color: red;', '#abc', 'rgb(1, 2, 3)', 'hsl(0 0% 0%)',
    'className="hover:bg-red-500 text-white"', 'fill-[#abc]',
  ]) assert.notEqual(violations(fixture).length, 0, fixture);
});

test('allows token colors and currentColor', () => {
  for (const fixture of [
    '<path fill="currentColor" stroke="none" />', "style={{ backgroundColor: 'var(--md-sys-color-primary)' }}",
    'color: var(--md-sys-color-primary);', 'color: var(--other-token);', 'bg-primary text-on-surface fill-[var(--md-sys-color-primary)]',
  ]) assert.deepEqual(violations(fixture), [], fixture);
});
