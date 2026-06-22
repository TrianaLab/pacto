import { describe, it, expect } from 'vitest';
import { renderCategoryStackedBars, renderReadinessDonut, renderOwnerBars } from './charts';

describe('renderCategoryStackedBars', () => {
  it('renders stacked bars for each category', () => {
    const container = document.createElement('div');
    const data = [
      { category: 'security', done: 5, partial: 2, notDone: 1, deferred: 0 },
      { category: 'docs', done: 3, partial: 1, notDone: 2, deferred: 1 },
    ];

    renderCategoryStackedBars(container, data);

    const svg = container.querySelector('svg');
    expect(svg).not.toBeNull();

    const layers = container.querySelectorAll('g.layer');
    expect(layers.length).toBe(4); // done, partial, notDone, deferred

    const rects = container.querySelectorAll('g.layer rect');
    expect(rects.length).toBeGreaterThan(0); // At least some segments
  });

  it('renders empty state when no data', () => {
    const container = document.createElement('div');
    renderCategoryStackedBars(container, []);
    expect(container.textContent).toContain('No category data');
  });

  it('renders legend with correct labels', () => {
    const container = document.createElement('div');
    const data = [{ category: 'test', done: 1, partial: 1, notDone: 1, deferred: 1 }];
    renderCategoryStackedBars(container, data);

    const legendTexts = Array.from(container.querySelectorAll('text')).map((t) => t.textContent);
    expect(legendTexts).toContain('Done');
    expect(legendTexts).toContain('Partial');
    expect(legendTexts).toContain('Not done');
    expect(legendTexts).toContain('Deferred');
  });

  it('mutes axis chrome (no domain line, no tick marks)', () => {
    const container = document.createElement('div');
    const data = [{ category: 'test', done: 1, partial: 1, notDone: 1, deferred: 1 }];
    renderCategoryStackedBars(container, data);

    expect(container.querySelectorAll('.domain').length).toBe(0);
    expect(container.querySelectorAll('.tick line').length).toBe(0);
  });

  it('applies theme typography to axis/label text', () => {
    const container = document.createElement('div');
    const data = [{ category: 'test', done: 1, partial: 1, notDone: 1, deferred: 1 }];
    renderCategoryStackedBars(container, data);

    const svg = container.querySelector('svg')!;
    expect(svg.style.fontFamily).toBe('var(--font-sans)');
    const texts = Array.from(container.querySelectorAll('text'));
    expect(texts.length).toBeGreaterThan(0);
    // Every text element carries a theme fill token (never a raw d3 default).
    for (const t of texts) {
      expect(t.style.fill.startsWith('var(--c-text')).toBe(true);
    }
  });
});

describe('renderReadinessDonut', () => {
  it('renders donut chart with arcs for non-zero buckets', () => {
    const container = document.createElement('div');
    const data = { ready: 10, partial: 5, notReady: 2, notConfigured: 3 };

    renderReadinessDonut(container, data);

    const svg = container.querySelector('svg');
    expect(svg).not.toBeNull();

    const paths = container.querySelectorAll('path');
    expect(paths.length).toBe(4); // ready, partial, notReady, notConfigured
  });

  it('filters out zero-value buckets', () => {
    const container = document.createElement('div');
    const data = { ready: 10, partial: 0, notReady: 0, notConfigured: 5 };

    renderReadinessDonut(container, data);

    const paths = container.querySelectorAll('path');
    expect(paths.length).toBe(2); // only ready and notConfigured
  });

  it('renders empty state when total is zero', () => {
    const container = document.createElement('div');
    const data = { ready: 0, partial: 0, notReady: 0, notConfigured: 0 };

    renderReadinessDonut(container, data);
    expect(container.textContent).toContain('No readiness data');
  });

  it('renders center label with total count', () => {
    const container = document.createElement('div');
    const data = { ready: 10, partial: 5, notReady: 2, notConfigured: 3 };

    renderReadinessDonut(container, data);

    const texts = Array.from(container.querySelectorAll('text')).map((t) => t.textContent);
    expect(texts).toContain('20'); // 10+5+2+3
    expect(texts).toContain('services');
  });

  it('applies theme typography to all text (no raw d3 defaults)', () => {
    const container = document.createElement('div');
    const data = { ready: 10, partial: 5, notReady: 2, notConfigured: 3 };

    renderReadinessDonut(container, data);

    const svg = container.querySelector('svg')!;
    expect(svg.style.fontFamily).toBe('var(--font-sans)');
    const texts = Array.from(container.querySelectorAll('text'));
    for (const t of texts) {
      expect(t.style.fill.startsWith('var(--c-text')).toBe(true);
      // No hard-coded px/rem font sizes — use theme tokens.
      expect(t.style.fontSize.startsWith('var(--text-')).toBe(true);
    }
  });
});

describe('renderOwnerBars', () => {
  it('renders stacked readiness bars for each owner', () => {
    const container = document.createElement('div');
    const data = [
      { key: 'team-a', services: 5, ready: 3, partial: 1, notReady: 1, notConfigured: 0 },
      { key: 'team-b', services: 3, ready: 1, partial: 1, notReady: 0, notConfigured: 1 },
    ];

    renderOwnerBars(container, data);

    const svg = container.querySelector('svg');
    expect(svg).not.toBeNull();

    const layers = container.querySelectorAll('g.layer');
    expect(layers.length).toBe(4); // ready, partial, notReady, notConfigured
  });

  it('uses a responsive viewBox so the legend is never clipped', () => {
    const container = document.createElement('div');
    const data = [{ key: 'team-a', services: 4, ready: 2, partial: 1, notReady: 1, notConfigured: 0 }];

    renderOwnerBars(container, data);

    const svg = container.querySelector('svg')!;
    expect(svg.getAttribute('width')).toBe('100%');
    expect(svg.getAttribute('viewBox')).toBeTruthy();
    expect(svg.getAttribute('preserveAspectRatio')).toBe('xMidYMid meet');
  });

  it('colors segments with the readiness palette', () => {
    const container = document.createElement('div');
    // Provide CSS custom props so getComputedStyle resolves real colors.
    container.style.setProperty('--c-ok', '#34d399');
    container.style.setProperty('--c-warn', '#fbbf24');
    container.style.setProperty('--c-err', '#f87171');
    container.style.setProperty('--c-text-3', '#64748b');
    document.body.appendChild(container);
    const data = [{ key: 'team-a', services: 4, ready: 1, partial: 1, notReady: 1, notConfigured: 1 }];

    renderOwnerBars(container, data);

    const fills = Array.from(container.querySelectorAll('g.layer')).map((g) => g.getAttribute('fill'));
    expect(fills).toEqual(['#34d399', '#fbbf24', '#f87171', '#64748b']);
    document.body.removeChild(container);
  });

  it('limits to top 15 owners', () => {
    const container = document.createElement('div');
    const data = Array.from({ length: 20 }, (_, i) => ({
      key: `owner-${i}`,
      services: i + 1,
      ready: i,
      partial: 0,
      notReady: 1,
      notConfigured: 0,
    }));

    renderOwnerBars(container, data);

    const layers = container.querySelectorAll('g.layer');
    const rects = container.querySelectorAll('g.layer rect');
    expect(layers.length).toBe(4); // 4 readiness segments
    // Each of the top 15 owners should have up to 4 segments (some may be zero-width)
    expect(rects.length).toBeGreaterThan(0);
  });

  it('renders empty state when no data', () => {
    const container = document.createElement('div');
    renderOwnerBars(container, []);
    expect(container.textContent).toContain('No owner data');
  });

  it('renders labels with total service count', () => {
    const container = document.createElement('div');
    const data = [{ key: 'team-x', services: 7, ready: 5, partial: 1, notReady: 1, notConfigured: 0 }];

    renderOwnerBars(container, data);

    const labels = Array.from(container.querySelectorAll('text.bar-label')).map((t) => t.textContent);
    expect(labels.some((l) => l === '7')).toBe(true);
  });

  it('renders legend with readiness labels', () => {
    const container = document.createElement('div');
    const data = [{ key: 'team-y', services: 4, ready: 2, partial: 1, notReady: 1, notConfigured: 0 }];

    renderOwnerBars(container, data);

    const legendTexts = Array.from(container.querySelectorAll('text')).map((t) => t.textContent);
    expect(legendTexts).toContain('Ready');
    expect(legendTexts).toContain('Partial');
    expect(legendTexts).toContain('Not Ready');
    expect(legendTexts).toContain('Not configured');
  });

  it('widens the left margin so a long owner label fits without clipping', () => {
    const container = document.createElement('div');
    // "platform-foundations" is the longest demo owner key (20 chars).
    const longKey = 'platform-foundations';
    const data = [{ key: longKey, services: 4, ready: 2, partial: 1, notReady: 1, notConfigured: 0 }];

    renderOwnerBars(container, data);

    // The first <g> (bars group) is translated by margin.left; it must exceed
    // the estimated label width so the leading char is never cut off.
    const barsGroup = container.querySelector('svg > g')!;
    const transform = barsGroup.getAttribute('transform') || '';
    const m = transform.match(/translate\(([\d.]+),/);
    expect(m).not.toBeNull();
    const marginLeft = parseFloat(m![1]);
    // 20 chars * 7px + 16px padding = 156px, well above the old fixed 120.
    expect(marginLeft).toBeGreaterThanOrEqual(longKey.length * 7);

    // The viewBox width must include that wider margin.
    const viewBox = container.querySelector('svg')!.getAttribute('viewBox') || '';
    const vbWidth = parseFloat(viewBox.split(' ')[2]);
    expect(vbWidth).toBeGreaterThan(marginLeft);

    // The label itself is present in full.
    const labels = Array.from(container.querySelectorAll('text')).map((t) => t.textContent);
    expect(labels).toContain(longKey);
  });

  it('keeps a 120px floor for short owner labels', () => {
    const container = document.createElement('div');
    const data = [{ key: 'a', services: 4, ready: 2, partial: 1, notReady: 1, notConfigured: 0 }];

    renderOwnerBars(container, data);

    const barsGroup = container.querySelector('svg > g')!;
    const m = (barsGroup.getAttribute('transform') || '').match(/translate\(([\d.]+),/);
    expect(parseFloat(m![1])).toBe(120);
  });
});
