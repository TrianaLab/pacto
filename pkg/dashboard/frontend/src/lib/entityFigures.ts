/**
 * The entity pages' figure models (SPEC 7, P4): pure functions from the entity detail
 * the server returned to the marks, rows and wires a figure draws. Every count comes
 * from a backend total or tally, never from counting a bounded preview, and nothing is
 * drawn that the record does not assert.
 */
import type { FigTone, GlyphName, WireDef, WireStyle } from './figure.ts';
import { MAX_STACK_LAYERS, sharePct } from './figure.ts';
import type { Step } from './narrate.ts';
import { stageName } from './behavior.ts';
import { countOf, formatCount } from './format.ts';

type Ref = { key?: string; label?: string; href?: string };

export interface RailStation {
  key: string; label: string; sub: string; tone: FigTone; hatch?: boolean; glyph?: GlyphName | ''; reached: boolean;
}

/** One link of a chain of stations `f0`..`f{n-1}`, left to right, top to bottom in a column. */
const chainWire = (k: number, style: WireStyle, head = true): WireDef => ({
  id: `wf${k}`, from: `f${k}`, fs: 'r', to: `f${k + 1}`, ts: 'l', col: { fs: 'b', ts: 't' }, style, tone: 'neutral', head,
});

// ── Revision: where did validation stop? ───────────────────────────────────────────

type Stage = { name: string; status: string; reason?: string; unverified?: boolean };

function stageStation(st: Stage): RailStation {
  const label = stageName(st.name);
  const base = { key: st.name, label, reached: true };
  if (st.status === 'pass') return { ...base, tone: 'ok', glyph: 'ok', sub: 'passed' };
  if (st.status === 'fail') return { ...base, tone: 'err', glyph: 'err', sub: st.reason ? `failed: ${st.reason}` : 'failed' };
  if (st.unverified) return { ...base, tone: 'neutral', hatch: true, glyph: 'unknown', sub: st.reason ? `not verified: ${st.reason}` : 'not verified' };
  return { ...base, tone: 'neutral', hatch: true, glyph: '', sub: st.reason ? `not run: ${st.reason}` : 'not run' };
}

/**
 * validationModel is the revision's validation Rail: one station per stage in run order,
 * a not-run stage drawn hatched (not run is never a pass), the wire into a stage that did
 * not run dashed. The walk takes one step per station, not-run ones included (A1b), and
 * plays on its own only when a stage failed (A2).
 */
export function validationModel(stages: Stage[]) {
  const stations = stages.map(stageStation);
  const ran = (s: Stage) => s.status === 'pass' || s.status === 'fail';
  const wires = stations.slice(1).map((_, k) => chainWire(k, ran(stages[k + 1]) ? 'obs' : 'proj'));
  const steps: Step[] = stations.map((s, k) => ({
    cap: `${s.label}: ${s.sub}.`, dur: 120, hold: 30,
    show: [`[data-m="f${k}"]`, ...(k ? [`w:wf${k - 1}`] : [])], sig: k && wires[k - 1].style === 'obs' ? [`wf${k - 1}`] : [],
  }));
  const failed = stages.findIndex((s) => s.status === 'fail');
  const notRun = stages.filter((s) => !ran(s)).length;
  const allPassed = stages.length > 0 && stages.every((s) => s.status === 'pass');
  const summary = allPassed ? `Every check passed: ${countOf(stages.length, 'check')}.`
    : failed >= 0 ? `Validation stopped at ${stations[failed].label} (${stations[failed].sub}).${notRun ? ` ${countOf(notRun, 'check')} did not run or could not be verified; that is never a pass.` : ''}`
      : `${countOf(notRun, 'check')} did not run or could not be verified; that is never a pass.`;
  return {
    stations, wires, steps, summary,
    question: allPassed ? 'Did this revision pass every check?' : 'Where did validation stop?',
    autoplay: failed >= 0,
    key: { rail: stages.map((s) => `${s.name}:${s.status}`) },
    alt: stations.map((s) => `${s.label} ${s.sub}`).join(', '),
  };
}

// ── Revisions as a Stack ───────────────────────────────────────────────────────────

export interface StackRow { a: string; ref: Ref | null; label: string; note: string; dash?: boolean; current?: boolean }

/** Wires from each layer's right corner to its row, and from the plinth to its row. */
const rowWires = (rows: { a: string; dash?: boolean }[]): WireDef[] => rows.map((r) => ({
  id: `w${r.a}`, from: r.a, fs: 'c', to: `l${r.a}`, ts: 'l', style: r.dash ? 'proj' : 'obs', tone: 'neutral',
}));

/**
 * serviceStackModel draws a service's revisions newest first (Revisions is the backend's
 * chronological preview): up to five layers, the current one in accent, and every older
 * revision folded into one plinth whose row links to the full history.
 */
export function serviceStackModel(revisions: { total?: number; items?: Ref[] } | undefined, currentKey: string, label: (r: Ref) => string) {
  const items = revisions?.items ?? [];
  const total = revisions?.total ?? items.length;
  const shown = items.slice(0, MAX_STACK_LAYERS);
  const earlier = Math.max(0, total - shown.length);
  const rows: StackRow[] = shown.map((r, k) => ({
    a: `r${k}`, ref: r, label: label(r), current: r.key === currentKey,
    note: r.key === currentKey ? 'current' : k === 0 ? 'newest' : '',
  }));
  const wires = rowWires(rows);
  if (earlier) wires.push({ id: 'wplinth', from: 'plinth', fs: 'c', to: 'lplinth', ts: 'l', style: 'obs', tone: 'neutral' });
  const newest = rows[0]?.label ?? '';
  const oldest = total === items.length && items.length ? label(items[items.length - 1]) : '';
  return {
    rows, earlier, total, wires, newest, oldest,
    layers: rows.map((r) => ({ tone: r.current ? 'accent' : 'neutral', a: r.a, m: r.a })),
    alt: `${countOf(total, 'revision')}${newest ? `; the newest is ${newest}` : ''}${earlier ? `; ${formatCount(earlier)} earlier ones on the plinth` : ''}.`,
  };
}

/**
 * revisionStackModel draws one revision between its neighbours: the next published
 * revision above (dashed), this one in accent, the previous one below (dashed). The
 * neighbours are the engine's adjacency; a revision without one draws none.
 */
export function revisionStackModel(self: Ref & { version?: string }, prev: Ref | null | undefined, next: Ref | null | undefined, label: (r: Ref) => string) {
  const rows: StackRow[] = [
    ...(next ? [{ a: 'n', ref: next, label: label(next), note: 'next', dash: true }] : []),
    { a: 'c', ref: null, label: self.version || label(self), note: 'this revision', current: true },
    ...(prev ? [{ a: 'pv', ref: prev, label: label(prev), note: 'previous', dash: true }] : []),
  ];
  return {
    rows, wires: rowWires(rows),
    layers: rows.map((r) => ({ tone: r.current ? 'accent' : 'neutral', dash: !!r.dash, a: r.a, m: r.a })),
    summary: `${prev ? `Previous ${label(prev)}.` : 'No previous revision.'} ${next ? `Next ${label(next)}.` : 'No newer revision.'}`,
    alt: rows.map((r) => `${r.note} ${r.label}`).join(', '),
  };
}

// ── Target: where it runs, and on what link ────────────────────────────────────────

/** SPEC 2.1: an exact match is observed, an inferred one projected, an ambiguous one dotted, none missing. */
export const LINK_WIRE: Record<string, WireStyle> = { exact: 'obs', inferred: 'proj', ambiguous: 'stale', unresolved: 'broken' };
const LINK_WORDS: Record<string, string> = {
  exact: 'Its digest matches this revision: the link is observed.',
  inferred: 'Matched by name and version only: the link is inferred.',
  ambiguous: 'More than one revision matches: the link is ambiguous.',
  unresolved: 'No known revision matches: the link is missing.',
};

export function targetModel(linkState: string | undefined) {
  const style = LINK_WIRE[linkState ?? ''] ?? 'broken';
  return {
    style,
    words: LINK_WORDS[linkState ?? ''] ?? LINK_WORDS.unresolved,
    wires: [{ id: 'wlink', from: 'plinth', fs: 'c', to: 'rev', ts: 'l', col: { fs: 'b', ts: 't' }, style, tone: style === 'stale' ? 'warn' : 'neutral' } as WireDef],
  };
}

// ── Source: what it reports, and is it current ─────────────────────────────────────

const SOURCE_MARK: Record<string, { tone: FigTone; hatch?: boolean; glyph: GlyphName; style: WireStyle; words: string }> = {
  available: { tone: 'ok', glyph: 'ok', style: 'obs', words: 'current' },
  stale: { tone: 'warn', glyph: 'warn', style: 'stale', words: 'stale' },
  partial: { tone: 'neutral', hatch: true, glyph: 'unknown', style: 'stale', words: 'answered in part' },
  unavailable: { tone: 'err', glyph: 'err', style: 'broken', words: 'unavailable' },
};

export function sourceModel(health: string | undefined) {
  const m = SOURCE_MARK[health ?? ''] ?? { tone: 'neutral' as FigTone, hatch: true, glyph: 'unknown' as GlyphName, style: 'stale' as WireStyle, words: 'in an unknown state' };
  return {
    ...m,
    wires: [{ id: 'wsrc', from: 'src', fs: 'r', to: 'snap', ts: 'l', col: { fs: 'b', ts: 't' }, style: m.style, tone: m.tone === 'warn' ? 'warn' : 'neutral' } as WireDef],
  };
}

// ── Offering: who provides it, who uses it, and is their request ready ─────────────

export interface PhaseTally { requested?: number; provisioning?: number; ready?: number; failed?: number; deleting?: number; unknown?: number }

export interface PhaseStation { key: string; label: string; sub: string; tone: FigTone; hatch?: boolean; glyph: GlyphName | ''; n: number; share: string }

const PHASES: [keyof PhaseTally, string, string, FigTone, GlyphName | '', boolean?][] = [
  ['requested', 'Requested', 'asked for, not started', 'info', 'progress'],
  ['provisioning', 'Provisioning', 'being created', 'info', 'progress'],
  ['ready', 'Ready', 'in use', 'ok', 'ok'],
  ['deleting', 'Deleting', 'being removed', 'info', 'none'],
  ['failed', 'Failed', 'did not become ready', 'err', 'err'],
  ['unknown', 'Not reported', 'no phase a provider reported can be trusted, so readiness is unknown', 'neutral', 'unknown', true],
];

/** The provider marks: up to two named, or one and a "+N" count when there are more. */
export const MAX_PROVIDER_MARKS = 2;

/**
 * offeringModel draws the providers wired into the offering, and the offering's
 * requests (one per use, environment and entry) on the phase track: where they are now,
 * never how they moved, because providers report only the current phase (A6). A phase
 * with no request keeps its station, neutral, with 0.
 */
export function offeringModel(providers: { total?: number; items?: Ref[] }, phases: PhaseTally | undefined) {
  const items = providers.items ?? [];
  const total = providers.total ?? items.length;
  const named = total > MAX_PROVIDER_MARKS ? items.slice(0, MAX_PROVIDER_MARKS - 1) : items.slice(0, MAX_PROVIDER_MARKS);
  const more = total - named.length;
  const requests = PHASES.reduce((n, [k]) => n + (phases?.[k] ?? 0), 0);
  const stations: PhaseStation[] = PHASES.map(([key, label, sub, tone, glyph, hatch]) => {
    const n = phases?.[key] ?? 0;
    return { key, label, sub, n, share: sharePct(n, requests), tone: n ? tone : 'neutral', hatch: hatch && n > 0, glyph: n ? glyph : '' };
  });
  const marks = [...named.map((_, k) => `p${k}`), ...(more ? ['pm'] : [])];
  const notCol: WireDef['when'] = ['wide', 'mid', 'narrow'];
  const wires: WireDef[] = [
    // Providers fan into the offering from above; the vertical layout lists them instead.
    ...marks.map((a): WireDef => ({ id: `w${a}`, from: a, fs: 'b', to: 'hub', ts: 't', style: 'obs', tone: 'neutral', when: notCol })),
    // The requests belong to the offering: no arrowhead from the hub (C13b).
    { id: 'whub', from: 'hub', fs: 'r', to: 'ph-requested', ts: 'l', col: { fs: 'b', ts: 't' }, style: 'obs', tone: 'accent' },
    ...(['requested', 'provisioning', 'ready'] as const).map((k, i): WireDef => ({
      id: `wt${i}`, from: `ph-${k}`, fs: 'r', to: `ph-${PHASES[i + 1][0]}`, ts: 'l', col: { fs: 'b', ts: 't' }, style: 'obs', tone: 'neutral', head: true,
    })),
    { id: 'wfail', from: 'ph-provisioning-b', fs: 'c', to: 'ph-failed', ts: 't', style: 'obs', tone: 'err', head: true, when: notCol },
  ];
  const n = (k: keyof PhaseTally) => phases?.[k] ?? 0;
  const progress = n('requested') + n('provisioning');
  const summary = requests
    ? `Of ${countOf(requests, 'request')}, ${formatCount(n('ready'))} ${n('ready') === 1 ? 'is' : 'are'} ready, ${formatCount(progress)} in progress, ${formatCount(n('failed'))} failed and ${formatCount(n('deleting'))} being removed; ${formatCount(n('unknown'))} ${n('unknown') === 1 ? 'has' : 'have'} no reported phase, so their readiness is unknown.`
    : 'No consumer has asked a provider for a resource yet.';
  return {
    named, more, total, requests, stations, wires, summary,
    alt: `${countOf(total, 'provider')}; ${summary}`,
  };
}
