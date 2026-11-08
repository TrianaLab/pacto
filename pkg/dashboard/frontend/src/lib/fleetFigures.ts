/**
 * The fleet figures' models (SPEC 7: Overview, Sources, Services, Owners, Domains and
 * the owner and domain pages): pure functions of the backend tallies, so caps, order,
 * partitions and narrations are unit-testable. Every count is a backend tally over the
 * whole population (S3), never a page.
 */
import type { Segment } from './distributions.ts';
import type { FigTone, GlyphName, WireDef, WireStyle } from './figure.ts';
import type { Step } from './narrate.ts';
import { countOf, formatCount } from './format.ts';
import { sharePct } from './figure.ts';

// ─── PostureColumn ────────────────────────────────────────────────────────────

/** Column height for a whole population, the floor of a non-empty stratum and the gap between strata. */
export const POSTURE_H = 150, POSTURE_FLOOR = 5, POSTURE_GAP = 2, POSTURE_W = 44;

export interface PostureStratum { i: number; y: number; h: number; tone: Segment['tone']; hatch: boolean }

/**
 * postureColumn stacks the strata worst on top (the segments arrive in the canonical
 * order), height proportional to share with a 5px floor; an empty stratum draws nothing
 * but keeps its row. Each drawn stratum is wired to its row in its own tone (unknown
 * and other neutral tones on the neutral line).
 */
export function postureColumn(segments: Segment[], total: number) {
  const strata: PostureStratum[] = [];
  let y = 2;
  segments.forEach((s, i) => {
    if (!s.value) return;
    const h = Math.max(POSTURE_FLOOR, Math.round((s.value / Math.max(1, total)) * POSTURE_H));
    strata.push({ i, y, h, tone: s.tone, hatch: !!s.hatch });
    y += h + POSTURE_GAP;
  });
  const wires: WireDef[] = strata.map((s) => ({
    id: `wp${s.i}`, from: `pk${s.i}`, to: `pl${s.i}`, style: 'obs', tone: s.hatch || s.tone === 'info' ? 'neutral' : (s.tone as FigTone),
  }));
  return { strata, H: Math.max(10, y), wires };
}

// ─── Rank figure (Owners, Domains) ───────────────────────────────────────────

export interface RankLane {
  key: string;
  label: string;
  /** The label is an identity: isolated, elided in its middle and titled (C6). */
  ident: boolean;
  /** The entity page the identity links to, beside the count's list link. */
  n: number;
  hatch: boolean;
  glyph: GlyphName | '';
  href: string;
  note: { text: string; href?: string } | null;
}

interface RankOwner { key: string; label: string; services: number; nonCompliant?: number }
interface RankDomain { domain: string; services: number; nonCompliant: number }
export interface RankAggregate {
  services?: number;
  serviceCompliance?: { nonCompliant?: number };
  nonCompliantByOwner?: RankOwner[];
  nonCompliantBeyondRanking?: number;
  nonCompliantUnidentified?: number;
  nonCompliantOwners?: number;
  nonCompliantByDomain?: RankDomain[];
  nonCompliantDomainsBeyond?: number;
  nonCompliantDomains?: number;
  nonCompliantNoDomain?: number;
  nonCompliantDomainUnknown?: number;
}

export interface RankHrefs {
  /** The services a lane counts: an owner key or a domain, '' for no identified owner. */
  services: (group: string) => string;
  /** The groups table sorted by non-compliant services, for "Other". */
  groups: string;
}

/** A group with fewer services than this never names the highest rate: 1 of 1 is no signal. */
export const RATE_MIN_SERVICES = 10;

/**
 * rankModel partitions the non-compliant services (C14, S4): the top groups by count,
 * "Other", then the services no group can name, at most 5 lanes (S2). Lanes are
 * neutral aggregates (S10); the share bars carry the err tone.
 */
export function rankModel(kind: 'owner' | 'domain', agg: RankAggregate | null | undefined, hrefs: RankHrefs) {
  const total = agg?.serviceCompliance?.nonCompliant ?? 0;
  const services = agg?.services ?? 0;
  const word = kind === 'owner' ? 'owner' : 'domain';
  const tails: RankLane[] = [];
  let rows: { key: string; label: string; n: number; size: number }[];
  let groups: number;
  if (kind === 'owner') {
    rows = (agg?.nonCompliantByOwner ?? []).map((o) => ({ key: o.key, label: o.label, n: o.nonCompliant ?? 0, size: o.services }));
    groups = agg?.nonCompliantOwners ?? rows.length;
    // An empty bucket keeps its place and shows 0 (SPEC 2.2).
    const none = agg?.nonCompliantUnidentified ?? 0;
    tails.push({ key: '-none', label: 'No identified owner', ident: false, n: none, hatch: true, glyph: 'unknown', href: hrefs.services(''), note: none ? { text: 'Nobody is accountable for these yet' } : null });
  } else {
    rows = (agg?.nonCompliantByDomain ?? []).map((d) => ({ key: d.domain, label: d.domain, n: d.nonCompliant, size: d.services }));
    groups = agg?.nonCompliantDomains ?? rows.length;
    const none = agg?.nonCompliantNoDomain ?? 0, unknown = agg?.nonCompliantDomainUnknown ?? 0;
    if (none) tails.push({ key: '-none', label: 'No domain', ident: false, n: none, hatch: false, glyph: 'none', href: hrefs.services('(none)'), note: null });
    if (unknown) tails.push({ key: '-unknown', label: 'Domain unknown', ident: false, n: unknown, hatch: true, glyph: 'unknown', href: hrefs.services('(unknown)'), note: null });
  }
  const top = rows.slice(0, Math.max(1, 4 - tails.length));
  const lanes: RankLane[] = top.map((r) => ({
    key: r.key, label: r.label, ident: true, n: r.n, hatch: false, glyph: '', href: hrefs.services(r.key),
    note: { text: `${sharePct(r.n, r.size)} of ${kind === 'owner' ? 'their' : 'its'} ${countOf(r.size, 'service')}` },
  }));
  // Other keeps its place at 0 (SPEC 2.2). Its note links to every group with a
  // non-compliant service, the ranked ones included, and counts exactly that list.
  const other = Math.max(0, total - lanes.reduce((a, l) => a + l.n, 0) - tails.reduce((a, l) => a + l.n, 0));
  if (lanes.length) {
    lanes.push({ key: '-other', label: `Other ${word}s`, ident: false, n: other, hatch: false, glyph: '', href: '',
      note: groups ? { text: `${countOf(groups, word)} with at least one`, href: hrefs.groups } : null });
  }
  lanes.push(...tails);
  const rated = rows.filter((r) => r.size >= RATE_MIN_SERVICES).sort((a, b) => b.n / b.size - a.n / a.size || a.label.localeCompare(b.label))[0];
  const rate = rated ? { label: rated.label, text: `${sharePct(rated.n, rated.size)} of ${countOf(rated.size, 'service')}` } : null;
  const wires: WireDef[] = lanes.map((l, k) => ({ id: `wr${k}`, from: 'hub', to: `ln-${k}`, style: 'obs', tone: 'neutral', col: { from: 'hub-n', fs: 'b', ts: 'l' } }));
  const alt = `${countOf(total, 'non-compliant service')} of ${formatCount(services)}: ${lanes.map((l) => `${l.label} ${formatCount(l.n)}`).join(', ')}.`;
  return { total, services, lanes, rate, wires, alt, word };
}

// ─── Trust figures (Overview, Sources) ────────────────────────────────────────

export type SourceState = 'unavailable' | 'stale' | 'partial' | 'available';
/** Each state's node, wire and words (SPEC 2.1): unavailable is missing, stale and partial dotted. */
export const SOURCE_STATE: Record<SourceState, { tone: FigTone; glyph: GlyphName; style: WireStyle; word: string; sub: string }> = {
  unavailable: { tone: 'err', glyph: 'err', style: 'broken', word: 'unavailable', sub: 'sent no report' },
  stale: { tone: 'warn', glyph: 'warn', style: 'stale', word: 'stale', sub: 'last report is out of date' },
  partial: { tone: 'warn', glyph: 'warn', style: 'stale', word: 'partial', sub: 'answered only in part' },
  available: { tone: 'ok', glyph: 'ok', style: 'obs', word: 'current', sub: 'reported recently' },
};
const STATE_ORDER: SourceState[] = ['unavailable', 'stale', 'partial', 'available'];

export interface SourceCountsLike { total?: number; available?: number; partial?: number; stale?: number; unavailable?: number; availableByKind?: { kind: string; count: number }[] }

const KIND_NOUN: Record<string, [string, string]> = {
  oci: ['OCI registry', 'OCI registries'], kubernetes: ['Kubernetes cluster', 'Kubernetes clusters'],
  'evidence-http': ['evidence server', 'evidence servers'], local: ['local directory', 'local directories'],
  cache: ['bundle cache', 'bundle caches'], observation: ['observation file', 'observation files'],
  'target-state': ['target-state file', 'target-state files'], mira: ['Mira source', 'Mira sources'],
};

/** sourceKindNoun names n sources of one kind ("Kubernetes clusters"); '' is the kinds folded together. */
export function sourceKindNoun(kind: string, n: number): string {
  const [one, many] = KIND_NOUN[kind] ?? (kind ? [`${kind} source`, `${kind} sources`] : ['source of other kinds', 'sources of other kinds']);
  return n === 1 ? one : many;
}

/**
 * currentByKind splits the current sources into one tally per kind, most first, in at
 * most `room` rows: the kinds past it fold into one row ('' kind). With no split, one
 * that does not add up or room for one row only, it is one tally of the current sources.
 */
function currentByKind(n: number, byKind: SourceCountsLike['availableByKind'], room: number): { st: SourceState; n: number; kind?: string }[] {
  if (n <= 0) return [];
  const kinds = (byKind ?? []).filter((k) => k.count > 0);
  if (!kinds.length || kinds.reduce((a, k) => a + k.count, 0) !== n || room <= 1) return [{ st: 'available', n }];
  const keep = kinds.length <= room ? kinds : kinds.slice(0, room - 1);
  const rest = n - keep.reduce((a, k) => a + k.count, 0);
  return [...keep.map((k) => ({ st: 'available' as SourceState, n: k.count, kind: k.kind })), ...(rest ? [{ st: 'available' as SourceState, n: rest, kind: '' }] : [])];
}

/** The wire a source row sends to the bus: stale and partial in warn, the rest on the neutral line. */
function srcWire(k: number, st: SourceState): WireDef {
  return { id: `ws${k}`, from: `s${k}`, to: `in${k}`, style: SOURCE_STATE[st].style, tone: st === 'stale' || st === 'partial' ? 'warn' : 'neutral' };
}
const BUS_WIRE: WireDef = { id: 'wbus', from: 'bus', to: 'snap', style: 'obs', tone: 'accent', col: { fs: 'b', ts: 't' } };

/**
 * passiveSteps is the passive narration (A2 r5): only when a source is unavailable or
 * stale. The unavailable marks and their ✕ are revealed, then the stale wires carry the
 * one signal; current wires and the snapshot never move, and no signal runs on a broken
 * wire (A6). Partial coverage alone yields nothing.
 */
function passiveSteps(rows: { k: number; st: SourceState }[], counts: SourceCountsLike): Step[] {
  const steps: Step[] = [];
  const un = rows.filter((r) => r.st === 'unavailable'), stale = rows.filter((r) => r.st === 'stale');
  if (un.length) steps.push({ cap: `No report from ${countOf(counts.unavailable ?? 0, 'source')}: unavailable.`, dur: 160, show: un.flatMap((r) => [`[data-m="s${r.k}"]`, `w:ws${r.k}`]) });
  if (stale.length) steps.push({ cap: `${countOf(counts.stale ?? 0, 'source')} last reported too long ago: stale.`, dur: 240, sig: stale.map((r) => `ws${r.k}`) });
  return steps;
}

/** trustModel is the Overview figure: one row per source state with sources in it (unavailable, stale and current always). */
export function trustModel(counts: SourceCountsLike | null | undefined) {
  const c = counts ?? {};
  const rows = STATE_ORDER.filter((st) => st !== 'partial' || (c.partial ?? 0) > 0)
    .map((st, k) => ({ k, st, n: c[st] ?? 0 }));
  const wires = [...rows.filter((r) => r.n).map((r) => srcWire(r.k, r.st)), BUS_WIRE];
  const steps = passiveSteps(rows.filter((r) => r.n), c);
  const partial = rows.some((r) => r.st !== 'available' && r.n);
  const alt = `${countOf(c.total ?? 0, 'source')} feed the snapshot: ${rows.map((r) => `${formatCount(r.n)} ${SOURCE_STATE[r.st].word}`).join(', ')}.`;
  return { rows, wires, steps, partial, alt, key: { un: c.unavailable ?? 0, st: c.stale ?? 0 } };
}

export interface NamedSource { key: string; label: string; href?: string; secondary?: string; status?: string }

/** At most this many rows, named sources and tallies together (S2). */
export const SOURCE_ROWS = 8;
/** Named non-current sources, at most (S2 r5). */
export const SOURCE_NAMED = 5;

const stateOf = (status: string | undefined): SourceState =>
  status === 'unavailable' || status === 'stale' || status === 'partial' ? status : 'available';

/**
 * sourcesModel is the Sources figure (r5): the sources that are not current are named
 * first (the backend's least-healthy-first order, at most five, fewer when needed to
 * keep eight rows); the non-current ones not named go into one tally per state, wired
 * in that state's style; the current ones into one tally. Every wire's style matches
 * every source behind it. A small fleet names every source.
 */
export function sourcesModel(worst: NamedSource[], counts: SourceCountsLike | null | undefined) {
  const c = counts ?? {};
  const total = c.total ?? worst.length;
  type Row = { k: number; st: SourceState; named?: NamedSource; n: number; kind?: string };
  let rows: Row[];
  if (total <= SOURCE_ROWS && worst.length >= total) {
    rows = worst.map((s, k) => ({ k, st: stateOf(s.status), named: s, n: 1 }));
  } else {
    const bad = worst.filter((s) => stateOf(s.status) !== 'available');
    let k = Math.min(SOURCE_NAMED, bad.length);
    // Non-current sources not named: one tally per state. Current ones: one tally
    // per kind (r5), from the backend's split of the whole population.
    const tallies = (named: number): Omit<Row, 'k'>[] => {
      const left = (st: SourceState) => (c[st] ?? 0) - bad.slice(0, named).filter((s) => stateOf(s.status) === st).length;
      const states = STATE_ORDER.filter((st) => st !== 'available' && left(st) > 0).map((st) => ({ st, n: left(st) }));
      return [...states, ...currentByKind(left('available'), c.availableByKind, SOURCE_ROWS - named - states.length)];
    };
    // Names give way to rows (at most eight) and to the kind tallies: one name fewer
    // beats folding every current source into one row that names no kind.
    const kinds = (c.availableByKind ?? []).filter((x) => x.count > 0).length;
    const lumped = (named: number) => kinds > 1 && tallies(named).some((r) => r.st === 'available' && r.kind == null);
    while (k > 0 && (k + tallies(k).length > SOURCE_ROWS || (k > 1 && lumped(k)))) k--;
    rows = [...bad.slice(0, k).map((s) => ({ st: stateOf(s.status), named: s, n: 1 })), ...tallies(k)].map((r, i) => ({ ...r, k: i }));
  }
  const named = rows.filter((r) => r.named).length;
  const wires = [...rows.map((r) => srcWire(r.k, r.st)), BUS_WIRE];
  const steps = passiveSteps(rows, c);
  const partial = (c.unavailable ?? 0) + (c.stale ?? 0) + (c.partial ?? 0) > 0;
  const alt = `${countOf(total, 'source')} feed the snapshot: ${STATE_ORDER.map((st) => `${formatCount(c[st] ?? 0)} ${SOURCE_STATE[st].word}`).join(', ')}.`;
  const tallyLabel = (r: Row) => `${formatCount(r.n)}${named ? ' more' : ''} ${r.kind != null ? sourceKindNoun(r.kind, r.n) : SOURCE_STATE[r.st].word}`;
  return {
    rows, wires, steps, partial, alt, tallyLabel,
    key: rows.filter((r) => r.named && r.st !== 'available').map((r) => `${r.named!.key}:${r.st}`),
  };
}
