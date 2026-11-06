import { test } from 'node:test';
import assert from 'node:assert/strict';
import { violations } from './check-colors.mjs';

test('rejects hard-coded colors in JSX, styles and classes', () => {
  for (const fixture of [
    '<path fill="red" />', "<path stroke={'blue'} />", "<path stopColor={'red'} />",
    "<path style={{ stopColor: 'red' }} />", "const style = { stopColor: 'red' }; const x = <path style={style} />;",
    "<div style={{ backgroundColor: 'red' }} />", "<div style={{ borderTop: '1px solid red' }} />",
    "<div style={{ color: '#abc' }} />", "<div style={{ background: 'rgb(1, 2, 3)' }} />",
    "<div style={{ background: 'hsl(0 0% 0%)' }} />", '<div className="hover:bg-red-500 text-white" />',
    '<path className="fill-[#abc]" />', '<div className="text-[color:rgb(1,2,3)]" />',
    "const colors = 'bg-blue-500'; const x = <div className={colors} />;",
  ]) assert.notEqual(violations(fixture).length, 0, fixture);
  for (const fixture of ['a { color: red; }', 'a { border-top: 1px solid red; }', 'a { fill: #abc; }', 'a { color: rgb(1, 2, 3); }', 'a { color: hsl(0 0% 0%); }']) {
    assert.notEqual(violations(fixture, 'fixture.css').length, 0, fixture);
  }
});

test('allows tokens, currentColor, none and transparent', () => {
  for (const fixture of [
    '<path fill="currentColor" stroke="none" />', '<path fill="transparent" />',
    "<div style={{ backgroundColor: 'var(--md-sys-color-primary)' }} />", "const style = { fill: 'currentColor' }; const x = <path style={style} />;",
    '<div className="bg-primary text-on-surface fill-current stroke-current fill-[currentColor] text-[color:var(--md-sys-color-primary)] fill-[var(--md-sys-color-primary)] bg-transparent" />',
    "const message = 'red #abc rgb(1, 2, 3) bg-red-500'; const x = <div title={message}>red</div>;",
  ]) assert.deepEqual(violations(fixture), [], fixture);
  for (const fixture of ['a { color: var(--md-sys-color-primary); }', 'a { color: var(--other-token); fill: currentColor; background: transparent; }']) {
    assert.deepEqual(violations(fixture, 'fixture.css'), [], fixture);
  }
});
