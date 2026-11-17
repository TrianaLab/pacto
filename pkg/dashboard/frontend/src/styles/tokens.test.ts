import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

// AC2 (#597): every chart colour is selected for its own surface, so the dark and light
// theme blocks each declare it with a different value. Only the FIRST `:root {` and
// `[data-theme="light"] {` blocks count: the later `:root, [data-theme="light"]` blocks
// are shared overrides (responsive, reduced motion) and hold no colour.
const css = readFileSync(join(process.cwd(), 'src/styles/tokens.css'), 'utf8');

function block(opener: string): Map<string, string> {
  const start = css.indexOf(`\n${opener} {`);
  expect(start, `${opener} block`).toBeGreaterThanOrEqual(0);
  const body = css.slice(start, css.indexOf('\n}', start));
  return new Map([...body.matchAll(/(--[a-zA-Z0-9-]+)\s*:\s*([^;]+);/g)].map((m) => [m[1], m[2].trim()]));
}

const dark = block(':root');
const light = block('[data-theme="light"]');
const CHART = ['--chart-1', '--chart-2', '--chart-hm-1', '--chart-hm-2', '--chart-hm-3', '--chart-hm-4', '--chart-grid', '--chart-axis', '--chart-surface'];

describe('chart tokens', () => {
  it('declares every chart role in the dark block', () => {
    for (const t of CHART) expect(dark.has(t), t).toBe(true);
  });

  it('selects each chart colour per theme, never one value for both', () => {
    for (const [t, v] of dark) {
      if (!t.startsWith('--chart-') || v.startsWith('var(')) continue;
      expect(light.get(t), `${t} in the light block`).toBeDefined();
      expect(light.get(t), `${t} differs between themes`).not.toBe(v);
    }
  });

  it('keeps the chart surface a reference to the product surface', () => {
    expect(dark.get('--chart-surface')).toBe('var(--c-surface)');
  });

  it('gives unverified a texture of its own, distinct from the unknown hatch', () => {
    expect(dark.get('--c-dots')).toMatch(/radial-gradient/);
    expect(dark.get('--c-dots')).not.toBe(dark.get('--c-hatch'));
  });
});
