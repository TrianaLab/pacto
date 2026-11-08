/**
 * The Delivery Environments figures' pure models (SPEC 7, SPEC 21).
 *
 * The fleet figure answers "Where do the services stand on their way to production?":
 * one flat neutral Station per environment (S10), in the promotion order the Stages
 * declare (counts.byEnvironment, never the names), each with its Stages counted by
 * state on one share bar and a count list that links to the board filtered by state and
 * environment (T3). An arrow joins two neighbouring stations only when the Stages of the
 * second are fed by the first: across services there is no one order.
 *
 * The service figure answers "Where is X on its way to production?" from that service's
 * own promotion graph: a staircase for a chain, a fork for a branch, a source marker for
 * an entry fed by artifacts or another cluster's records, and a hatched marker with the
 * reason for a Stage whose source is missing or undeclared (never drawn as a link).
 */
import type { DeliveryEnvironments } from './api.ts';
import { countOf, formatCount } from './format.ts';
import { andList } from './delivery.ts';
import type { FigTone, GlyphName, WireDef } from './figure.ts';
import type { Step } from './narrate.ts';

type Counts = NonNullable<DeliveryEnvironments['counts']['byEnvironment']>[number];
type Row = NonNullable<DeliveryEnvironments['environments']>[number];
type Graph = NonNullable<DeliveryEnvironments['promotion']>;
type GNode = NonNullable<Graph['nodes']>[number];

export type EnvState = 'failed' | 'waiting' | 'autohold' | 'frozen' | 'unverified' | 'empty' | 'verified';
/** Canonical order (SPEC 2.2): err, warn, frozen (information), unknown, then the rest. */
export const ENV_STATES: { key: EnvState; field: keyof Counts; tone: FigTone; hatch?: boolean; glyph: GlyphName; word: string; stamp?: string }[] = [
  { key: 'failed', field: 'failed', tone: 'err', glyph: 'err', word: 'failed', stamp: 'Failed' },
  { key: 'waiting', field: 'waiting', tone: 'warn', glyph: 'held', word: 'waiting for a person', stamp: 'Held' },
  { key: 'autohold', field: 'autoHold', tone: 'warn', glyph: 'held', word: 'on auto-hold', stamp: 'Auto-hold' },
  { key: 'frozen', field: 'frozen', tone: 'frost', glyph: 'frozen', word: 'frozen', stamp: 'Frozen' },
  { key: 'unverified', field: 'unverified', tone: 'neutral', hatch: true, glyph: 'unknown', word: 'not verified' },
  { key: 'empty', field: 'empty', tone: 'neutral', glyph: 'none', word: 'running nothing yet' },
  { key: 'verified', field: 'verified', tone: 'ok', glyph: 'ok', word: 'verified' },
];

/** The one state a board row is counted in, as the server's stageState decides it. */
export function rowState(r: Row): (typeof ENV_STATES)[number] {
  const key: EnvState = r.failed ? 'failed' : r.frozen ? 'frozen' : r.autoHold ? 'autohold' : r.held > 0 ? 'waiting'
    : !r.pointer ? 'empty' : !r.verified ? 'unverified' : 'verified';
  return ENV_STATES.find((s) => s.key === key)!;
}

/** Stations per fleet figure: 7 and their 6 arrows stay under 20 marks (S2); the eighth is "N more". */
export const MAX_ENV_STATIONS = 7;

export interface EnvStation {
  key: string;
  label: string;
  /** The environment its counts filter the board to; '' for the folded station. */
  env: string;
  /** The folded station's filter: every environment but these (the ones drawn). */
  except?: string[];
  tier: string;
  stages: number;
  counts: { s: (typeof ENV_STATES)[number]; n: number }[];
}

export function fleetEnvModel(byEnv: Counts[]) {
  let envs = byEnv;
  let rest: Counts[] = [];
  const worst = (e: Counts) => [e.failed, e.waiting + e.autoHold, e.frozen];
  if (envs.length > MAX_ENV_STATIONS) {
    const keep = new Set([...envs].sort((a, b) => { const x = worst(a), y = worst(b); return y[0] - x[0] || y[1] - x[1] || y[2] - x[2] || b.stages - a.stages; })
      .slice(0, MAX_ENV_STATIONS - 1));
    rest = envs.filter((e) => !keep.has(e));
    envs = envs.filter((e) => keep.has(e));
  }
  const counted = (cs: Counts[]) => ENV_STATES.map((s) => ({ s, n: cs.reduce((n, e) => n + Number(e[s.field] ?? 0), 0) })).filter((c) => c.n);
  const stations: EnvStation[] = envs.map((e) => ({ key: e.environment, label: e.environment, env: e.environment, tier: e.tier ?? '', stages: e.stages, counts: counted([e]) }));
  if (rest.length) {
    stations.push({ key: '+', label: countOf(rest.length, 'more environment'), env: '', except: envs.map((e) => e.environment), tier: '', stages: rest.reduce((n, e) => n + e.stages, 0), counts: counted(rest) });
  }
  const fed = (i: number) => i + 1 < envs.length && (envs[i + 1].upstream ?? []).includes(envs[i].environment);
  const wires: WireDef[] = stations.slice(1).flatMap((_, i) => fed(i) ? [{
    id: `we${i}`, from: `e${i}-r`, fs: 'c' as const, to: `e${i + 1}-l`, ts: 'c' as const, col: { from: `e${i}-b`, to: `e${i + 1}-t` },
    style: 'obs' as const, tone: 'neutral' as const, head: true,
  }] : []);
  const total = (k: EnvState) => byEnv.reduce((n, e) => n + Number(e[ENV_STATES.find((s) => s.key === k)!.field] ?? 0), 0);
  const stages = byEnv.reduce((n, e) => n + e.stages, 0);
  const cyc = byEnv.filter((e) => e.cycle).map((e) => e.environment);
  const caption = [
    `${countOf(stages, 'Stage')} in ${countOf(byEnv.length, 'environment')}: ${formatCount(total('failed'))} failed, ${formatCount(total('waiting'))} waiting for a person and ${formatCount(total('frozen'))} frozen.`,
    cyc.length ? `The Stages of ${andList(cyc, 3)} feed each other in a cycle, so ${cyc.length === 1 ? 'it is' : 'they are'} placed by name.` : '',
  ].filter(Boolean).join(' ');
  return {
    stations, wires, caption, stages,
    figRef: [wires.length ? 'An arrow joins two environments when the Stages of the second are fed by the first.' : '',
      'Stations are drawn the same size; the bars give the Stages in each state.'].filter(Boolean).join(' '),
    alt: stations.map((s) => `${s.label}: ${s.counts.map((c) => `${c.n} ${c.s.word}`).join(', ') || 'no Stages'}`).join('; '),
  };
}

/**
 * Stages per service figure, and its mark budget: each shown Stage is a station, a wire
 * from a shown feeder and, for an entry, a source marker and its wire. Stages join in
 * topological order until the next one would pass 20 marks (S2).
 */
export const MAX_STAGES = 8;

export interface PromoSource {
  kind: 'artifact' | 'records' | 'stage' | 'unknown';
  label: string;
  sub: string;
}

export interface PromoStation {
  key: string;
  env: string;
  stage: string;
  /** Grid placement: the column is the level, the row its place in the level. */
  col: number;
  row: number;
  stageRow: Row | undefined;
  state: (typeof ENV_STATES)[number] | null;
  version: string;
  source: PromoSource | null;
  cycle: boolean;
  /** The last succeeded promotion: 'from' on the Stage that offered it, 'here' on the one it reached. */
  promoted: '' | 'from' | 'here';
}

/** sourceOf says where an entry's candidates come from; null when a shown Stage feeds it. */
function sourceOf(n: GNode, fedInGraph: boolean, ownStage = false): PromoSource | null {
  const u = n.upstream;
  if (fedInGraph) return null;
  switch (u.kind) {
    case 'artifact': return { kind: 'artifact', label: 'New artifacts', sub: u.repository ? `from ${u.repository}` : 'published to the registry' };
    case 'records':
      // SPEC 21 r1: records the promoter does not trust, or cannot verify, feed nothing.
      if (u.trust === 'untrusted') return { kind: 'unknown', label: 'Untrusted records', sub: "the promoter trusts no key for another cluster's records, so the Stage never promotes" };
      if (u.trust === 'unreadable') return { kind: 'unknown', label: 'Unreadable records', sub: `the records of ${u.stream} cannot be read or verified` };
      return { kind: 'records', label: 'Another cluster', sub: u.trust === 'trusted' ? 'verified there, read from its signed records' : 'verified there; the promoter has not said yet whether it trusts the records' };
    case 'stage':
      if (u.problem === 'missing') return { kind: 'unknown', label: 'Missing upstream', sub: `the Stage ${u.namespace}/${u.stage} does not exist` };
      if (u.problem === 'unreadable') return { kind: 'unknown', label: 'Unreadable upstream', sub: `the Stage ${u.namespace}/${u.stage} cannot be read` };
      return { kind: 'stage', label: u.environment || `${u.namespace}/${u.stage}`, sub: `the Stage ${u.namespace}/${u.stage}${ownStage ? '' : ', of another service'}` };
  }
  return { kind: 'unknown', label: 'No source', sub: 'the Stage declares no candidates, so it never promotes' };
}

export function promotionModel(service: string, graph: Graph, rows: Row[]) {
  const nodes = graph.nodes ?? [], edges = graph.edges ?? [];
  const byKey = new Map(rows.map((r) => [`${r.namespace}/${r.stage}`, r]));
  const fedBy = new Map(edges.map((e) => [e.to, e.from]));
  const src = (n: GNode, shownKeys: string[]) => sourceOf(n, shownKeys.includes(fedBy.get(`${n.namespace}/${n.stage}`) ?? ''), fedBy.has(`${n.namespace}/${n.stage}`));
  const envCount = new Map<string, number>();
  for (const n of nodes) envCount.set(n.environment, (envCount.get(n.environment) ?? 0) + 1);
  const shown: GNode[] = [];
  let marks = 0;
  for (const n of nodes) {
    const key = `${n.namespace}/${n.stage}`;
    const shownKeys = shown.map((s) => `${s.namespace}/${s.stage}`);
    const fed = shownKeys.includes(fedBy.get(key) ?? '');
    const from = src(n, shownKeys);
    const cost = 1 + (fed ? 1 : 0) + (from ? (from.kind === 'unknown' ? 1 : 2) : 0);
    if (shown.length >= MAX_STAGES || marks + cost > 20) break;
    shown.push(n);
    marks += cost;
  }
  const keys = shown.map((n) => `${n.namespace}/${n.stage}`);
  const levels = [...new Set(shown.map((n) => n.level))].sort((a, b) => a - b);
  const hasSource = shown.some((n) => src(n, keys));
  const rowIn = new Map<number, number>();
  const stations: PromoStation[] = shown.map((n) => {
    const key = `${n.namespace}/${n.stage}`;
    const r = byKey.get(key);
    const row = (rowIn.get(n.level) ?? 0) + 1;
    rowIn.set(n.level, row);
    return {
      key, env: n.environment, stage: (envCount.get(n.environment) ?? 0) > 1 ? key : '',
      col: levels.indexOf(n.level) + 1 + (hasSource ? 1 : 0), row, stageRow: r, state: r ? rowState(r) : null,
      version: r ? r.version || (r.pointer ? r.pointer.slice(0, 19) : '') : '',
      source: src(n, keys), cycle: !!n.cycle, promoted: '',
    };
  });
  // The last concluded promotion is an accent arrow between the two Stages, when both
  // are drawn and one feeds the other (SPEC 7); a replay walks it with its real times.
  const last = graph.last;
  const toI = last ? keys.indexOf(last.to) : -1, fromI = last?.from ? keys.indexOf(last.from) : -1;
  const arc = toI >= 0 && fromI >= 0 ? toI : -1;
  if (arc >= 0) { stations[fromI].promoted = 'from'; stations[toI].promoted = 'here'; }
  const wires: WireDef[] = [];
  stations.forEach((s, i) => {
    const from = keys.indexOf(fedBy.get(s.key) ?? '');
    if (from >= 0) wires.push({ id: `wp${i}`, from: `s${from}-r`, fs: 'c', to: `s${i}-l`, ts: 'c', col: { from: `s${from}-b`, to: `s${i}-t` }, style: 'obs', tone: i === arc ? 'accent' : 'neutral', head: true });
    if (s.source && s.source.kind !== 'unknown') {
      wires.push({ id: `ws${i}`, from: `src${i}`, fs: 'r', to: `s${i}-l`, ts: 'c', col: { from: `src${i}`, fs: 'b', to: `s${i}-t`, ts: 'c' }, style: 'obs', tone: 'neutral', head: true });
    }
  });
  const more = nodes.length - shown.length;
  const unknown = stations.filter((s) => s.source?.kind === 'unknown');
  const forks = [...rowIn.values()].some((n) => n > 1);
  const shape = nodes.length === 1 ? 'one Stage' : forks ? 'a path that forks' : 'a chain';
  let promotion = '';
  const steps: Step[] = [];
  if (arc >= 0 && last) {
    const f = stations[fromI], t = stations[toI];
    const v = t.stageRow?.pointer === last.digest && t.stageRow.version ? t.stageRow.version : last.digest.slice(0, 19);
    const utc = (iso: string) => (Number.isNaN(Date.parse(iso)) ? iso : new Date(iso).toISOString().slice(0, 16).replace('T', ' '));
    promotion = `The accent arrow is the last promotion: ${v} from ${f.env} to ${t.env}, concluded ${utc(last.finished)} UTC.`;
    steps.push({ cap: `Replaying 1 event: ${v} promoted from ${f.env} to ${t.env}, ${last.started ? `started ${utc(last.started)} and ` : ''}concluded ${utc(last.finished)} UTC.`, dur: 240, sig: [`wp${arc}`] });
  }
  const caption = [
    `${service} is delivered through ${countOf(nodes.length, 'Stage')}, ${shape} declared by each Stage's upstream.`,
    unknown.length ? `${andList(unknown.map((s) => s.env), 3)} ${unknown.length === 1 ? 'has' : 'have'} no source Pacto can follow, drawn hatched: ${unknown.map((s) => s.source!.sub).join('; ')}.` : '',
    stations.some((s) => s.cycle) ? 'Some Stages feed each other in a cycle, so the order cannot place them.' : '',
    promotion,
  ].filter(Boolean).join(' ');
  // The "N more Stages" link opens the board without the environments every one of
  // whose Stages is drawn. ponytail: an environment holding a drawn Stage and a folded
  // one stays on that board, so it also lists the drawn sibling; a Stage filter would fix it.
  const drawnPerEnv = new Map<string, number>();
  for (const s of stations) drawnPerEnv.set(s.env, (drawnPerEnv.get(s.env) ?? 0) + 1);
  const moreExcept = [...drawnPerEnv].filter(([e, k]) => envCount.get(e) === k).map(([e]) => e);
  return {
    stations, wires, steps, caption, more, moreExcept, hasSource,
    // Only what the replay animates: the arc and its times.
    key: arc >= 0 && last ? [last.to, last.from, last.digest, last.finished] : null, cols: levels.length + (hasSource ? 1 : 0),
    rows: Math.max(1, ...rowIn.values()),
    figRef: 'An arrow goes from the Stage that verifies a version to the Stage it offers it to.',
    alt: stations.map((s) => `${s.env}${s.version ? ` runs ${s.version}` : ''}${s.state ? `, ${s.state.word}` : ''}${s.source ? `; fed by ${s.source.label}` : ''}`).join('; '),
  };
}
