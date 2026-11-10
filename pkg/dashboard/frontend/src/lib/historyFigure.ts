/**
 * The Delivery History figure's pure model (SPEC 7): "What happened in the last 24
 * hours, and where is the record missing?", as a Timeline of twelve equal columns
 * stacked by kind (worst on top), a count over each that opens the entries of that
 * slice, and a dashed axis with ⊗ under a column whose record is missing. The counts
 * are the server's timeline (B7), over every page, never the entries on screen.
 */
import type { components } from './generated/schema.ts';
import { countOf, formatCount } from './format.ts';
import type { FigTone } from './figure.ts';

type Timeline = components['schemas']['Delivery.HistoryTimeline'];
type Counts = Timeline['totals'];
export type TlKind = 'failed' | 'held' | 'acceptedUnknown' | 'unverified' | 'released' | 'other';

/** Canonical order (SPEC 2.2): err, warn, unknown (hatch), ok, then the rest. */
export const TL_KINDS: { key: TlKind; tone: FigTone; hatch?: boolean; label: string }[] = [
  { key: 'failed', tone: 'err', label: 'Failed' },
  { key: 'held', tone: 'warn', label: 'Held for a person' },
  { key: 'acceptedUnknown', tone: 'neutral', hatch: true, label: 'Unknown, accepted by a person' },
  // An override, or a promote a rollback or override preempted: it moved the Stage
  // and was never verified, so it is an unknown, never drawn as released.
  { key: 'unverified', tone: 'neutral', hatch: true, label: 'Released, not verified' },
  { key: 'released', tone: 'ok', label: 'Released' },
  { key: 'other', tone: 'neutral', label: 'Other' },
];

/** The tallest column, in px; a non-zero kind keeps a 4px floor (S5). */
export const TL_HEIGHT = 88;
/** The space between two stacked segments, in px (`.tl-bar` gap). */
const TL_GAP = 1;
const TL_FLOOR = 4;

/**
 * segHeights splits a column of n changes into its segments, so the drawn column
 * (segments, floors and gaps) is never taller than TL_HEIGHT (C4: a column that grew
 * past its box covered the count above it). The column is scaled to the tallest; each
 * non-zero kind keeps its floor, and the largest kind gives back what the floors took.
 */
export function segHeights(ns: number[], max: number): number[] {
  const n = ns.reduce((a, b) => a + b, 0);
  if (!n) return ns.map(() => 0);
  const col = Math.max(TL_FLOOR * ns.length, Math.round((n / max) * (TL_HEIGHT - TL_GAP * (ns.length - 1))));
  const out = ns.map((k) => Math.max(TL_FLOOR, Math.round((k / n) * col)));
  const big = ns.indexOf(Math.max(...ns));
  out[big] = Math.max(TL_FLOOR, out[big] + col - out.reduce((a, b) => a + b, 0));
  return out;
}

/** tickLabel is a time with its day when it is not the window's last day (SPEC 6.2 r3). */
export function tickLabel(iso: string, endIso: string): string {
  const t = new Date(iso), end = new Date(endIso);
  if (Number.isNaN(t.getTime())) return iso;
  const hm = t.toISOString().slice(11, 16);
  const day = (d: Date) => Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate());
  const back = Math.round((day(end) - day(t)) / 86_400_000);
  if (back === 0) return hm;
  if (back === 1) return `yesterday ${hm}`;
  return `${t.toLocaleString('en', { month: 'short', day: 'numeric', timeZone: 'UTC' })} ${hm}`;
}

const sum = (c: Counts) => TL_KINDS.reduce((n, k) => n + (c[k.key] ?? 0), 0);
// A column's count is the length of the list it opens, which keeps the gap and
// unrecordable lines the drawn kinds leave out.
const lines = (c: Counts) => sum(c) + (c.missing ?? 0);

/**
 * historyModel shapes the figure. `source` is where the timeline was counted: one
 * Stage's signed records, which show a missing record; one Stage's status, or the
 * changes still in the cluster across Stages, which cannot. `live` is true when the
 * window ends now (no until), so the last tick says "now". `problems` counts what the
 * Stage's records lack that no line stands for (a concluded change with no record, a
 * break in the chain, a record that could not be read or verified): it is in no
 * column, so the caption never says the record is whole while it is not.
 */
export function historyModel(t: Timeline, { source, live, problems = 0 }: { source: 'records' | 'status' | 'cluster'; live: boolean; problems?: number }) {
  const missingKnown = source === 'records';
  const buckets = t.buckets ?? [];
  const max = Math.max(1, ...buckets.map(sum));
  const columns = buckets.map((b, i) => {
    const kinds = TL_KINDS.filter((k) => b[k.key]);
    const hs = segHeights(kinds.map((k) => b[k.key]), max);
    return {
      i, start: b.start, end: b.end, n: lines(b), missing: missingKnown ? b.missing : 0,
      segs: kinds.map((k, j) => ({ k, n: b[k.key], h: hs[j] })),
    };
  });
  const total = lines(t.totals);
  const gaps = columns.filter((c) => c.missing);
  const end = live ? `now, ${tickLabel(t.end, t.end)}` : tickLabel(t.end, t.end);
  const ticks = { first: tickLabel(t.start, t.end), mid: buckets[6] ? tickLabel(buckets[6].start, t.end) : '', last: end };
  const window = `${tickLabel(t.start, t.end)} to ${end}`;
  const caption = [
    `${formatCount(t.totals.failed)} failed and ${formatCount(t.totals.held)} held for a person, of ${countOf(total, 'change')} from ${window} UTC.`,
    source === 'cluster' ? 'Counted from the changes still in the cluster, which cannot show a missing record: open one Stage to read its signed records.'
      : source === 'status' ? 'Read from the changes still in the cluster: this Stage keeps no signed records, so a missing record cannot be shown.'
      : [gaps.length ? `The record is missing in ${countOf(gaps.length, 'slice')}, ${countOf(t.totals.missing, 'record')} in all.` : problems ? '' : 'No record is missing in this window.',
        problems ? `${countOf(problems, 'record problem')} ${problems === 1 ? 'is' : 'are'} listed below and drawn in no column, so the record may be incomplete.` : ''].filter(Boolean).join(' '),
    t.untimed ? `${countOf(t.untimed, 'change')} with no time ${t.untimed === 1 ? 'is' : 'are'} not drawn.` : '',
  ].filter(Boolean).join(' ');
  const gapWords = gaps.map((g) => `${g.missing === 1 ? 'one record' : `${formatCount(g.missing)} records`} between ${tickLabel(g.start, t.end)} and ${tickLabel(g.end, t.end)}`);
  return {
    missingKnown, columns, total, gaps, gapWords, ticks, window, caption,
    figRef: missingKnown && gaps.length ? 'A dashed axis with ⊗ under a column marks where the record is missing.' : 'Each column is one twelfth of the window, worst on top.',
    alt: `${window}: ${TL_KINDS.map((k) => `${t.totals[k.key]} ${k.label.toLowerCase()}`).join(', ')}${missingKnown ? `; ${t.totals.missing} records missing` : ''}${missingKnown && problems ? `; ${problems} more record problems` : ''}.`,
  };
}
