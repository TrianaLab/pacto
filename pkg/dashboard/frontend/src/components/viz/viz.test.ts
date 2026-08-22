/**
 * The visualization system's accessibility contract, asserted once here so every
 * product surface that draws a proportion inherits it: the bar is
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

  // THE counterexample: the buckets classify MORE than the population being
  // classified. Widening the denominator to the bucket sum makes the contradiction
  // vanish — no remainder, every slice a clean share, a bar full to the edge — and
  // presents impossible data as a complete, healthy distribution.
  it('refuses to hide an over-count by widening its own denominator', () => {
    const over = [
      { label: 'One declared owner', value: 6, tone: 'ok' },
      { label: 'Revisions name different owners', value: 3, tone: 'err' },
      { label: 'No declared owner', value: 1, tone: 'warn' },
    ];
    comp = mount(DistributionBar, { target, props: { title: 'Declared ownership', segments: over, total: 8 } });

    // The authoritative total stays the denominator, so the percentages exceed 100
    // and cannot be mistaken for a valid distribution.
    const legend = target.querySelector('.dist-legend')?.textContent || '';
    expect(legend).toContain('of 8');
    expect(legend).not.toContain('of 10');
    expect(legend).toContain('(75% of 8)');
    // No fake completeness: nothing invents an Unclassified remainder to fill a gap
    // that does not exist, and nothing reads as 100%.
    expect(legend).not.toContain('Unclassified');

    // The contradiction is stated in words, so it does not depend on noticing a
    // colour, a border or the arithmetic.
    const warn = target.querySelector('[data-testid="dist-inconsistent"]');
    expect(warn).not.toBeNull();
    const text = warn?.textContent || '';
    expect(text).toContain('8');   // the authoritative population, still visible
    expect(text).toContain('10');  // what the buckets actually account for
    expect(text).toContain('2');   // by how much they over-count
    // Announced, not merely drawn.
    expect(warn?.getAttribute('role')).toBe('status');
  });

  it('says nothing about an over-count when the buckets exactly fill the total', () => {
    comp = mount(DistributionBar, { target, props: { title: 'Compliance', segments, total: 7 } });
    expect(target.querySelector('[data-testid="dist-inconsistent"]')).toBeNull();
  });

  it('says nothing about an over-count when the buckets fall short of the total', () => {
    comp = mount(DistributionBar, { target, props: { title: 'Compliance', segments, total: 10 } });
    expect(target.querySelector('[data-testid="dist-inconsistent"]')).toBeNull();
    expect(target.querySelector('.dist-legend')?.textContent).toContain('Unclassified');
  });

  // A population of zero with buckets in it is the same contradiction at the edge:
  // nothing exists, and yet seven things have been classified. It is the one case
  // where the denominator cannot carry a percentage, and "0% of 0" is not the
  // degenerate version of a share — it is a measurement of a quantity that does not
  // exist, and it reads as the reassuring one.
  it('shows the counts without inventing a share of a population of zero', () => {
    comp = mount(DistributionBar, { target, props: { title: 'Compliance', segments, total: 0 } });

    const warn = target.querySelector('[data-testid="dist-inconsistent"]');
    expect(warn).not.toBeNull();
    expect(warn?.getAttribute('role')).toBe('status');
    const text = warn?.textContent || '';
    expect(text).toContain('7');            // what the buckets account for
    expect(text).toContain('population of 0');  // the authoritative denominator, unmoved
    expect(text).toContain('no population to take a share of');

    const legend = target.querySelector('.dist-legend')?.textContent || '';
    // Every contradicting count stays visible — the buckets are not suppressed for
    // disagreeing with the total.
    for (const s of segments) {
      expect(legend).toContain(s.label);
      expect(legend).toContain(String(s.value));
    }
    // No fabricated percentage, in either direction: not a plausible zero, and not a
    // valid-looking distribution rescaled onto the bucket sum.
    expect(legend).not.toContain('% of 0');
    expect(legend).not.toContain('0%');
    expect(legend).not.toContain('100%');
    expect(legend).not.toContain('of 7');
    expect(legend).toContain('(share unavailable)');
    // And no Unclassified remainder invented to make zero look accounted for.
    expect(legend).not.toContain('Unclassified');
    // The bar is marked inconsistent too, so the shape does not contradict the words.
    expect(target.querySelector('.dist-bar')?.classList.contains('dist-bar-warn')).toBe(true);
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

  // A zero is an answer. "This owner has four services and nothing running" and "no
  // consumer is breaking" are the most useful things these charts say, and dropping the
  // row deleted them -- along with the row-for-row alignment between two charts drawn
  // over the same population, which is the whole reason they sit side by side.
  it('keeps a zero row, and gives it no bar', () => {
    comp = mount(HorizontalBars, { target, props: { title: 'By category', items: bars } });
    expect(target.querySelectorAll('.hb-row')).toHaveLength(3);

    const rows = Array.from(target.querySelectorAll('.hb-row'));
    const zero = rows.find((r) => r.textContent?.includes('Readiness'));
    expect(zero?.textContent).toContain('0');
    // No stub. A 2% sliver would read as "a little" where the answer is "none".
    expect((zero?.querySelector('.hb-fill') as HTMLElement).style.width).toBe('0%');
    // And the non-zero rows still scale against the largest value, not against zero.
    const six = rows.find((r) => r.textContent?.includes('Non-compliant'));
    expect((six?.querySelector('.hb-fill') as HTMLElement).style.width).toBe('100%');
  });

  it('says so plainly when every row is zero, instead of a scale relative to nothing', () => {
    comp = mount(HorizontalBars, {
      target,
      props: { title: 'By category', items: [{ label: 'A', value: 0 }, { label: 'B', value: 0 }] },
    });
    expect(target.querySelectorAll('.hb-row')).toHaveLength(2);
    expect(target.querySelector('.hb-scale')?.textContent).toBe('Every row here is zero, so no bar is drawn.');
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

  // A ranked chart is mostly small numbers, so "1 consumers" is the common case rather
  // than the edge case. The singular is supplied by the caller because an irregular
  // noun cannot be recovered by stripping an "s".
  it('uses the caller-supplied singular for a row of one, and the plural otherwise', () => {
    comp = mount(HorizontalBars, {
      target,
      props: {
        title: 'By category',
        items: [{ label: 'Checkout', value: 1 }, { label: 'Billing', value: 4 }],
        unit: 'consumers',
        unitOne: 'consumer',
      },
    });
    const values = Array.from(target.querySelectorAll('.hb-value')).map((n) => n.textContent);
    expect(values).toEqual(['1 consumer', '4 consumers']);
  });

  it('falls back to the plural when no singular is given, rather than dropping the unit', () => {
    comp = mount(HorizontalBars, { target, props: { title: 'By category', items: [{ label: 'Checkout', value: 1 }], unit: 'items' } });
    expect(target.querySelector('.hb-value')?.textContent).toBe('1 items');
  });
});

// @ts-expect-error — Svelte components have no declaration files
import PostureBars from './PostureBars.svelte';

/**
 * PostureBars is the one place the fleet, a service and an owner draw the same three
 * orthogonal questions, so what is asserted here is that the three surfaces cannot
 * drift: same dimensions, same denominator rule, and a drill-down scoped to whatever
 * population was drawn.
 */
const posture = {
  targets: 6,
  compliance: { compliant: 3, nonCompliant: 2, unknown: 1 },
  links: { exact: 4, inferred: 1, unresolved: 1 },
  evidence: { withEvidence: 5, withoutEvidence: 1, stale: 2, oldest: '2026-07-01T00:00:00Z', newest: '2026-07-29T00:00:00Z' },
  findings: { errors: 1, warnings: 2 },
};

describe('PostureBars', () => {
  it('asks all three orthogonal questions, and never collapses them into a score', () => {
    comp = mount(PostureBars, { target, props: { summary: posture } });
    const titles = Array.from(target.querySelectorAll('.dist-title')).map((h) => h.textContent);
    expect(titles).toEqual(['Compliance', 'Revision-match certainty', 'Evidence freshness', 'Findings by severity']);
    const text = target.textContent || '';
    // Exact counts, and each proportion states the population it is a proportion of.
    for (const fragment of ['Compliant', '3', 'Not compliant', '2', 'Exact', '4', 'Stale evidence', 'No evidence', 'of 6']) {
      expect(text).toContain(fragment);
    }
  });

  it('uses the backend population as the denominator, not the sum of the buckets', () => {
    // Five classified targets out of a population of six: the missing one is shown,
    // not absorbed. A page that rescaled to 5 would report a proportion of a
    // population nobody counted.
    comp = mount(PostureBars, {
      target,
      props: { summary: { targets: 6, compliance: { compliant: 5 } } },
    });
    const legend = target.querySelector('.dist-legend')?.textContent || '';
    expect(legend).toContain('Unclassified');
    expect(legend).toContain('of 6');
  });

  it('scopes every drill-down to the population it drew', () => {
    comp = mount(PostureBars, {
      target,
      props: { summary: posture, attentionUrl: (c: string) => `#/fleet/attention?service=svc&category=${c}` },
    });
    const hrefs = Array.from(target.querySelectorAll('.dist-legend a')).map((a) => a.getAttribute('href'));
    // Every href carries the scope; a service page must never send the user to the
    // fleet-wide backlog.
    expect(hrefs.every((h) => h?.includes('service=svc'))).toBe(true);
    expect(hrefs).toContain('#/fleet/attention?service=svc&category=non-compliant');
    expect(hrefs).toContain('#/fleet/attention?service=svc&category=unknown');
    expect(hrefs).toContain('#/fleet/attention?service=svc&category=unresolved');
    expect(hrefs).toContain('#/fleet/attention?service=svc&category=stale');
    // Compliant and Exact have no destination by design: there is no list of things
    // that are fine, and inventing one would be a dead link.
    const linked = Array.from(target.querySelectorAll('.dist-legend a')).map((a) => a.textContent || '');
    expect(linked.some((t) => t.includes('Compliant') && !t.includes('Non-compliant'))).toBe(false);
    expect(linked.some((t) => t.startsWith('Exact'))).toBe(false);
  });

  it('renders plain text, not dead links, when the caller has no scoped destination', () => {
    comp = mount(PostureBars, { target, props: { summary: posture } });
    expect(target.querySelectorAll('.dist-legend a')).toHaveLength(0);
  });

  it('states the evidence window in words, so freshness is not only a bar length', () => {
    comp = mount(PostureBars, { target, props: { summary: posture } });
    expect(target.querySelector('.pb-hint')?.textContent).toContain('Evidence spans');
  });

  it('says nothing is running rather than drawing three empty bars', () => {
    comp = mount(PostureBars, { target, props: { summary: { targets: 0 }, empty: 'Nothing observed here yet.' } });
    expect(target.querySelector('.pb-dists')).toBeNull();
    expect(target.querySelector('.pb-hint')?.textContent).toBe('Nothing observed here yet.');
  });

  it('omits the findings bar entirely when there are no findings', () => {
    comp = mount(PostureBars, { target, props: { summary: { ...posture, findings: {} } } });
    const titles = Array.from(target.querySelectorAll('.dist-title')).map((h) => h.textContent);
    expect(titles).not.toContain('Findings by severity');
  });
});
