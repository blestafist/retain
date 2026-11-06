import { normalizeSeedColor } from './settings';

const BLACK = '#000000';
const WHITE = '#FFFFFF';

function linearize(channel: number): number {
  const normalized = channel / 255;
  return normalized <= 0.04045
    ? normalized / 12.92
    : ((normalized + 0.055) / 1.055) ** 2.4;
}

function channels(color: string): [number, number, number] {
  const normalized = normalizeSeedColor(color);
  if (normalized === null) {
    throw new TypeError(`Invalid HEX color: ${color}`);
  }

  return [
    Number.parseInt(normalized.slice(1, 3), 16),
    Number.parseInt(normalized.slice(3, 5), 16),
    Number.parseInt(normalized.slice(5, 7), 16),
  ];
}

export function relativeLuminance(color: string): number {
  const [red, green, blue] = channels(color).map(linearize);
  return 0.2126 * red + 0.7152 * green + 0.0722 * blue;
}

export function contrastRatio(foreground: string, background: string): number {
  const foregroundLuminance = relativeLuminance(foreground);
  const backgroundLuminance = relativeLuminance(background);
  const lighter = Math.max(foregroundLuminance, backgroundLuminance);
  const darker = Math.min(foregroundLuminance, backgroundLuminance);

  return (lighter + 0.05) / (darker + 0.05);
}

export function meetsWcagAA(
  foreground: string,
  background: string,
  minimumRatio = 4.5,
): boolean {
  return contrastRatio(foreground, background) >= minimumRatio;
}

export function contrastingForeground(background: string): string {
  return contrastRatio(BLACK, background) >= contrastRatio(WHITE, background)
    ? BLACK
    : WHITE;
}
