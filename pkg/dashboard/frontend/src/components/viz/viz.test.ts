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
import { mount, unmount, flushSync } from 'svelte';
// @ts-expect-error — Svelte components have no declaration files
import DistributionBar from './DistributionBar.svelte';
import ColumnChart from './ColumnChart.svelte';
import LineChart from './LineChart.svelte';
import { get } from 'svelte/store';
import { tipLabel, tipState, hideTip, type TipModel } from '../../lib/chartTip.ts';

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
    expect(legend).toContain('(57% of 7)');
  });

  // A tenth of a percent on a population of 7 is precision the data does not have:
  // the smallest step 7 services can take is 14 points. Above a hundred the tenth is
  // a real distinction and stays.
  it('drops the decimal below a hundred and keeps it above', () => {
    comp = mount(DistributionBar, { target, props: { title: 'Compliance', segments: [{ label: 'A', value: 1, tone: 'ok' }], total: 3 } });
    expect(target.querySelector('.dist-pct')?.textContent).toBe('(33% of 3)');
    unmount(comp);
    comp = mount(DistributionBar, { target, props: { title: 'Compliance', segments: [{ label: 'A', value: 1, tone: 'ok' }], total: 400 } });
    expect(target.querySelector('.dist-pct')?.textContent).toBe('(0.3% of 400)');
  });

  // The other end of the same rounding: one invalid target in a fleet of three thousand
  // is 0.033%, and a flat "0%" beside a count of 1 is a row contradicting itself.
  it('never prints a non-zero count as a zero share', () => {
    comp = mount(DistributionBar, { target, props: { title: 'Compliance', segments: [{ label: 'Invalid', value: 1, tone: 'err' }], total: 3000 } });
    expect(target.querySelector('.dist-pct')?.textContent).toBe('(<0.1% of 3,000)');
  });

  it('draws no bar and no share for a population of one', () => {
    comp = mount(DistributionBar, { target, props: { title: 'Compliance', segments: [{ label: 'Unknown', value: 1, tone: 'warn' }], total: 1 } });
    expect(target.querySelector('.dist-bar')).toBeNull();
    expect(target.querySelector('.dist-pct')).toBeNull();
    expect(target.querySelector('.dist-legend')?.textContent).toContain('Unknown');
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

  // A four-way bar of similar tones asks the reader to count slices to find the one a
  // legend row names. Hovering either half dims the others; it is presentational only,
  // so the accessible tree is unchanged and every value stays printed as text.
  it('links a legend row to its slice on hover, and dims the others', () => {
    comp = mount(DistributionBar, { target, props: { title: 'Compliance', segments, total: 7 } });
    const items = Array.from(target.querySelectorAll('.dist-item')) as HTMLElement[];
    expect(target.querySelectorAll('.dist-seg.dim')).toHaveLength(0);
    items[1].dispatchEvent(new MouseEvent('mouseenter'));
    flushSync();
    expect(Array.from(target.querySelectorAll('.dist-seg')).map((s) => s.classList.contains('dim')))
      .toEqual([true, false, true]);
    items[1].dispatchEvent(new MouseEvent('mouseleave'));
    flushSync();
    expect(target.querySelectorAll('.dist-seg.dim')).toHaveLength(0);
  });

  // The page is filtered to one bucket: the bar has to say which, or it is a summary of
  // a population the reader is no longer looking at. Never colour alone -- the legend
  // link carries aria-current, which is what a screen reader reads.
  it('marks the bucket the page is filtered to', () => {
    comp = mount(DistributionBar, {
      target,
      props: { title: 'Compliance', segments, total: 7, selected: 'Not compliant' },
    });
    const slices = Array.from(target.querySelectorAll('.dist-seg'));
    expect(slices.map((s) => s.classList.contains('sel'))).toEqual([false, true, false]);
    expect(slices.map((s) => s.classList.contains('dim'))).toEqual([true, false, true]);
    expect(target.querySelector('.dist-legend a')?.getAttribute('aria-current')).toBe('true');
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
      { label: 'Owners disagree', value: 3, tone: 'err' },
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
    // Three slices, not four: an empty bucket draws nothing.
    expect(target.querySelectorAll('.dist-seg')).toHaveLength(3);
  });

  // One stacked bar at every size, readable without the legend: each slice prints its
  // own count and name and carries the exact share as a tooltip.
  it('labels every slice directly and gives it a tooltip with the share', () => {
    comp = mount(DistributionBar, { target, props: { title: 'Compliance', segments, total: 7 } });
    const slices = Array.from(target.querySelectorAll('.dist-seg'));
    expect(slices.map((s) => s.textContent?.trim())).toEqual(['4 Compliant', '2 Not compliant', '1 Unknown']);
    expect(slices[1].getAttribute('title')).toBe('Not compliant: 2 (29% of 7)');
    unmount(comp);

    comp = mount(DistributionBar, { target, props: { title: 'Compliance', segments, total: 400 } });
    // Three buckets plus the Unclassified remainder of the 400.
    expect(target.querySelectorAll('.dist-seg')).toHaveLength(4);
  });

  it('states an over-count in words and draws the bar as suspect', () => {
    comp = mount(DistributionBar, { target, props: { title: 'Compliance', segments, total: 5 } });
    expect(target.querySelector('.dist-bar')?.classList.contains('dist-bar-warn')).toBe(true);
    expect(target.querySelector('[data-testid="dist-inconsistent"]')).not.toBeNull();
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

  // Unknown is never 0 (Metrics trends): a bucket with no records draws a full hatched
  // track and prints its reason in words, so the fact survives without colour.
  it('prints an unknown row as its reason, hatched, never as 0, and keeps it out of the scale', () => {
    comp = mount(HorizontalBars, {
      target,
      props: { title: 'Per week', items: [{ label: 'week of 3 Nov', value: 4 }, { label: 'week of 10 Nov', value: 0, unknown: 'no records', href: '#/metrics' }] },
    });
    const rows = Array.from(target.querySelectorAll('.hb-row'));
    const unk = rows[1];
    expect(unk.querySelector('.hb-value')?.textContent).toBe('no records');
    expect(unk.textContent).not.toMatch(/\b0\b/);
    const fill = unk.querySelector('.hb-fill') as HTMLElement;
    expect(fill.classList.contains('hatch')).toBe(true);
    expect(fill.style.width).toBe('100%');
    expect((rows[0].querySelector('.hb-fill') as HTMLElement).style.width).toBe('100%');
    expect(target.querySelector('.hb-scale')?.textContent).toContain('largest value shown (4)');
    // A linked unknown row stays a keyboard-operable link.
    expect(unk.querySelector('a')?.getAttribute('href')).toBe('#/metrics');
  });

  it('lets panels that share one domain state the scale once, instead of in each panel', () => {
    comp = mount(HorizontalBars, { target, props: { title: 'prod', items: [{ label: 'Held', value: 0.2, valueText: '1/5 (20%)' }], domainMax: 0.5, scaleText: '' } });
    expect(target.querySelector('.hb-scale')).toBeNull();
    expect(target.querySelector('.hb-value')?.textContent).toBe('1/5 (20%)');
    expect((target.querySelector('.hb-fill') as HTMLElement).style.width).toBe('40%');
  });

  it('states no scale and no all-zero line when every row is unknown', () => {
    comp = mount(HorizontalBars, { target, props: { title: 'Per week', items: [{ label: 'a', value: 0, unknown: 'no records' }] } });
    expect(target.querySelector('.hb-scale')).toBeNull();
    expect(target.textContent).not.toMatch(/\b0\b/);
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
    expect(hrefs).toContain('#/fleet/attention?service=svc&category=stale');
    // Compliant and Exact have no destination by design: there is no list of things
    // that are fine, and inventing one would be a dead link.
    const linked = Array.from(target.querySelectorAll('.dist-legend a')).map((a) => a.textContent || '');
    expect(linked.some((t) => t.includes('Compliant') && !t.includes('Non-compliant'))).toBe(false);
    expect(linked.some((t) => t.startsWith('Exact'))).toBe(false);
  });

  // Attention `unresolved` lists unresolved DEPENDENCIES, so a revision-match bucket
  // linking there opened a list of something else.
  it('never links the Ambiguous or Unresolved revision-match buckets', () => {
    comp = mount(PostureBars, {
      target,
      props: {
        summary: { ...posture, links: { exact: 3, ambiguous: 1, unresolved: 2 } },
        attentionUrl: (c: string) => `#/fleet/attention?category=${c}`,
      },
    });
    const anchors = Array.from(target.querySelectorAll('.dist-legend a'));
    const hrefs = anchors.map((a) => a.getAttribute('href') || '');
    expect(hrefs.some((h) => h.includes('category=unresolved'))).toBe(false);
    const linked = anchors.map((a) => a.textContent || '');
    expect(linked.some((t) => t.includes('Ambiguous') || t.includes('Unresolved'))).toBe(false);
    expect(hrefs).toContain('#/fleet/attention?category=non-compliant');
    expect(hrefs).toContain('#/fleet/attention?category=stale');
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

// The column and line primitives of the Metrics page (#597): unknown is hatched and
// never drawn as 0, a real 0 is a stub, every word is HTML and every bucket reads by
// keyboard with the same words as its tooltip.
const weeks = (n: number) => Array.from({ length: n }, (_, i) => ({ label: `Week ${i + 1}`, short: `W${i + 1}` }));
const tipFor = (values: (number | null)[]) => (i: number): TipModel => ({ title: `Week ${i + 1}`, rows: [{ value: String(values[i]), label: 'caught' }] });
const texts = (sel: string) => Array.from(target.querySelectorAll(sel)).map((e) => (e.textContent || '').trim());

describe('ColumnChart', () => {
  it('draws an all-unknown chart with its reason and never a 0', () => {
    comp = mount(ColumnChart, { target, props: { buckets: weeks(3), series: [{ key: 'b', label: 'caught', values: [null, null, null] }], unknown: ['no records', 'no records', 'no CI records'] } });
    flushSync();
    const label = target.querySelector('.cc-labels')?.textContent || '';
    expect(label).not.toMatch(/\b0\b/);
    expect(target.querySelectorAll('.cc-stub')).toHaveLength(0);
    expect(target.querySelectorAll('.cc-col')).toHaveLength(0);
    const bands = target.querySelectorAll('.cc-unknown');
    expect(bands).toHaveLength(3);
    for (const b of Array.from(bands)) expect(b.childNodes).toHaveLength(0);
    for (const hit of Array.from(target.querySelectorAll('.cc-hit'))) expect(hit.getAttribute('aria-label')).toMatch(/no (CI )?records.*Not counted as 0/);
  });

  it('puts the reason on an opaque plate when the band is wide enough', () => {
    comp = mount(ColumnChart, { target, props: { buckets: weeks(2), series: [{ key: 'b', label: 'caught', values: [3, null] }], unknown: [null, 'no records'] } });
    flushSync();
    expect(texts('.cc-plate')).toEqual(['no records']);
  });

  it('draws a real 0 as a stub and says 0 in its tooltip', () => {
    const values = [0, 4];
    comp = mount(ColumnChart, { target, props: { buckets: weeks(2), series: [{ key: 'b', label: 'caught', values }], tipOf: tipFor(values) } });
    flushSync();
    expect(target.querySelectorAll('.cc-stub')).toHaveLength(1);
    expect(target.querySelectorAll('.cc-col')).toHaveLength(1);
    expect(target.querySelectorAll('.cc-hit')[0].getAttribute('aria-label')).toBe('Week 1, caught 0');
  });

  it('stacks segments in series order with a 2px surface gap', () => {
    comp = mount(ColumnChart, { target, props: { buckets: weeks(1), series: [
      { key: 'clear', label: 'Clear', color: 'var(--c-ok)', values: [6] },
      { key: 'blocked', label: 'Blocked', color: 'var(--c-err)', values: [2] },
      { key: 'unv', label: 'Unverified', texture: 'dots', values: [2] },
    ] } });
    const segs = Array.from(target.querySelectorAll('.cc-col')) as SVGElement[];
    expect(segs.map((s) => s.getAttribute('data-series'))).toEqual(['clear', 'blocked', 'unv']);
    expect(segs[2].getAttribute('fill')).toBe('url(#viz-dots)');
    const box = (s: SVGElement) => ({ y: Number(s.getAttribute('data-y')), h: Number(s.getAttribute('data-h')) });
    expect(box(segs[0]).y - (box(segs[1]).y + box(segs[1]).h)).toBeCloseTo(2);
    expect(box(segs[1]).y - (box(segs[2]).y + box(segs[2]).h)).toBeCloseTo(2);
  });

  it('labels the peak and the last column only', () => {
    comp = mount(ColumnChart, { target, props: { buckets: weeks(8), series: [{ key: 'b', label: 'caught', values: [1, 9, 2, 3, 2, 1, 4, 5] }] } });
    flushSync();
    expect(texts('.cc-val')).toEqual(['9', '5']);
  });

  it('makes every bucket a tab stop named with its tooltip text', () => {
    const values = [2, 3, null];
    comp = mount(ColumnChart, { target, props: { buckets: weeks(3), series: [{ key: 'b', label: 'caught', values }], unknown: [null, null, 'no records'], tipOf: tipFor(values) } });
    flushSync();
    const hits = Array.from(target.querySelectorAll('.cc-hit'));
    expect(hits).toHaveLength(3);
    hits.slice(0, 2).forEach((h, i) => {
      expect(h.getAttribute('tabindex')).toBe('0');
      expect(h.getAttribute('aria-label')).toBe(tipLabel(tipFor(values)(i)));
    });
  });

  it('draws a hatched cap on a known column with unknown on top', () => {
    comp = mount(ColumnChart, { target, props: { buckets: weeks(2), series: [{ key: 'p', label: 'protected', values: [3, 4] }], caps: [false, true] } });
    flushSync();
    expect(target.querySelectorAll('.cc-cap')).toHaveLength(1);
  });

  it('draws a cap on a 0 with unknown on top, so it never looks like a real 0', () => {
    comp = mount(ColumnChart, { target, props: { buckets: weeks(2), series: [{ key: 'p', label: 'protected', values: [0, 0] }], caps: [true, false] } });
    flushSync();
    expect(target.querySelectorAll('.cc-stub')).toHaveLength(2);
    expect(target.querySelectorAll('.cc-cap')).toHaveLength(1);
  });

  it('never prints half a count on the axis and formats large counts', () => {
    const stack = (v: number) => [{ key: 'a', label: 'a', values: [v] }, { key: 'b', label: 'b', values: [0] }];
    comp = mount(ColumnChart, { target, props: { buckets: weeks(1), series: stack(1) } });
    flushSync();
    expect(texts('.cc-y')).toEqual(['0', '1']);
    unmount(comp);
    comp = mount(ColumnChart, { target, props: { buckets: weeks(1), series: stack(1500) } });
    flushSync();
    expect(texts('.cc-y')).toEqual(['0', '800', '1,600']);
  });

  it('prints no axis text in compact mode', () => {
    comp = mount(ColumnChart, { target, props: { compact: true, buckets: weeks(4), series: [{ key: 'b', label: 'caught', values: [1, 0, null, 3] }] } });
    flushSync();
    expect(target.querySelector('.cc-labels')).toBeNull();
    expect(target.querySelectorAll('.cc-hit')).toHaveLength(4);
  });
});

describe('LineChart', () => {
  const series = (values: (number | null)[], partial?: boolean[]) => [{ key: 's', label: 'Services observed', color: 'var(--chart-1)', values, partial }];
  afterEach(() => hideTip());

  it('breaks the line at a missing value, never dropping it to 0', () => {
    comp = mount(LineChart, { target, props: { buckets: weeks(5), series: series([3, 4, null, 5, 6]), label: 'Coverage' } });
    flushSync();
    const d = target.querySelector('.lc-line')?.getAttribute('d') || '';
    expect(d.match(/M/g)).toHaveLength(2);
  });

  it('hatches a bucket where every series is missing', () => {
    comp = mount(LineChart, { target, props: { buckets: weeks(3), series: [...series([3, null, 5]), { key: 'd', label: 'Described', color: 'var(--chart-2)', values: [1, null, 2] }], label: 'Coverage' } });
    flushSync();
    expect(target.querySelectorAll('.lc-unknown')).toHaveLength(1);
  });

  it('names a run of missing buckets once, with one band and one plate', () => {
    comp = mount(LineChart, { target, props: { buckets: weeks(5), series: series([3, null, null, 5, 6]), label: 'Coverage' } });
    flushSync();
    expect(target.querySelectorAll('.lc-unknown')).toHaveLength(1);
    expect(texts('.lc-plate')).toEqual(['no records']);
  });

  it('says why a bucket with records draws no point, and never half a count', () => {
    comp = mount(LineChart, { target, props: { buckets: weeks(5), series: series([3, null, null, 5, 1]), unknown: [null, 'no coverage snapshot', null, null, null], label: 'Coverage' } });
    flushSync();
    expect(texts('.lc-plate')).toEqual(['no coverage snapshot']);
    expect(texts('.lc-y')).toEqual(['0', '5']);
    const hit = target.querySelector('.lc-hit') as SVGElement;
    hit.dispatchEvent(new FocusEvent('focus'));
    hit.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowLeft', bubbles: true }));
    hit.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowLeft', bubbles: true }));
    hit.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowLeft', bubbles: true }));
    flushSync();
    expect(get(tipState)?.model.rows[0].label).toBe('no coverage snapshot');
  });

  it('shows the tapped bucket on a touch tap and keeps it past pointerleave', () => {
    comp = mount(LineChart, { target, props: { buckets: weeks(4), series: series([3, 8, 5, 2]), label: 'Coverage' } });
    flushSync();
    const hit = target.querySelector('.lc-hit') as SVGElement;
    // jsdom: the hit rect is 0 wide, so any tap reads as the first bucket.
    const ev = (type: string) => Object.assign(new MouseEvent(type, { clientX: 0, clientY: 0, bubbles: true }), { pointerType: 'touch' });
    hit.dispatchEvent(ev('pointerup'));
    hit.dispatchEvent(ev('pointerleave'));
    flushSync();
    expect(get(tipState)?.model.title).toBe('Week 1');
  });

  it('draws one y scale', () => {
    comp = mount(LineChart, { target, props: { buckets: weeks(3), series: series([3, 8, 5]), label: 'Coverage' } });
    flushSync();
    expect(target.querySelectorAll('.lc-yaxis')).toHaveLength(1);
  });

  it('moves the crosshair with the arrow keys and updates the tooltip', () => {
    comp = mount(LineChart, { target, props: { buckets: weeks(3), series: series([3, 8, 5]), label: 'Coverage per week; use arrow keys to read each week' } });
    flushSync();
    const hit = target.querySelector('.lc-hit') as SVGElement;
    expect(hit.getAttribute('tabindex')).toBe('0');
    expect(hit.getAttribute('aria-label')).toBe('Coverage per week; use arrow keys to read each week');
    hit.dispatchEvent(new FocusEvent('focus'));
    flushSync();
    expect(get(tipState)?.model.title).toBe('Week 3');
    hit.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowLeft', bubbles: true }));
    flushSync();
    expect(get(tipState)?.model.title).toBe('Week 2');
    expect(get(tipState)?.model.rows[0].value).toBe('8');
    expect(target.querySelector('.lc-cross')?.getAttribute('opacity')).toBe('1');
    hit.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    flushSync();
    expect(get(tipState)).toBeNull();
  });

  it('draws a partial point hollow and hatched', () => {
    comp = mount(LineChart, { target, props: { buckets: weeks(3), series: series([3, 8, 5], [false, false, true]), label: 'Coverage' } });
    flushSync();
    const p = target.querySelectorAll('.lc-partial');
    expect(p).toHaveLength(1);
    expect(p[0].getAttribute('fill')).toBe('url(#viz-hatch)');
  });
});

describe('HorizontalBars additions (#597)', () => {
  const fill = (i: number) => (target.querySelectorAll('.hb-fill')[i] as HTMLElement).style.width;

  it('takes a shared domain, so panels compare and a 0.6 rate draws longer than 0.5', () => {
    comp = mount(HorizontalBars, { target, props: { title: 'prod', domainMax: 0.6, items: [
      { label: 'Held', value: 0.5 }, { label: 'Failed', value: 0.6 },
    ] } });
    expect(fill(0)).toBe('83%');
    expect(fill(1)).toBe('100%');
  });

  it('draws a rate under the shared domain shorter than the full track', () => {
    comp = mount(HorizontalBars, { target, props: { title: 'prod', domainMax: 0.5, items: [{ label: 'Held', value: 0.2 }] } });
    expect(fill(0)).toBe('40%');
  });

  it('prints valueText in place of the number and keeps a real 0 bar-less', () => {
    comp = mount(HorizontalBars, { target, props: { title: 'prod', domainMax: 0.5, items: [
      { label: 'Held', value: 0.2, valueText: '1/5 (20%)' }, { label: 'Failed', value: 0, valueText: '0/5 (0%)' },
    ] } });
    expect(texts('.hb-value')).toEqual(['1/5 (20%)', '0/5 (0%)']);
    expect(fill(1)).toBe('0%');
  });
});

describe('DistributionBar dots texture (#597)', () => {
  it('draws an unverified bucket with the dot texture on its segment and its swatch', () => {
    comp = mount(DistributionBar, { target, props: { title: 'Gates', segments: [
      { label: 'Clear', value: 6, tone: 'ok' }, { label: 'Unverified', value: 2, texture: 'dots' },
    ] } });
    expect(target.querySelectorAll('.dist-seg.dots')).toHaveLength(1);
    expect(target.querySelectorAll('.dist-swatch.dots')).toHaveLength(1);
    expect(target.querySelectorAll('.hatch')).toHaveLength(0);
  });
});

import RangeDots from './RangeDots.svelte';
import RateGrid from './RateGrid.svelte';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { vi, beforeAll, afterAll } from 'vitest';

describe('RangeDots', () => {
  // elide.ts caches its measuring context on first use: give it a fixed-width font.
  beforeAll(() => { vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({ font: '', measureText: (s: string) => ({ width: [...s].length * 10 }) } as never); });
  afterAll(() => vi.restoreAllMocks());
  afterEach(() => hideTip());
  const hitLabels = () => Array.from(target.querySelectorAll('.rd-hit')).map((h) => h.getAttribute('aria-label') || '');

  it('draws an unjoined lead time as a hatched unknown row, never at the axis start', () => {
    comp = mount(RangeDots, { target, props: { rows: [{ name: 'prod', p50: 3600, p90: 7200 }, { name: 'stage', p50: null, p90: null }] } });
    flushSync();
    expect(target.querySelectorAll('.rd-dot')).toHaveLength(1);
    const band = target.querySelectorAll('.rd-unknown');
    expect(band).toHaveLength(1);
    expect(band[0].childNodes).toHaveLength(0);
    expect(texts('.rd-plate')).toEqual(['unknown']);
    expect(hitLabels()[1]).toContain('p50 unknown');
  });

  it('draws a 0s lead as a finite dot that says 0s, never unknown', () => {
    comp = mount(RangeDots, { target, props: { rows: [{ name: 'prod', p50: 0, p90: 30 }, { name: 'stage', p50: 600, p90: 900 }] } });
    flushSync();
    const dots = Array.from(target.querySelectorAll('.rd-dot'));
    expect(dots).toHaveLength(2);
    for (const d of dots) expect(Number.isFinite(Number(d.getAttribute('cx')))).toBe(true);
    expect(hitLabels()[0]).toContain('p50 0s');
    expect(hitLabels()[0]).not.toContain('unknown');
    expect(target.querySelectorAll('.rd-unknown')).toHaveLength(0);
  });

  it('draws no whisker for a missing p90 and says p90 unknown', () => {
    comp = mount(RangeDots, { target, props: { rows: [{ name: 'prod', p50: 3600, p90: null }, { name: 'stage', p50: 60, p90: 120 }] } });
    flushSync();
    expect(target.querySelectorAll('.rd-whisker')).toHaveLength(1);
    expect(hitLabels()[0]).toContain('p90 unknown');
  });

  it('renders group headers in the given order', () => {
    comp = mount(RangeDots, { target, props: { rows: [
      { group: 'hop 2' }, { name: 'a', p50: 60, p90: 90 }, { group: 'entry' }, { name: 'b', p50: 30, p90: 40 },
    ] } });
    flushSync();
    expect(texts('.rd-group')).toEqual(['hop 2', 'entry']);
    expect(target.querySelectorAll('.rd-hit')).toHaveLength(2);
  });

  it('elides a name longer than its column and keeps the whole name in title', () => {
    const full = 'risk-us-eu-west-1-partner-feed-compliance';
    const cw = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'clientWidth');
    const sw = Object.getOwnPropertyDescriptor(Element.prototype, 'scrollWidth');
    const rects = Element.prototype.getClientRects;
    Object.defineProperty(HTMLElement.prototype, 'clientWidth', { configurable: true, get() { return this.classList.contains('fit') ? 200 : 0; } });
    Object.defineProperty(Element.prototype, 'scrollWidth', { configurable: true, get() { return (this.textContent ?? '').length * 10; } });
    Element.prototype.getClientRects = function () { return [{}] as unknown as DOMRectList; };
    try {
      comp = mount(RangeDots, { target, props: { rows: [{ name: full, p50: 60, p90: 90 }] } });
      flushSync();
      const label = target.querySelector('.rd-name') as HTMLElement;
      expect(label.getAttribute('title')).toBe(full);
      expect(label.querySelector('.fit')?.textContent).toContain('…');
      expect(label.querySelector('.fit-full')?.textContent).toBe(full);
    } finally {
      if (cw) Object.defineProperty(HTMLElement.prototype, 'clientWidth', cw);
      if (sw) Object.defineProperty(Element.prototype, 'scrollWidth', sw);
      Element.prototype.getClientRects = rects;
    }
  });

  it('gives every environment one focusable hit row with its tooltip words', () => {
    comp = mount(RangeDots, { target, props: { rows: [{ name: 'prod', p50: 3600, p90: 7200, foot: '1 promotion with no publish record is left out' }] } });
    flushSync();
    const hit = target.querySelector('.rd-hit') as SVGElement;
    expect(hit.getAttribute('tabindex')).toBe('0');
    expect(hit.getAttribute('aria-label')).toBe('Lead time to prod, p50 1h, p90 2h, 1 promotion with no publish record is left out');
  });
});

describe('RateGrid', () => {
  const columns = [{ key: 'held', label: 'Held' }, { key: 'failed', label: 'Failed' }];
  const cell = (count: number, of: number, bin?: 1 | 2 | 3 | 4) => ({ count, of, bin, foot: '' });
  const group = (key: string, open: boolean, rows = [
    { name: 'dev', cells: { held: cell(1, 10, 1), failed: cell(0, 10) } },
    { name: 'test', cells: { held: cell(2, 0), failed: cell(4, 10, 3) } },
  ]) => ({ key, label: key, title: `${key}, ${rows.length} environments`, count: rows.length, summary: { held: cell(3, 10, 3), failed: cell(4, 20, 2) }, rows, open });
  const cells = () => Array.from(target.querySelectorAll('.cell')) as HTMLElement[];

  it('hatches a rate with nothing to count, says unknown and keeps the count', () => {
    comp = mount(RateGrid, { target, props: { columns, groups: [group('entry', true)] } });
    flushSync();
    const unk = target.querySelectorAll('.cell.unk');
    expect(unk).toHaveLength(1);
    expect(unk[0].textContent).toContain('unknown');
    expect(unk[0].textContent).toContain('2 of 0');
    const hatch = unk[0].querySelector('.hatch') as HTMLElement;
    expect(hatch.childNodes).toHaveLength(0);
  });

  it('draws a real 0 as the zero cell, never hatched', () => {
    comp = mount(RateGrid, { target, props: { columns, groups: [group('entry', true)] } });
    flushSync();
    const zero = target.querySelectorAll('.cell.zero');
    expect(zero).toHaveLength(1);
    expect(zero[0].querySelector('.hatch')).toBeNull();
    expect(zero[0].textContent).toContain('0 of 10');
  });

  it('maps the bins onto the ordinal ramp', () => {
    comp = mount(RateGrid, { target, props: { columns, groups: [group('entry', true)] } });
    flushSync();
    expect(target.querySelectorAll('.cell.b1')).toHaveLength(1);
    expect(target.querySelectorAll('.cell.b3')).toHaveLength(2);
    const css = readFileSync(join(process.cwd(), 'src/components/viz/RateGrid.svelte'), 'utf8');
    for (const b of [1, 2, 3, 4]) expect(css).toMatch(new RegExp(`\\.b${b}\\s*\\{[^}]*var\\(--chart-hm-${b}\\)`));
  });

  it('renders a collapsed group as its summary row only and toggles by key', () => {
    const ontoggle = vi.fn();
    comp = mount(RateGrid, { target, props: { columns, groups: [group('hop 2', false)], ontoggle } });
    flushSync();
    expect(target.querySelectorAll('tbody th[scope="row"]')).toHaveLength(0);
    const btn = target.querySelector('th[scope="rowgroup"] button') as HTMLButtonElement;
    expect(btn.getAttribute('aria-expanded')).toBe('false');
    btn.click();
    expect(ontoggle).toHaveBeenCalledWith('hop 2');
  });

  it('is the only scroller, named and focusable', () => {
    comp = mount(RateGrid, { target, props: { columns, groups: [group('entry', true)] } });
    const sc = target.querySelector('.hm-scroll') as HTMLElement;
    expect(sc.getAttribute('tabindex')).toBe('0');
    expect(sc.getAttribute('role')).toBe('region');
    expect(sc.getAttribute('aria-label')).toBe('Delivery health heatmap, scrolls in both directions');
  });

  it('keeps every cell a table cell and is one tab stop that arrows move', () => {
    comp = mount(RateGrid, { target, props: { columns, groups: [group('entry', true)] } });
    flushSync();
    expect(Array.from(target.querySelectorAll('td')).filter((td) => td.hasAttribute('role'))).toEqual([]);
    const stops = () => cells().filter((c) => c.getAttribute('tabindex') === '0');
    expect(stops()).toHaveLength(1);
    const first = stops()[0];
    expect(first).toBe(cells()[0]);
    first.focus();
    first.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true }));
    expect(stops()).toEqual([cells()[1]]);
    expect(document.activeElement).toBe(cells()[1]);
    cells()[1].dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }));
    expect(stops()).toEqual([cells()[3]]);
    cells()[3].dispatchEvent(new KeyboardEvent('keydown', { key: 'Home', bubbles: true }));
    expect(stops()).toEqual([cells()[2]]);
  });

  it('pages a group locally with its own buttons, never a URL pager', () => {
    const onclick = vi.fn();
    comp = mount(RateGrid, { target, props: { columns, groups: [{ ...group('entry', true), more: { label: '1 to 2 of 40', actions: [{ label: 'Next', onclick }] } }] } });
    flushSync();
    expect(target.querySelector('[data-view-total]')).toBeNull();
    (target.querySelector('tr.more button') as HTMLButtonElement).click();
    expect(onclick).toHaveBeenCalled();
    expect(target.querySelector('tr.more')?.textContent).toContain('1 to 2 of 40');
  });

  it('says so when no environment matches', () => {
    comp = mount(RateGrid, { target, props: { columns, groups: [], emptyText: 'No environment matches this filter.' } });
    expect(target.querySelector('.hm')?.textContent).toContain('No environment matches this filter.');
  });
});
