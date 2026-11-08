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
export type TlKind = 'failed' | 'held' | 'acceptedUnknown' | 'released' | 'other';

/** Canonical order (SPEC 2.2): err, warn, unknown (hatch), ok, then the rest. */
export const TL_KINDS: { key: TlKind; tone: FigTone; hatch?: boolean; label: string }[] = [
  { key: 'failed', tone: 'err', label: 'Failed' },
  { key: 'held', tone: 'warn', label: 'Held for a person' },
  { key: 'acceptedUnknown', tone: 'neutral', hatch: true, label: 'Unknown, accepted by a person' },
  { key: 'released', tone: 'ok', label: 'Released' },
  { key: 'other', tone: 'neutral', label: 'Other' },
];

/** The tallest column, in px; a non-zero kind keeps a 4px floor (S5). */
export const TL_HEIGHT = 88;

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

/**
 * historyModel shapes the figure. `source` is where the timeline was counted: one
 * Stage's signed records, which show a missing record; one Stage's status, or the
 * changes still in the cluster across Stages, which cannot. `live` is true when the
 * window ends now (no until), so the last tick says "now".
 */
export function historyModel(t: Timeline, { source, live }: { source: 'records' | 'status' | 'cluster'; live: boolean }) {
  const missingKnown = source === 'records';
  const buckets = t.buckets ?? [];
  const max = Math.max(1, ...buckets.map(sum));
  const columns = buckets.map((b, i) => ({
    i, start: b.start, end: b.end, n: sum(b), missing: missingKnown ? b.missing : 0,
    segs: TL_KINDS.filter((k) => b[k.key]).map((k) => ({ k, n: b[k.key], h: Math.max(4, Math.round((b[k.key] / max) * TL_HEIGHT)) })),
  }));
  const total = sum(t.totals);
  const gaps = columns.filter((c) => c.missing);
  const end = live ? `now, ${tickLabel(t.end, t.end)}` : tickLabel(t.end, t.end);
  const ticks = { first: tickLabel(t.start, t.end), mid: buckets[6] ? tickLabel(buckets[6].start, t.end) : '', last: end };
  const window = `${tickLabel(t.start, t.end)} to ${end}`;
  const caption = [
    `${formatCount(t.totals.failed)} failed and ${formatCount(t.totals.held)} held for a person, of ${countOf(total, 'change')} from ${window} UTC.`,
    source === 'cluster' ? 'Counted from the changes still in the cluster, which cannot show a missing record: open one Stage to read its signed records.'
      : source === 'status' ? 'Read from the changes still in the cluster: this Stage keeps no signed records, so a missing record cannot be shown.'
      : gaps.length ? `The record is missing in ${countOf(gaps.length, 'slice')}, ${countOf(t.totals.missing, 'record')} in all.` : 'No record is missing in this window.',
    t.untimed ? `${countOf(t.untimed, 'change')} with no time ${t.untimed === 1 ? 'is' : 'are'} not drawn.` : '',
  ].filter(Boolean).join(' ');
  const gapWords = gaps.map((g) => `${g.missing === 1 ? 'one record' : `${formatCount(g.missing)} records`} between ${tickLabel(g.start, t.end)} and ${tickLabel(g.end, t.end)}`);
  return {
    missingKnown, columns, total, gaps, gapWords, ticks, window, caption,
    figRef: missingKnown && gaps.length ? 'A dashed axis with ⊗ under a column marks where the record is missing.' : 'Each column is one twelfth of the window, worst on top.',
    alt: `${window}: ${TL_KINDS.map((k) => `${t.totals[k.key]} ${k.label.toLowerCase()}`).join(', ')}${missingKnown ? `; ${t.totals.missing} records missing` : ''}.`,
  };
}
