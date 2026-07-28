/**
 * Regression test: D3 charts must re-render when the theme toggles.
 *
 * The chart reads its colors from CSS custom properties that switch on [data-theme],
 * so a theme change has to re-run the render effect. Previously the effect read the
 * theme via document.documentElement.getAttribute (not a reactive dependency), so a
 * toggle left stale colors. It now reads the reactive currentTheme() store.
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, unmount } from 'svelte';

vi.mock('../lib/charts.ts', () => ({ renderHeatmap: vi.fn() }));

// @ts-expect-error — Svelte component has no declaration file
import ReadinessHeatmap from './ReadinessHeatmap.svelte';
import { renderHeatmap } from '../lib/charts.ts';
import { toggleTheme } from '../lib/theme.svelte.ts';

const flush = () => new Promise(resolve => setTimeout(resolve, 0));

describe('ReadinessHeatmap — theme reactivity', () => {
  let target: HTMLElement;

  beforeEach(() => {
    target = document.createElement('div');
    document.body.appendChild(target);
    (renderHeatmap as ReturnType<typeof vi.fn>).mockClear();
  });

  afterEach(() => {
    document.body.removeChild(target);
  });

  it('re-renders when the theme toggles', async () => {
    const component = mount(ReadinessHeatmap, {
      target,
      props: { data: { rows: [{ owner: 'team', cells: [] }] } },
    });
    await flush();
    const before = (renderHeatmap as ReturnType<typeof vi.fn>).mock.calls.length;
    expect(before).toBeGreaterThan(0);

    toggleTheme();
    await flush();
    const after = (renderHeatmap as ReturnType<typeof vi.fn>).mock.calls.length;
    expect(after).toBeGreaterThan(before);

    unmount(component);
  });
});
