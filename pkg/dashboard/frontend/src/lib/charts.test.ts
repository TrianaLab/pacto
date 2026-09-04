import { describe, it, expect, vi } from 'vitest';
import { renderCategoryStackedBars, renderPriorityQuadrant, renderHeatmap, renderVersionTimeline } from './charts';

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

  it('defines gradients for Soft Depth polish', () => {
    const container = document.createElement('div');
    const data = [{ category: 'test', done: 1, partial: 1, notDone: 1, deferred: 1 }];
    renderCategoryStackedBars(container, data);
    expect(container.querySelector('defs linearGradient')).not.toBeNull();
  });
});

describe('renderPriorityQuadrant', () => {
  it('draws a dot per datum and empty state', () => {
    const container = document.createElement('div');
    renderPriorityQuadrant(container, [{ name: 'a', x: -20, y: 9, status: 'NonCompliant' }, { name: 'b', x: 40, y: 1, status: 'Compliant' }]);
    expect(container.querySelectorAll('svg circle').length).toBeGreaterThanOrEqual(2);
    const c2 = document.createElement('div');
    renderPriorityQuadrant(c2, []);
    expect(c2.textContent).toContain('No readiness data');
  });

  // The divider is the gate, and it has to be ON the plot to be read as one -- even
  // when the whole fleet sits on one side of it.
  it('keeps the threshold divider on the plot when every service clears its gate', () => {
    const c = document.createElement('div');
    renderPriorityQuadrant(c, [{ name: 'a', x: 10, y: 1, status: 'Compliant' }, { name: 'b', x: 40, y: 2, status: 'Compliant' }]);
    expect(c.textContent).toContain('meets its own threshold');
    const dashed = [...c.querySelectorAll('line')].filter((l) => l.getAttribute('stroke-dasharray'));
    const vertical = dashed.find((l) => l.getAttribute('x1') === l.getAttribute('x2'));
    expect(vertical).toBeDefined();
    expect(Number(vertical!.getAttribute('x1'))).toBeGreaterThanOrEqual(0);
  });

  // Dots used to be sized by blast radius, which is already the y position: the same
  // number said twice, once by height and once by area. Read under reduced motion so
  // the assertion sees the resting radius rather than the entrance's starting 0.
  it('draws every dot the same size, because impact is on the axis', () => {
    const mm = vi.spyOn(window, 'matchMedia').mockReturnValue({ matches: true } as MediaQueryList);
    try {
      const c = document.createElement('div');
      renderPriorityQuadrant(c, [{ name: 'a', x: 0, y: 100, status: 'Compliant' }, { name: 'b', x: 0, y: 1, status: 'Compliant' }]);
      const plot = c.querySelector('svg > g');
      const radii = new Set([...plot!.querySelectorAll('circle')].map((el) => el.getAttribute('r')));
      expect(radii).toEqual(new Set(['8']));
    } finally {
      mm.mockRestore();
    }
  });

  // Four of the seven statuses had no key to read them by.
  it('keys every status it plotted, including the ones nobody hand-listed', () => {
    const c = document.createElement('div');
    renderPriorityQuadrant(c, [{ name: 'a', x: 0, y: 1, status: 'Invalid' }, { name: 'b', x: 0, y: 2, status: 'NotEvaluated' }]);
    expect(c.textContent).toContain('Invalid');
    expect(c.textContent).toContain('Not evaluated');
  });
});

describe('renderHeatmap', () => {
  it('draws a cell per owner×category and empty state', () => {
    const c = document.createElement('div');
    renderHeatmap(c, { owners: ['x','y'], categories: ['docs','test'], cells: [
      { owner:'x', category:'docs', score:50, n:2 }, { owner:'x', category:'test', score:100, n:1 },
      { owner:'y', category:'docs', score:0, n:1 },
    ]});
    expect(c.querySelectorAll('svg rect').length).toBeGreaterThanOrEqual(4); // 2×2 grid cells (+ maybe legend)
    const c2 = document.createElement('div'); renderHeatmap(c2, { owners:[], categories:[], cells:[] });
    expect(c2.textContent).toContain('No category data');
  });

  it('cells have a border so the grid reads, and column headers are labelled category icons (no rotated text)', () => {
    const c = document.createElement('div');
    renderHeatmap(c, { owners: ['x'], categories: ['documentation','testing'], cells: [{ owner:'x', category:'documentation', score:50, n:2 }] });
    // Every grid cell carries a stroke so empty/low cells stay visible as a matrix.
    const cell = c.querySelector('svg g rect');
    expect(cell?.getAttribute('stroke')).toBe('var(--c-border)');
    // One labelled icon per category header — full name on aria-label (a11y), not shape-alone.
    const ariaLabels = [...c.querySelectorAll('[role="img"]')].map((s) => s.getAttribute('aria-label'));
    expect(ariaLabels).toEqual(expect.arrayContaining(['documentation', 'testing']));
    // A transparent full-cell hit rect drives the hover tooltip (icons are stroke-only).
    const hitRects = [...c.querySelectorAll('rect')].filter((r) => r.getAttribute('fill') === 'transparent');
    expect(hitRects.length).toBeGreaterThanOrEqual(2);
    // No rotated text labels (the old collision-prone approach is gone).
    const anyRotatedText = [...c.querySelectorAll('text')].some((t) => (t.getAttribute('transform') || '').includes('rotate'));
    expect(anyRotatedText).toBe(false);
  });
});

describe('renderVersionTimeline', () => {
  it('draws a marker per dated version + legend + empty', () => {
    const c = document.createElement('div');
    renderVersionTimeline(c, [
      { version: '1.0.0', at: 1735689600000, classification: 'NON_BREAKING', isCurrent: false },
      { version: '2.0.0', at: 1738368000000, classification: 'BREAKING', isCurrent: true },
    ]);
    expect(c.querySelectorAll('svg circle').length).toBeGreaterThanOrEqual(2);
    const c2 = document.createElement('div');
    renderVersionTimeline(c2, []);
    expect(c2.textContent).toContain('No version history');
  });

  it('handles a single version without collapsing', () => {
    const c = document.createElement('div');
    renderVersionTimeline(c, [{ version: '1.0.0', at: 1735689600000, classification: 'NON_BREAKING', isCurrent: true }]);
    expect(c.querySelectorAll('svg circle').length).toBeGreaterThanOrEqual(1);
  });

  // Every marker's position encodes a date. Without an axis the only way to read one
  // was to hover it, which is no way at all on a touch screen.
  it('labels the time axis it positions its markers on', () => {
    const c = document.createElement('div');
    renderVersionTimeline(c, [
      { version: '1.0.0', at: Date.UTC(2025, 0, 1), classification: 'NON_BREAKING' },
      { version: '2.0.0', at: Date.UTC(2025, 11, 1), classification: 'BREAKING' },
    ]);
    const ticks = [...c.querySelectorAll('svg g .tick text')].map((t) => t.textContent);
    expect(ticks.length).toBeGreaterThan(0);
    expect(ticks.every((t) => t && t.length > 0)).toBe(true);
  });
});
