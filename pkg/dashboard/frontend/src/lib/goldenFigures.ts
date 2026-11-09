/**
 * The golden path figures' models (SPEC 7: Golden paths list and golden path entity,
 * P6): pure functions of the backend's origin tally (B5) and the creation checklist,
 * so the partition, order and tones are unit-testable. Every count is a backend tally
 * over the whole population (S3). Hidden services are counted and never named, and sit
 * in no total.
 */
import type { GlyphName, WireDef } from './figure.ts';
import type { Step } from './narrate.ts';
import type { ExplainStation } from './explainers.ts';
import { countOf, formatCount } from './format.ts';

// ─── Origins: which golden paths created the fleet ────────────────────────────

interface OriginCount { services?: number; nonCompliant?: number }
interface PathCount { key: string; label: string; services: number; nonCompliant: number; hidden?: number }
export interface OriginAggregate {
  services?: number;
  origins?: { byPath?: PathCount[]; paths?: number; beyond?: OriginCount; unresolved?: OriginCount; none?: OriginCount; hidden?: number };
}

export interface OriginHrefs {
  /** The services a lane counts: a golden path key, '(none)' or '(unresolved)'. */
  services: (bucket: string) => string;
  /** The golden paths table ranked by services created, for the CountTile. */
  paths: string;
}

export interface OriginLane {
  key: string;
  label: string;
  ident: boolean;
  n: number;
  hatch: boolean;
  glyph: GlyphName | '';
  href: string;
  note: { text: string; href?: string } | null;
}

/** Lanes per figure (S2): the top paths, then Other, Origin not resolved and None declared. */
export const ORIGIN_LANES = 5;

const ncNote = (c: OriginCount | undefined) => `${formatCount(c?.nonCompliant ?? 0)} of ${formatCount(c?.services ?? 0)} non-compliant`;

/**
 * originModel partitions the fleet's services by the golden path they were created from
 * (C14, S4): the top paths by services created, "Other paths", then the services whose
 * origin resolves to no golden path and those that declare none, at most five lanes.
 * Every lane, Other and None declared included, says how many of its services are
 * non-compliant, of its own count. Lanes are neutral (S10); none is a status.
 */
export function originModel(agg: OriginAggregate | null | undefined, hrefs: OriginHrefs) {
  const o = agg?.origins ?? {};
  const total = agg?.services ?? 0;
  const tails: OriginLane[] = [];
  const unresolved = o.unresolved?.services ?? 0;
  if (unresolved) {
    tails.push({ key: '-unresolved', label: 'Origin not resolved', ident: false, n: unresolved, hatch: true, glyph: 'unknown', href: hrefs.services('(unresolved)'),
      note: { text: `${ncNote(o.unresolved)}; the golden path they name is not one you can see` } });
  }
  // None declared keeps its place at 0 (SPEC 2.2).
  tails.push({ key: '-none', label: 'None declared', ident: false, n: o.none?.services ?? 0, hatch: true, glyph: 'none', href: hrefs.services('(none)'), note: { text: ncNote(o.none) } });
  const rows = o.byPath ?? [];
  const top = rows.slice(0, ORIGIN_LANES - 1 - tails.length);
  const lanes: OriginLane[] = top.map((p) => ({
    key: p.key, label: p.label, ident: true, n: p.services, hatch: false, glyph: '', href: hrefs.services(p.key),
    note: { text: ncNote(p) + (p.hidden ? `; ${formatCount(p.hidden)} more you may not see` : '') },
  }));
  if (rows.length) {
    const rest = rows.slice(top.length);
    const other: OriginCount = {
      services: (o.beyond?.services ?? 0) + rest.reduce((a, p) => a + p.services, 0),
      nonCompliant: (o.beyond?.nonCompliant ?? 0) + rest.reduce((a, p) => a + p.nonCompliant, 0),
    };
    // No filter lists "every other path's services", so the count is plain; the figure's
    // CountTile carries the link to the golden paths it does not draw (one carrier).
    lanes.push({ key: '-other', label: 'Other paths', ident: false, n: other.services ?? 0, hatch: false, glyph: '', href: '', note: { text: ncNote(other) } });
  }
  lanes.push(...tails);
  const created = rows.reduce((a, p) => a + p.services, 0) + (o.beyond?.services ?? 0);
  const wires: WireDef[] = lanes.map((l, k) => ({
    id: `wo${k}`, from: `lane-${k}`, fs: 'r', to: 'hub', ts: 'l', style: 'obs', tone: 'neutral', col: { from: `ln-${k}`, fs: 'l', to: 'hub-n', ts: 'b' },
  }));
  const alt = `${countOf(total, 'service')}: ${lanes.map((l) => `${l.label} ${formatCount(l.n)}`).join(', ')}.`;
  return { total, created, hidden: o.hidden ?? 0, paths: o.paths ?? 0, more: Math.max(0, (o.paths ?? rows.length) - top.length), lanes, wires, alt };
}

// ─── "What does creating a service involve?" (explainer) ──────────────────────

/**
 * creationExplainModel draws the mechanism of creating a service from a golden path
 * (SPEC 7, golden path entity): golden path → parameters → contract → check → first
 * revision. An explainer: neutral and accent only, no states, no fleet data. `requires`
 * names what the path consumes on a golden path page ([] says it requests none) and
 * `hidden` counts requirements the reader may not see, never named; null leaves the
 * last station general (the list page). A rail walk (A1b) on Replay only.
 */
export function creationExplainModel(requires: string[] | null, hidden = 0) {
  const asked = [...(requires ?? []), ...(hidden ? [countOf(hidden, 'requirement') + ' you may not see'] : [])];
  const last = requires === null
    ? 'The first revision is ready to publish, with any offerings the path requests'
    : asked.length ? `It also requests ${asked.length > 1 ? `${asked.slice(0, -1).join(', ')} and ${asked[asked.length - 1]}` : asked[0]}` : 'This path requests no offering';
  const stations: ExplainStation[] = [
    { key: 'path', label: 'Golden path', sub: 'A contract a platform team publishes to start from', tone: 'accent', shape: 'square', reached: true },
    { key: 'params', label: 'You fill in parameters', sub: 'A name, an owner and what the path asks for', tone: 'neutral', shape: 'circle', reached: true },
    { key: 'contract', label: 'Pacto writes the contract', sub: 'It records the golden path as the service origin, by digest', tone: 'neutral', shape: 'square', reached: true },
    { key: 'check', label: 'It is checked', sub: 'The contract is validated before anything is written', tone: 'neutral', shape: 'diamond', reached: true },
    { key: 'revision', label: 'First revision', sub: last, tone: 'accent', shape: 'square', reached: true },
  ];
  const wires: WireDef[] = stations.slice(1).map((_, k) => ({
    id: `wc${k}`, from: `f${k}`, fs: 'r', to: `f${k + 1}`, ts: 'l', col: { fs: 'b', ts: 't' }, style: 'obs', tone: 'accent', head: true,
  }));
  const steps: Step[] = stations.map((s, k) => ({
    cap: `${s.label}: ${s.sub}.`, dur: 120, hold: 30,
    show: [`[data-m="f${k}"]`, ...(k ? [`w:wc${k - 1}`] : [])], sig: k ? [`wc${k - 1}`] : [],
  }));
  return { stations, wires, steps, alt: `A golden path, your parameters, a written contract, a check, then the first revision. ${last}.` };
}

// ─── Creation status (data rail) ──────────────────────────────────────────────

export interface CreationStep { step: string; name?: string; status: string; detail?: string }

const STEP_LABEL: Record<string, string> = {
  signature: 'Signature', contract: 'Contract', validation: 'Validation', plugin: 'Plugins', delivery: 'Delivery',
  published: 'Published', fleet: 'Seen by Pacto', deployed: 'Deployed', provisioning: 'Provisioning',
};

/**
 * Each checklist status: its word, its tone and its glyph. Unknown is the grey hatch,
 * never a status tone and never ok (SPEC 2.2); not applicable is a neutral dash, so
 * "none needed" never reads as missing.
 */
export const CREATION_STATUS: Record<string, { word: string; tone: string; hatch: boolean; glyph: GlyphName | '' }> = {
  done: { word: 'Done', tone: 'ok', hatch: false, glyph: 'ok' },
  failed: { word: 'Failed', tone: 'err', hatch: false, glyph: 'err' },
  unknown: { word: 'Unknown', tone: 'neutral', hatch: true, glyph: 'unknown' },
  planned: { word: 'Planned', tone: 'neutral', hatch: false, glyph: 'progress' },
  skipped: { word: 'Skipped', tone: 'neutral', hatch: false, glyph: 'none' },
  'not-applicable': { word: 'Not applicable', tone: 'neutral', hatch: false, glyph: 'none' },
};
export const creationStatus = (s: string) => CREATION_STATUS[s] ?? CREATION_STATUS.unknown;

/** fold is the status of several plugin steps: any failed fails, all done is done, else unknown. */
function fold(steps: CreationStep[]): string {
  if (steps.some((s) => s.status === 'failed')) return 'failed';
  return steps.every((s) => s.status === 'done') ? 'done' : 'unknown';
}

/**
 * creationRailModel is the creation checklist of a service with an origin as a Rail
 * (A1b: at most 8 stations): the plugin steps fold into one Plugins station. A step is
 * done only on evidence; anything Pacto cannot observe stays Unknown, hatched.
 */
export function creationRailModel(steps: CreationStep[]) {
  const groups: CreationStep[][] = [];
  for (const s of steps) {
    const prev = groups[groups.length - 1];
    if (s.step === 'plugin' && prev?.[0].step === 'plugin') prev.push(s);
    else groups.push([s]);
  }
  const stations = groups.map((g, i) => {
    const status = g[0].step === 'plugin' ? fold(g) : g[0].status;
    const st = creationStatus(status);
    const sub = g[0].step === 'plugin'
      ? `${countOf(g.length, 'plugin')} (${g.map((s) => s.name).filter(Boolean).join(', ')}): ${g[0].detail ?? ''}`
      : g[0].detail ?? '';
    return { key: `${g[0].step}-${i}`, label: `${STEP_LABEL[g[0].step] ?? g[0].step} · ${st.word}`, sub, tone: st.tone, hatch: st.hatch, glyph: st.glyph, reached: true, status };
  });
  const wires: WireDef[] = stations.slice(1).map((_, k) => ({
    id: `wk${k}`, from: `f${k}`, fs: 'r', to: `f${k + 1}`, ts: 'l', col: { fs: 'b', ts: 't' }, style: 'obs', tone: 'neutral',
  }));
  const counts = (st: string) => stations.filter((s) => s.status === st).length;
  const alt = `${countOf(stations.length, 'step')}: ${counts('done')} done, ${counts('unknown')} unknown, ${counts('failed')} failed.`;
  return { stations, wires, alt };
}
