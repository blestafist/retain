import process from "node:process";
import console from "node:console";
import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import assert from 'node:assert/strict';

// Check application and component sources, excluding theme infrastructure and tests.
export function violations(text) {
  const patterns = [
    /#[\da-f]{3,8}\b/gi,
    /\b(?:color|background(?:-color)?|border-color|outline-color|fill|stroke)\s*:\s*(?!var\(--md-sys-color-|none\b|currentColor\b|inherit\b)[a-z]+\b/gi,
    /\b(?:rgba?|hsla?|hwb|lab|lch|oklab|oklch|color)\s*\(/gi,
    /(?:^|[\s"'`:{])(?:[\w-]+:)*(?:bg|text|border|outline|ring|fill|stroke|decoration|shadow|from|via|to|accent|caret|divide|placeholder)-(?:white|black|transparent|current|inherit|slate|gray|grey|zinc|neutral|stone|red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose)(?:[-/\d\w]*)/gm,
    /(?:bg|text|border|outline|ring|fill|stroke)-\[(?!var\(--md-sys-color-)[^\]]+\]/g,
  ];
  return patterns.flatMap((pattern) => [...text.matchAll(pattern)].map((match) => match[0].trim()));
}

assert.ok(violations('bg-white text-red-500 #fff rgb(0,0,0)').length >= 4);
assert.equal(violations('bg-primary text-on-surface fill-[var(--md-sys-color-primary)]').length, 0);

function check(dir) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    if (entry.name === 'theme' || entry.name === 'i18n' || entry.name.includes('.test.')) continue;
    const path = join(dir, entry.name);
    if (entry.isDirectory()) check(path);
    else if (/\.(css|tsx?|jsx?)$/.test(entry.name)) {
      const found = violations(readFileSync(path, 'utf8'));
      if (found.length) { console.error(`${path}: forbidden colors: ${found.join(', ')}`); process.exitCode = 1; }
    }
  }
}
check('src');
