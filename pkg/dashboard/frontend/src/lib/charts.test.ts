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
});

describe('renderOwnerBars', () => {
  it('renders horizontal bars for each owner', () => {
    const container = document.createElement('div');
    const data = [
      { key: 'team-a', services: 5, compliancePercent: 80 },
      { key: 'team-b', services: 3, compliancePercent: 50 },
    ];

    renderOwnerBars(container, data);

    const svg = container.querySelector('svg');
    expect(svg).not.toBeNull();

    const rects = container.querySelectorAll('rect');
    expect(rects.length).toBe(2); // one bar per owner
  });

  it('limits to top 15 owners', () => {
    const container = document.createElement('div');
    const data = Array.from({ length: 20 }, (_, i) => ({
      key: `owner-${i}`,
      services: i,
      compliancePercent: i * 5,
    }));

    renderOwnerBars(container, data);

    const rects = container.querySelectorAll('rect');
    expect(rects.length).toBe(15); // limited to top 15
  });

  it('renders empty state when no data', () => {
    const container = document.createElement('div');
    renderOwnerBars(container, []);
    expect(container.textContent).toContain('No owner data');
  });

  it('renders labels with percent and service count', () => {
    const container = document.createElement('div');
    const data = [{ key: 'team-x', services: 7, compliancePercent: 100 }];

    renderOwnerBars(container, data);

    const labels = Array.from(container.querySelectorAll('text.bar-label')).map((t) => t.textContent);
    expect(labels.some((l) => l?.includes('100%'))).toBe(true);
    expect(labels.some((l) => l?.includes('7 services'))).toBe(true);
  });
});
