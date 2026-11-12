/**
 * The Delivery Change figure's pure model (SPEC 7): "Why did the gate decide this, and
 * where is the change now?", as a rail in causal order: where the version comes from,
 * the admission policies, the checks before release, the approval, the gate, the
 * environment and the checks after release. Every station reads one field of the
 * operation the server returned, so the drawing never asserts more than the record.
 *
 * A token stands where the change is now; the narration walks the stations the change
 * reached (A1b), and the ones it did not reach are drawn at rest from frame 0.
 */
import type { DeliveryOperation } from './api.ts';
import { AUTONOMY_TYPE, changeSummary, levelLabel, versionLabel } from './delivery.ts';
import type { FigTone, GlyphName, WireDef } from './figure.ts';
import type { Step, TokenWalk } from './narrate.ts';

interface ChangeStation {
  key: string;
  label: string;
  sub: string;
  tone: FigTone;
  hatch?: boolean;
  glyph?: GlyphName | '';
  shape?: 'circle' | 'square' | 'diamond';
  reached: boolean;
  here?: string;
}

type S = Omit<ChangeStation, 'reached'>;
const unknown = (key: string, label: string, sub: string, shape: S['shape'] = 'circle'): S => ({ key, label, sub, tone: 'neutral', hatch: true, shape });

/** The station index of each part of the rail. */
const ORIGIN = 0, GATE = 4, TO = 5, VERIFY = 6;

export function changeModel(d: DeliveryOperation, namespace: string) {
  const e = d.explanation;
  const st = d.operation?.status ?? {};
  const phase = e.status.phase ?? '';
  const outcome = e.status.outcome ?? '';
  const applied = !!st.applied;
  const concluded = phase === 'Concluded';
  const held = phase === 'Held';
  const running = phase === 'Running';
  const level = e.spec.type === AUTONOMY_TYPE;
  const name = (x?: string) => (!x ? '' : level ? levelLabel(x) : versionLabel(x, d.versions));
  const fromV = name(e.spec.params?.from);
  const toV = name(e.spec.params?.to);
  const toD = level ? '' : e.spec.params?.to ?? '';
  const here = `${namespace}/${e.spec.stage}`;
  // The environment station names the Stage's spec.environment; namespace/Stage only
  // when the Stage could not be read.
  const env = d.environment || here;
  const stillRuns = fromV ? `still runs ${fromV}` : 'nothing changed here';
  const failed = outcome === 'Failed' || outcome === 'Errored';

  const stations: S[] = [
    origin(d, here, toV, fromV),
    admission(phase, !!st.guarded, e.status.reason ?? '', concluded),
    checks(st.plan?.verdicts ?? null, !!st.plan),
    approval(st, held && !applied),
    gate(),
    target(),
    verify(),
  ];

  function gate(): S {
    const g = { key: 'gate', label: 'Gate', shape: 'diamond' as const };
    if (held && !applied) return { ...g, tone: 'warn', glyph: 'held', sub: 'holds it until a person decides' };
    if (applied || running) return { ...g, tone: 'ok', glyph: 'ok', sub: 'let it through' };
    if (concluded) {
      if (outcome === 'Cancelled' || outcome === 'Superseded') return { ...g, tone: 'neutral', glyph: 'none', sub: `stopped: ${outcome === 'Cancelled' ? 'cancelled' : 'superseded'}` };
      return { ...g, tone: 'err', glyph: 'err', sub: outcome === 'Expired' ? 'stopped it: the decision expired' : outcome === 'Rejected' ? 'stopped it: rejected' : 'stopped it' };
    }
    if (phase === 'Pending' && !e.stage.frozen) return { ...g, tone: 'neutral', glyph: '', sub: 'the change waits its turn' };
    return unknown('gate', 'Gate', 'not reached', 'diamond');
  }
  function target(): S {
    const t = { key: 'to', label: env, shape: 'square' as const };
    if (running && !applied) return { ...t, tone: 'accent', glyph: '', sub: `${toV || 'the change'} is being released` };
    if (applied) return failed ? { ...t, tone: 'err', glyph: 'err', sub: `${toV || 'the change'} was released here, then failed` } : { ...t, tone: 'ok', glyph: 'ok', sub: `${toV || 'the change'} was released here` };
    if (e.stage.frozen && !concluded) return { ...t, tone: 'frost', glyph: 'frozen', sub: `frozen: ${stillRuns}` };
    return unknown('to', env, `not reached: ${stillRuns}`, 'square');
  }
  function verify(): S {
    const v = { key: 'verify', label: 'Checks after release' };
    if (!applied) return unknown('verify', v.label, 'not run');
    if (phase === 'Verifying') return { ...v, tone: 'neutral', glyph: '', sub: 'being checked' };
    if (held) return { ...v, tone: 'warn', glyph: 'held', sub: 'ended unknown: a person decides' };
    // The Stage's newest re-check, when it judged this change's version as this change
    // shipped it (the operation uid, as the server's CheckSummary.current reads it):
    // a later change that brought the same version back has its own re-check.
    const lc = e.stage.lastCheck;
    const uid = d.operation?.metadata?.uid;
    if (lc && toD && lc.digest === toD && uid && lc.operation === uid) {
      const rs = (lc.verdicts ?? []).map((x) => x.result);
      if (rs.includes('Fail')) return { ...v, tone: 'err', glyph: 'err', sub: 're-checked after release: failed' };
      if (rs.includes('Unknown')) return { ...v, tone: 'neutral', hatch: true, glyph: 'unknown', sub: 're-checked after release: could not decide' };
      return { ...v, tone: 'ok', glyph: 'ok', sub: 're-checked after release: passed' };
    }
    switch (e.status.verification) {
      case 'Verified': return { ...v, tone: 'ok', glyph: 'ok', sub: 'verified' };
      case 'AcceptedUnknown': return { ...v, tone: 'neutral', hatch: true, glyph: 'unknown', sub: 'a person accepted the unknown' };
    }
    return failed ? { ...v, tone: 'err', glyph: 'err', sub: 'failed' } : unknown('verify', v.label, 'not verified');
  }

  // Where the change is now, in its tone: held at the gate, released at the
  // environment, stopped at the gate, waiting at the start.
  let at = ORIGIN;
  let tone: FigTone = 'accent';
  if (held && !applied) { at = GATE; tone = 'warn'; }
  else if (applied || running) { at = TO; tone = failed ? 'err' : held ? 'warn' : outcome === 'Succeeded' ? 'ok' : 'accent'; }
  else if (concluded) { at = GATE; tone = outcome === 'Cancelled' || outcome === 'Superseded' ? 'neutral' : 'err'; }
  else if (e.stage.frozen) tone = 'frost';
  else if (phase === 'Pending') at = GATE;
  const reach = applied && !running ? VERIFY : at;
  stations[at] = { ...stations[at], here: `${toV || 'The change'} is here` };

  const all: ChangeStation[] = stations.map((s, i) => ({ ...s, reached: i <= reach }));
  const diamond = (k: number, side: 'l' | 'r' | 't' | 'b') => (k === GATE ? `f${k}-${side}` : `f${k}`);
  const wires: WireDef[] = all.slice(1).map((_, k) => ({
    id: `wf${k}`, from: diamond(k, 'r'), fs: k === GATE ? 'c' : 'r', to: diamond(k + 1, 'l'), ts: k + 1 === GATE ? 'c' : 'l',
    col: { from: diamond(k, 'b'), fs: k === GATE ? 'c' : 'b', to: diamond(k + 1, 't'), ts: k + 1 === GATE ? 'c' : 't' },
    style: k + 1 <= reach ? 'obs' : 'proj', tone: 'neutral', head: true,
  }));
  // A1b: one step per station the change reached, 120ms with a 30ms hold.
  const steps: Step[] = all.slice(0, reach + 1).map((s, k) => ({
    cap: `${s.label}: ${s.sub}.`, dur: 120, hold: 30,
    show: [`[data-m="f${k}"]`, ...(k ? [`w:wf${k - 1}`] : [])], sig: k ? [`wf${k - 1}`] : [],
  }));
  const token: TokenWalk = { sel: '.tok', slots: steps.map((_, k) => `[data-tok="${Math.min(k, at)}"]`) };
  const summary = changeSummary(e, namespace, d.versions);
  return {
    stations: all, at, tone, reach, wires, steps, token, summary,
    // A6 and S10: only an errored or held change moves on its own; frozen is information.
    autoplay: tone === 'err' || tone === 'warn',
    key: { c: `${namespace}/${d.operation?.metadata?.name ?? ''}`, phase, outcome, applied, at },
    alt: `${summary} ${all.map((s) => `${s.label}: ${s.sub}`).join('; ')}.`,
    figRef: `The callout is the change; it rests at the station marked “${toV || 'The change'} is here”.`,
  };
}

/** Where the version comes from: the upstream the Stage declares, or the environment before the change. */
function origin(d: DeliveryOperation, here: string, toV: string, fromV: string): S {
  const u = d.upstream;
  const before: S = { key: 'from', label: here, sub: fromV ? `${fromV} runs here` : 'before the change', tone: 'neutral', glyph: '', shape: 'square' };
  if (d.explanation.spec.type !== 'delivery.pacto.trianalab.io/promote' || !u) return before;
  const v = toV || 'the candidate';
  switch (u.kind) {
    case 'stage':
      if (u.problem) return unknown('from', `${u.namespace}/${u.stage}`, u.problem === 'missing' ? 'the upstream Stage does not exist' : u.problem === 'self' ? 'the Stage names itself as its upstream' : u.problem === 'outOfScope' ? 'the upstream Stage is in a namespace this dashboard does not show' : 'the upstream Stage cannot be read', 'square');
      return { ...before, label: u.environment || `${u.namespace}/${u.stage}`, sub: `${v} verified here` };
    case 'records': return { ...before, label: 'Another cluster', sub: `${v} verified there, read from its signed records` };
    case 'artifact': return { ...before, label: 'New artifacts', sub: `${v} published` };
  }
  return unknown('from', 'No source', 'the Stage declares no candidates', 'square');
}

function admission(phase: string, guarded: boolean, reason: string, concluded: boolean): S {
  const a = { key: 'admission', label: 'Admission policies' };
  if (!phase && !concluded) return reason === 'Unguarded' ? { ...a, tone: 'warn', glyph: 'held', sub: 'waiting for the policies' } : unknown(a.key, a.label, 'not admitted yet');
  return guarded ? { ...a, tone: 'ok', glyph: 'ok', sub: 'admitted under them' } : unknown(a.key, a.label, 'not recorded');
}

function checks(verdicts: { result?: string }[] | null, planned: boolean): S {
  const c = { key: 'checks', label: 'Checks before release' };
  if (!planned) return unknown(c.key, c.label, 'not run');
  const n = (r: string) => (verdicts ?? []).filter((v) => v.result === r).length;
  if (n('Fail')) return { ...c, tone: 'err', glyph: 'err', sub: `${n('Fail')} failed` };
  if (n('Unknown')) return { ...c, tone: 'neutral', hatch: true, glyph: 'unknown', sub: `${n('Unknown')} ended unknown` };
  return { ...c, tone: 'ok', glyph: 'ok', sub: verdicts?.length ? `all ${verdicts.length} passed` : 'none to run' };
}

function approval(st: NonNullable<NonNullable<DeliveryOperation['operation']>['status']>, waiting: boolean): S {
  const a = { key: 'approval', label: 'Approval' };
  if (waiting) return { ...a, tone: 'warn', glyph: 'held', sub: 'waits for a person' };
  const last = st.approvals?.[st.approvals.length - 1]?.decision;
  if (last === 'reject') return { ...a, tone: 'err', glyph: 'err', sub: 'a person rejected it' };
  if (last === 'approve') return { ...a, tone: 'ok', glyph: 'ok', sub: 'a person approved it' };
  if (last === 'accept-unknown') return { ...a, tone: 'ok', glyph: 'ok', sub: 'a person accepted the unknown' };
  if (st.plan && !st.plan.floor?.human) return { ...a, tone: 'neutral', glyph: 'none', sub: 'not needed' };
  return unknown(a.key, a.label, 'not asked');
}
