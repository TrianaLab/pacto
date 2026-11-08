/**
 * Contrast of every text and stroke the isometric diagram and backdrop paint, in both
 * themes, computed from tokens.css. axe cannot cover them: it never reads SVG `fill`,
 * and it reports text over the backdrop's ::before as "incomplete", not as a violation.
 */
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

const css = readFileSync(join(process.cwd(), 'src/styles/tokens.css'), 'utf8');

// The first block that opens with `selector {`: the theme definitions, not the media overrides.
function block(selector: string): Record<string, string> {
  const start = css.indexOf(`${selector} {`);
  const body = css.slice(start, css.indexOf('\n}', start));
  return Object.fromEntries([...body.matchAll(/(--[\w-]+):\s*([^;]+);/g)].map((m) => [m[1], m[2].trim()]));
}

type RGBA = [number, number, number, number];
function color(v: string): RGBA {
  const hex = v.match(/^#([0-9a-f]{6})$/i);
  if (hex) return [0, 2, 4].map((i) => parseInt(hex[1].slice(i, i + 2), 16)).concat(1) as RGBA;
  const rgba = v.match(/^rgba?\(([^)]+)\)$/);
  if (rgba) {
    const [r, g, b, a = 1] = rgba[1].split(',').map(Number);
    return [r, g, b, a];
  }
  throw new Error(`not a literal colour: ${v}`);
}
const over = (top: RGBA, under: RGBA): RGBA => [0, 1, 2].map((i) => top[i] * top[3] + under[i] * (1 - top[3])).concat(1) as RGBA;
function luminance([r, g, b]: RGBA): number {
  const lin = (c: number) => ((c /= 255) <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4);
  return 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b);
}
function ratio(a: RGBA, b: RGBA): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

const dark = block(':root');
const THEMES: Record<string, Record<string, string>> = { dark, light: { ...dark, ...block('[data-theme="light"]') } };

describe.each(Object.entries(THEMES))('isometric contrast, %s theme', (_, t) => {
  const c = (name: string) => color(t[name]);

  it('keeps text at 4.5:1 over the backdrop grid and glow at full strength', () => {
    for (const ground of ['--c-bg', '--c-surface']) {
      const worst = over(c('--c-iso-glow'), over(c('--c-iso-grid'), c(ground)));
      for (const text of ['--c-text', '--c-text-2', '--c-text-3']) {
        expect(ratio(c(text), worst), `${text} on ${ground} + backdrop`).toBeGreaterThanOrEqual(4.5);
      }
    }
  });

  it('keeps diagram text at 4.5:1 on the ground, the sheet and the slab face', () => {
    for (const ground of ['--c-bg', '--c-surface', '--c-iso-face']) {
      for (const text of ['--c-text', '--c-text-3', '--c-err', '--c-ok', '--c-warn']) {
        expect(ratio(c(text), c(ground)), `${text} on ${ground}`).toBeGreaterThanOrEqual(4.5);
      }
    }
  });

  it('keeps verdict strokes at 3:1 on the ground', () => {
    for (const stroke of ['--c-err', '--c-ok', '--c-neutral']) {
      expect(ratio(c(stroke), c('--c-bg')), `${stroke} on --c-bg`).toBeGreaterThanOrEqual(3);
    }
  });

  // G6 (SPEC 8): every figure mark -- tone fills' edges, wires, glyphs, the hatch's solid
  // edge -- is at 3:1 against the figure surface, the slab face and the page.
  it('keeps every figure mark at 3:1 on the surface, the slab face and the page', () => {
    for (const ground of ['--c-bg', '--c-surface', '--c-iso-face']) {
      for (const stroke of ['--c-err', '--c-warn', '--c-ok', '--c-accent', '--c-neutral', '--c-text-3']) {
        expect(ratio(c(stroke), c(ground)), `${stroke} on ${ground}`).toBeGreaterThanOrEqual(3);
      }
    }
  });
});
