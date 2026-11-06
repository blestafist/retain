import { URL } from "node:url";
import { readFileSync } from 'node:fs';

// The generated default contains the complete set of dynamic and semantic roles.
const roles = [...readFileSync(new URL('./src/theme/generated-default.css', import.meta.url), 'utf8').matchAll(/--md-sys-color-([\w-]+):/g)]
  .map((match) => match[1]);
const colors = Object.fromEntries(roles.map((role) => [role, `var(--md-sys-color-${role})`]));

/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    screens: { medium: '600px', expanded: '840px' },
    colors,
  },
  plugins: [],
};
