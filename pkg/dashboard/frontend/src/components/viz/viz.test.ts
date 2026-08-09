/**
 * The visualization system's accessibility contract, asserted once here so every
 * product surface that draws a proportion inherits it (requirement 10): the bar is
 * decorative, every value it encodes is printed as text, the figure has a real
 * accessible name, nothing is conveyed by colour alone, and a bucket that leads
 * somewhere is a keyboard-operable link.
 *
 * These are semantic assertions, not layout ones -- they must keep holding through
 * any restyle.
 */
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { mount, unmount } from 'svelte';
// @ts-expect-error — Svelte components have no declaration files
import DistributionBar from './DistributionBar.svelte';

let target: HTMLElement;
let comp: ReturnType<typeof mount> | null = null;
beforeEach(() => { target = document.createElement('div'); document.body.appendChild(target); });
afterEach(() => { if (comp) { unmount(comp); comp = null; } document.body.removeChild(target); });

const segments = [
  { label: 'Compliant', value: 4, tone: 'ok' },
  { label: 'Not compliant', value: 2, tone: 'err', href: '#/fleet/attention?category=non-compliant' },
  { label: 'Unknown', value: 1, tone: 'warn' },
];

describe('DistributionBar', () => {
  it('prints every value as text, so nothing is carried by colour or length alone', () => {
    comp = mount(DistributionBar, { target, props: { title: 'Compliance', segments, total: 7 } });
    const legend = target.querySelector('.dist-legend')?.textContent || '';
    for (const s of segments) {
      expect(legend).toContain(s.label);
      expect(legend).toContain(String(s.value));
    }
    // The percentage is a companion to the exact count, never a replacement, and it
    // always states the denominator it is a percentage OF.
    expect(legend).toContain('(57.1% of 7)');
  });

  it('names the figure from its own caption heading at the requested level', () => {
    comp = mount(DistributionBar, { target, props: { title: 'Compliance', level: 2, segments, total: 7 } });
    const fig = target.querySelector('figure');
    const heading = fig?.querySelector('figcaption h2');
    expect(heading?.textContent).toBe('Compliance');
  });

  it('hides the bar from assistive technology, because the legend already says it', () => {
    comp = mount(DistributionBar, { target, props: { title: 'Compliance', segments, total: 7 } });
    expect(target.querySelector('.dist-bar')?.getAttribute('aria-hidden')).toBe('true');
    // The swatches are the colour, and the colour is never the message.
    for (const sw of Array.from(target.querySelectorAll('.dist-swatch'))) {
      expect(sw.getAttribute('aria-hidden')).toBe('true');
    }
  });

  it('makes a bucket with a destination a real link and leaves the rest as text', () => {
    comp = mount(DistributionBar, { target, props: { title: 'Compliance', segments, total: 7 } });
    const links = Array.from(target.querySelectorAll('.dist-legend a'));
    expect(links).toHaveLength(1);
    expect(links[0].getAttribute('href')).toBe('#/fleet/attention?category=non-compliant');
    expect(links[0].textContent).toContain('Not compliant');
  });

  // The denominator is the backend's population. When the buckets do not account for
  // all of it, the remainder is shown as its own slice: silently rescaling would state
  // a proportion of a population nobody counted.
  it('shows the unaccounted remainder instead of rescaling the proportion', () => {
    comp = mount(DistributionBar, { target, props: { title: 'Compliance', segments, total: 10 } });
    const legend = target.querySelector('.dist-legend')?.textContent || '';
    expect(legend).toContain('Unclassified');
    expect(legend).toContain('3');
    expect(legend).toContain('of 10');
  });

  it('falls back to the population sum when no total is given', () => {
    comp = mount(DistributionBar, { target, props: { title: 'Compliance', segments } });
    expect(target.querySelector('.dist-legend')?.textContent).not.toContain('Unclassified');
    expect(target.querySelector('.dist-legend')?.textContent).toContain('of 7');
  });

  it('says so plainly when there is nothing to show, rather than drawing an empty bar', () => {
    comp = mount(DistributionBar, { target, props: { title: 'Compliance', segments: [], total: 0, emptyLabel: 'No targets yet.' } });
    expect(target.querySelector('.dist-bar')).toBeNull();
    expect(target.querySelector('.dist-empty')?.textContent).toBe('No targets yet.');
  });

  it('drops a zero bucket rather than drawing a slice nobody is in', () => {
    comp = mount(DistributionBar, {
      target,
      props: { title: 'Compliance', segments: [...segments, { label: 'Invalid', value: 0, tone: 'err' }], total: 7 },
    });
    expect(target.querySelector('.dist-legend')?.textContent).not.toContain('Invalid');
    expect(target.querySelectorAll('.dist-seg')).toHaveLength(3);
  });
});

// @ts-expect-error — Svelte components have no declaration files
import HorizontalBars from './HorizontalBars.svelte';

const bars = [
  { label: 'Non-compliant', value: 6, tone: 'err', href: '#/fleet/attention?category=non-compliant' },
  { label: 'Stale', value: 3, tone: 'warn' },
  { label: 'Readiness', value: 0, tone: 'info' },
];

describe('HorizontalBars', () => {
  it('prints every exact value as text and states the length scale', () => {
    comp = mount(HorizontalBars, { target, props: { title: 'By category', items: bars } });
    const text = target.textContent || '';
    expect(text).toContain('Non-compliant');
    expect(text).toContain('6');
    expect(text).toContain('Stale');
    expect(text).toContain('3');
    // Without a stated scale, "full width" is an unanswerable question.
    expect(target.querySelector('.hb-scale')?.textContent).toContain('largest value shown (6)');
  });

  // These are magnitudes, not shares. Offering a percentage would invite reading
  // unrelated counts as parts of a whole.
  it('offers no percentage, because the rows do not sum to a population', () => {
    comp = mount(HorizontalBars, { target, props: { title: 'By category', items: bars } });
    expect(target.textContent).not.toContain('%');
  });

  it('names the figure from its own caption heading at the requested level', () => {
    comp = mount(HorizontalBars, { target, props: { title: 'By category', level: 2, items: bars } });
    expect(target.querySelector('figure figcaption h2')?.textContent).toBe('By category');
  });

  it('hides the bar track from assistive technology, because the row already says it', () => {
    comp = mount(HorizontalBars, { target, props: { title: 'By category', items: bars } });
    for (const t of Array.from(target.querySelectorAll('.hb-track'))) {
      expect(t.getAttribute('aria-hidden')).toBe('true');
    }
  });

  it('makes a row with a destination a real link and leaves the rest as text', () => {
    comp = mount(HorizontalBars, { target, props: { title: 'By category', items: bars } });
    const links = Array.from(target.querySelectorAll('.hb-list a'));
    expect(links).toHaveLength(1);
    expect(links[0].getAttribute('href')).toBe('#/fleet/attention?category=non-compliant');
    expect(links[0].textContent).toContain('Non-compliant');
  });

  it('drops a zero row rather than drawing a bar nobody is in', () => {
    comp = mount(HorizontalBars, { target, props: { title: 'By category', items: bars } });
    expect(target.querySelectorAll('.hb-row')).toHaveLength(2);
    expect(target.textContent).not.toContain('Readiness');
  });

  it('says so plainly when there is nothing to rank, and states no scale for it', () => {
    comp = mount(HorizontalBars, { target, props: { title: 'By category', items: [], emptyLabel: 'No attention items.' } });
    expect(target.querySelector('.hb-list')).toBeNull();
    expect(target.querySelector('.hb-scale')).toBeNull();
    expect(target.querySelector('.hb-empty')?.textContent).toBe('No attention items.');
  });

  // A page-drawn ranking looks exactly like a ranking of everything, so the caller's
  // scope note has to survive to the reader.
  it('renders the scope note verbatim when the rows are not a whole population', () => {
    comp = mount(HorizontalBars, {
      target,
      props: { title: 'By category', items: bars, scopeNote: 'This page only — 25 of 300.', description: 'Ranked.' },
    });
    expect(target.querySelector('.hb-scope')?.textContent).toBe('This page only — 25 of 300.');
    expect(target.querySelector('.hb-desc')?.textContent).toBe('Ranked.');
  });

  it('appends a unit to each value so a bare number is never ambiguous', () => {
    comp = mount(HorizontalBars, { target, props: { title: 'By category', items: bars, unit: 'consumers' } });
    expect(target.querySelector('.hb-value')?.textContent).toBe('6 consumers');
  });
});
