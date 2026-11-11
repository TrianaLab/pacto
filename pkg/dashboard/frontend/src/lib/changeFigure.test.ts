import { describe, it, expect } from 'vitest';
import { changeModel } from './changeFigure.ts';
import { timeline, sessionKey } from './narrate.ts';

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture builder
type Any = any;
const FROM = `sha256:${'1'.repeat(64)}`, TO = `sha256:${'2'.repeat(64)}`;

function change(status: Any = {}, o: Any = {}): Any {
  const { phase = 'Pending', outcome, verification, reason, frozen = false, ...st } = status;
  return {
    explanation: {
      operation: 'payments-promote-2', summary: '', spec: { type: 'delivery.pacto.trianalab.io/promote', stage: 'payments', params: { from: FROM, to: TO }, actor: { user: 'a' } },
      status: { phase, outcome, verification, reason }, verdicts: [], stage: { name: 'payments', frozen },
    },
    operation: { metadata: { name: 'payments-promote-2', namespace: 'prod' }, spec: {}, status: { phase, outcome, plan: { verdicts: [{ gate: 'pacto.diff', result: 'Pass' }], floor: {} }, guarded: true, ...st } },
    versions: { [FROM]: '2.0.0', [TO]: '2.0.1' },
    upstream: { kind: 'stage', namespace: 'test', stage: 'payments', environment: 'test' },
    actions: [], ...o,
  };
}

const view = (m: ReturnType<typeof changeModel>) => m.stations.map((s) => `${s.label}|${s.tone}${s.hatch ? '/hatch' : ''}|${s.sub}${s.reached ? '' : '|rest'}`);

describe('changeModel stations (G3)', () => {
  it('holds a change at the gate, the token there and the rest of the rail at rest', () => {
    const m = changeModel(change({ phase: 'Held', plan: { verdicts: [{ result: 'Pass' }], floor: { human: true } } }), 'prod');
    expect(view(m)).toEqual([
      'test|neutral|2.0.1 verified here', 'Admission policies|ok|admitted under them', 'Checks before release|ok|all 1 passed',
      'Approval|warn|waits for a person', 'Gate|warn|holds it until a person decides',
      'prod/payments|neutral/hatch|not reached: still runs 2.0.0|rest', 'Checks after release|neutral/hatch|not run|rest',
    ]);
    expect([m.at, m.tone, m.reach, m.autoplay]).toEqual([4, 'warn', 4, true]);
    expect(m.stations[4].here).toBe('2.0.1 is here');
    expect(m.wires.map((w) => w.style)).toEqual(['obs', 'obs', 'obs', 'obs', 'proj', 'proj']);
    // The wires end on the gate's vertices (C13) and all mean "moves to" (C13b).
    expect([m.wires[3].to, m.wires[4].from]).toEqual(['f4-l', 'f4-r']);
    expect(m.wires.every((w) => w.head)).toBe(true);
  });

  it('puts a change released then failed at the environment, with its checks after release', () => {
    const m = changeModel(change({ phase: 'Concluded', outcome: 'Failed', verification: 'Unverified', applied: true }), 'prod');
    expect([m.at, m.tone, m.reach, m.autoplay]).toEqual([5, 'err', 6, true]);
    expect(view(m).slice(4)).toEqual(['Gate|ok|let it through', 'prod/payments|err|2.0.1 was released here, then failed', 'Checks after release|err|failed']);
  });

  it('rests a verified release at the environment, calm, named by the Stage', () => {
    const m = changeModel(change({ phase: 'Concluded', outcome: 'Succeeded', verification: 'Verified', applied: true }, { environment: 'production' }), 'prod');
    expect([m.at, m.tone, m.autoplay]).toEqual([5, 'ok', false]);
    expect(m.stations[5]).toMatchObject({ label: 'production', sub: '2.0.1 was released here' });
    expect(m.stations[6]).toMatchObject({ tone: 'ok', sub: 'verified' });
    expect(m.stations[3]).toMatchObject({ tone: 'neutral', sub: 'not needed' });
  });

  it('reads the checks after release from the Stage\'s newest re-check when it is about this change', () => {
    const shipped = { phase: 'Concluded', outcome: 'Succeeded', verification: 'Verified', applied: true };
    const withCheck = (digest: string, result: string) => {
      const c = change(shipped);
      c.explanation.stage.lastCheck = { operation: 'u1', digest, at: '2026-11-11T09:58:00Z', verdicts: [{ gate: 'mira.health', phase: 'verify', result: 'Pass' }, { gate: 'pacto.contract', phase: 'verify', result }] };
      return changeModel(c, 'prod').stations[6];
    };
    expect(withCheck(TO, 'Fail')).toMatchObject({ tone: 'err', sub: 're-checked after release: failed' });
    expect(withCheck(TO, 'Unknown')).toMatchObject({ tone: 'neutral', hatch: true, sub: 're-checked after release: could not decide' });
    expect(withCheck(TO, 'Pass')).toMatchObject({ tone: 'ok', sub: 're-checked after release: passed' });
    // A re-check of another version says nothing about this change.
    expect(withCheck(FROM, 'Fail')).toMatchObject({ tone: 'ok', sub: 'verified' });
  });

  it('draws an accepted unknown as the hatch, never ok', () => {
    const m = changeModel(change({ phase: 'Concluded', outcome: 'Succeeded', verification: 'AcceptedUnknown', applied: true, approvals: [{ decision: 'accept-unknown' }] }), 'prod');
    expect(m.stations[6]).toMatchObject({ tone: 'neutral', hatch: true, sub: 'a person accepted the unknown' });
    for (const s of m.stations.filter((x) => x.hatch)) expect(s.tone).toBe('neutral');
  });

  it('waits at the start in frost when the environment is frozen, and does not move on its own', () => {
    const m = changeModel(change({ phase: 'Pending', frozen: true }), 'prod');
    expect([m.at, m.tone, m.reach, m.autoplay]).toEqual([0, 'frost', 0, false]);
    expect(m.stations[5]).toMatchObject({ tone: 'frost', glyph: 'frozen', sub: 'frozen: still runs 2.0.0' });
  });

  it('waits at the gate when it is planned and waits its turn', () => {
    const m = changeModel(change({ phase: 'Pending' }), 'prod');
    expect([m.at, m.tone, m.reach, m.autoplay]).toEqual([4, 'accent', 4, false]);
    expect(m.stations[4]).toMatchObject({ tone: 'neutral', sub: 'the change waits its turn' });
  });

  it('stops a rejected change at the gate', () => {
    const m = changeModel(change({ phase: 'Concluded', outcome: 'Rejected', approvals: [{ decision: 'reject' }] }), 'prod');
    expect([m.at, m.tone]).toEqual([4, 'err']);
    expect(view(m)[3]).toBe('Approval|err|a person rejected it');
    expect(m.stations[4].sub).toBe('stopped it: rejected');
    const c = changeModel(change({ phase: 'Concluded', outcome: 'Cancelled' }), 'prod');
    expect([c.tone, c.stations[4].sub]).toEqual(['neutral', 'stopped: cancelled']);
  });

  it('says where the version comes from, as the Stage declares it', () => {
    const origin = (upstream: Any, type?: string) => {
      const d = change({}, { upstream });
      if (type) d.explanation.spec.type = type;
      return view(changeModel(d, 'prod'))[0];
    };
    expect(origin({ kind: 'records' })).toBe('Another cluster|neutral|2.0.1 verified there, read from its signed records');
    expect(origin({ kind: 'artifact' })).toBe('New artifacts|neutral|2.0.1 published');
    expect(origin({ kind: 'none' })).toBe('No source|neutral/hatch|the Stage declares no candidates');
    expect(origin({ kind: 'stage', namespace: 'test', stage: 'gone', problem: 'missing' })).toBe('test/gone|neutral/hatch|the upstream Stage does not exist');
    expect(origin({ kind: 'stage', namespace: 'test', stage: 'away', problem: 'outOfScope' })).toBe('test/away|neutral/hatch|the upstream Stage is in a namespace this dashboard does not show');
    expect(origin(undefined)).toBe('prod/payments|neutral|2.0.0 runs here');
    expect(origin({ kind: 'stage' }, 'delivery.pacto.trianalab.io/rollback')).toBe('prod/payments|neutral|2.0.0 runs here');
  });
});

describe('changeModel narration (G2)', () => {
  it('walks only the stations the change reached, within the rail-walk budget', () => {
    for (const st of [{ phase: 'Held' }, { phase: 'Concluded', outcome: 'Failed', applied: true }, { phase: 'Pending' }]) {
      const m = changeModel(change(st), 'prod');
      expect(m.steps).toHaveLength(m.reach + 1);
      expect(timeline(m.steps, { walk: true }).total).toBeLessThanOrEqual(1170);
      expect(m.token.slots).toHaveLength(m.steps.length);
      expect(m.token.slots[m.token.slots.length - 1]).toBe(`[data-tok="${m.at}"]`);
      // A step reveals only reached stations and the wire into them.
      expect(m.steps.flatMap((s) => s.show)).not.toContain(`[data-m="f${m.reach + 1}"]`);
    }
  });

  it('keys the story on where the change is, never on unrelated fields', () => {
    const a = changeModel(change({ phase: 'Held' }), 'prod');
    const b = changeModel(change({ phase: 'Held' }, { versions: {} }), 'prod');
    const c = changeModel(change({ phase: 'Concluded', outcome: 'Failed', applied: true }), 'prod');
    expect(sessionKey(a.key)).toBe(sessionKey(b.key));
    expect(sessionKey(a.key)).not.toBe(sessionKey(c.key));
  });

  it('draws at most 20 marks: seven stations, six arrows and the token', () => {
    const m = changeModel(change(), 'prod');
    expect(m.stations.length + 2 * m.wires.length + 1).toBeLessThanOrEqual(20);
  });
});
