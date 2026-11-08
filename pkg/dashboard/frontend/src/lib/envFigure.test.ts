import { describe, it, expect } from 'vitest';
import { fleetEnvModel, promotionModel, rowState, ENV_STATES, MAX_ENV_STATIONS } from './envFigure.ts';

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture builder
type Any = any;
const env = (environment: string, c: Partial<Record<string, number>> = {}, upstream: string[] = []): Any => ({
  environment, stages: Object.values(c).reduce((a, b) => a! + b!, 0), failed: 0, frozen: 0, autoHold: 0, waiting: 0, unverified: 0, empty: 0, verified: 0, upstream, ...c,
});
const node = (environment: string, level: number, upstream: Any, ns = environment): Any => ({ namespace: ns, stage: 'svc', environment, level, upstream });
const row = (ns: string, r: Any = {}): Any => ({ namespace: ns, stage: 'svc', service: 'svc', environment: ns, verified: true, failed: false, frozen: false, autoHold: false, held: 0, pointer: 'sha256:a', version: '1.0.0', actions: [], ...r });

describe('fleetEnvModel', () => {
  it('counts each environment in canonical order and arrows only declared links (S10, SPEC 21)', () => {
    const m = fleetEnvModel([env('test', { verified: 2, unverified: 1 }), env('prod', { failed: 1, verified: 1 }, ['test']), env('books', { frozen: 1 })]);
    expect(m.stations.map((s) => s.label)).toEqual(['test', 'prod', 'books']);
    expect(m.stations[0].counts.map((c) => c.s.key)).toEqual(['unverified', 'verified']);
    expect(m.stations[1].counts.map((c) => [c.s.key, c.n])).toEqual([['failed', 1], ['verified', 1]]);
    expect(m.wires.map((w) => [w.from, w.to])).toEqual([['e0-r', 'e1-l']]);
    expect(m.caption).toBe('6 Stages in 3 environments: 1 failed, 0 waiting for a person and 1 frozen.');
    // Unknown is the hatch, never a status tone; order is err, warn, frozen, unknown, ok.
    expect(ENV_STATES.find((s) => s.key === 'unverified')).toMatchObject({ tone: 'neutral', hatch: true });
    expect(ENV_STATES.map((s) => s.tone)).toEqual(['err', 'warn', 'warn', 'frost', 'neutral', 'neutral', 'ok']);
  });

  it('caps the stations at any fleet size, keeps the worst and folds the rest (S2)', () => {
    for (const n of [1, 8, 40, 400]) {
      const all = Array.from({ length: n }, (_, i) => env(`env-${i}`, { failed: i % 9, verified: 3 }, i ? [`env-${i - 1}`] : []));
      const m = fleetEnvModel(all);
      expect(m.stations.length).toBe(Math.min(n, MAX_ENV_STATIONS));
      expect(m.stations.length + m.wires.length).toBeLessThanOrEqual(20);
      const sum = m.stations.reduce((a, s) => a + s.counts.reduce((b, c) => b + c.n, 0), 0);
      expect(sum).toBe(m.stages);
      if (n > MAX_ENV_STATIONS) {
        expect(m.stations.at(-1)!.key).toBe('+');
        // Its filter is every environment but the ones drawn.
        expect(m.stations.at(-1)!.except).toEqual(m.stations.slice(0, -1).map((s) => s.env));
        expect(m.stations.slice(0, -1).every((s) => s.counts[0].s.key === 'failed')).toBe(true);
      }
    }
  });

  it('says when a cycle places environments by name', () => {
    expect(fleetEnvModel([{ ...env('a', { verified: 1 }), cycle: true }]).caption).toContain('The Stages of a feed each other in a cycle, so it is placed by name.');
  });
});

describe('rowState', () => {
  it('counts a row in the first state that holds, as the server does', () => {
    expect(rowState(row('x', { failed: true, frozen: true })).key).toBe('failed');
    expect(rowState(row('x', { frozen: true, autoHold: true })).key).toBe('frozen');
    expect(rowState(row('x', { autoHold: true, held: 1 })).key).toBe('autohold');
    expect(rowState(row('x', { held: 2 })).key).toBe('waiting');
    expect(rowState(row('x', { pointer: '' })).key).toBe('empty');
    expect(rowState(row('x', { verified: false })).key).toBe('unverified');
    expect(rowState(row('x')).key).toBe('verified');
  });
});

describe('promotionModel', () => {
  const art = { kind: 'artifact', repository: 'svc-candidates' };
  it('draws a fork from an artifact entry, one column per link (SPEC 21)', () => {
    const graph = {
      nodes: [node('dev', 0, art), node('staging', 1, { kind: 'stage', namespace: 'dev', stage: 'svc', environment: 'dev' }),
        node('prod-eu', 2, { kind: 'stage', namespace: 'staging', stage: 'svc' }), node('prod-us', 2, { kind: 'stage', namespace: 'staging', stage: 'svc' })],
      edges: [{ from: 'dev/svc', to: 'staging/svc' }, { from: 'staging/svc', to: 'prod-eu/svc' }, { from: 'staging/svc', to: 'prod-us/svc' }],
    };
    const m = promotionModel('svc', graph, [row('dev'), row('staging', { held: 1 }), row('prod-eu', { failed: true }), row('prod-us')]);
    expect(m.stations.map((s) => [s.env, s.col, s.row])).toEqual([['dev', 2, 1], ['staging', 3, 1], ['prod-eu', 4, 1], ['prod-us', 4, 2]]);
    expect(m.stations[0].source).toMatchObject({ kind: 'artifact', label: 'New artifacts', sub: 'from svc-candidates' });
    expect(m.stations.slice(1).every((s) => s.source === null)).toBe(true);
    expect(m.wires.map((w) => `${w.from}>${w.to}`)).toEqual(['src0>s0-l', 's0-r>s1-l', 's1-r>s2-l', 's1-r>s3-l']);
    expect(m.stations.map((s) => s.state?.key)).toEqual(['verified', 'waiting', 'failed', 'verified']);
    expect(m.caption).toBe('svc is delivered through 4 Stages, a path that forks declared by each Stage\'s upstream.');
  });

  it('draws the last promotion as an accent arrow with badges and a replay on its real times', () => {
    const graph: Any = {
      nodes: [node('test', 0, art), node('prod', 1, { kind: 'stage', namespace: 'test', stage: 'svc', environment: 'test' })],
      edges: [{ from: 'test/svc', to: 'prod/svc' }],
      last: { to: 'prod/svc', from: 'test/svc', digest: 'sha256:a', started: '2026-11-08T10:00:00Z', finished: '2026-11-08T12:04:00Z' },
    };
    const m = promotionModel('svc', graph, [row('test'), row('prod')]);
    expect(m.stations.map((s) => s.promoted)).toEqual(['from', 'here']);
    expect(m.wires.find((w) => w.id === 'wp1')?.tone).toBe('accent');
    expect(m.steps).toEqual([{ cap: 'Replaying 1 event: 1.0.0 promoted from test to prod, started 2026-11-08 10:00 and concluded 2026-11-08 12:04 UTC.', dur: 240, sig: ['wp1'] }]);
    expect(m.caption).toContain('The accent arrow is the last promotion: 1.0.0 from test to prod, concluded 2026-11-08 12:04 UTC.');
    expect(m.key).toEqual(['prod/svc', 'test/svc', 'sha256:a', '2026-11-08T12:04:00Z']);
    // A promotion into an entry, or a digest the row no longer runs, draws no arc or names the digest.
    expect(promotionModel('svc', { ...graph, last: { ...graph.last, from: '' } }, []).steps).toEqual([]);
    const moved = promotionModel('svc', { ...graph, last: { ...graph.last, digest: 'sha256:bbbbbbbbbbbbbbbbbbbbbbbb', started: '' } }, [row('test'), row('prod')]);
    expect(moved.steps[0].cap).toBe('Replaying 1 event: sha256:bbbbbbbbbbbb promoted from test to prod, concluded 2026-11-08 12:04 UTC.');
    expect(promotionModel('svc', { ...graph, last: undefined }, []).key).toBeNull();
  });

  it('draws a source it cannot follow hatched, with the reason and no wire (SPEC 21 rule 4)', () => {
    const graph = { nodes: [node('prod', 0, { kind: 'none' }), node('qa', 0, { kind: 'stage', namespace: 'x', stage: 'gone', problem: 'missing' }),
      node('dr', 0, { kind: 'records', stream: 'c/n/s' }), node('ext', 0, { kind: 'stage', namespace: 'o', stage: 'other', environment: 'test' })], edges: [] };
    const m = promotionModel('svc', graph, []);
    expect(m.stations.map((s) => s.source?.kind)).toEqual(['unknown', 'unknown', 'records', 'stage']);
    expect(m.stations[0].source!.sub).toBe('the Stage declares no candidates, so it never promotes');
    expect(m.stations[1].source!.sub).toBe('the Stage x/gone does not exist');
    expect(m.stations[3].source).toMatchObject({ label: 'test', sub: 'the Stage o/other, of another service' });
    expect(m.wires.map((w) => w.to)).toEqual(['s2-l', 's3-l']);
    expect(m.caption).toContain('prod and qa have no source Pacto can follow, drawn hatched');
    expect(m.stations.every((s) => s.state === null)).toBe(true);
  });

  it('stays within 20 marks however many Stages a service has, and says how many more (S2)', () => {
    for (const n of [1, 8, 40]) {
      const nodes = Array.from({ length: n }, (_, i) => node(`e${i}`, i, i ? { kind: 'stage', namespace: `e${i - 1}`, stage: 'svc' } : art));
      const edges = nodes.slice(1).map((_, i) => ({ from: `e${i}/svc`, to: `e${i + 1}/svc` }));
      const m = promotionModel('svc', { nodes, edges }, []);
      const marks = m.stations.length + m.stations.filter((s) => s.source).length + m.wires.length;
      expect(marks).toBeLessThanOrEqual(20);
      expect(m.more).toBe(n - m.stations.length);
      if (m.more) expect(m.moreExcept).toEqual(m.stations.map((s) => s.env));
    }
  });

  it('names the Stage when an environment holds two of the service, and marks a cycle', () => {
    const graph = { nodes: [node('prod', 0, { kind: 'stage', namespace: 'b', stage: 'svc' }, 'a'), { ...node('prod', 1, { kind: 'stage', namespace: 'a', stage: 'svc' }, 'b'), cycle: true }],
      edges: [{ from: 'a/svc', to: 'b/svc' }, { from: 'b/svc', to: 'a/svc' }] };
    const m = promotionModel('svc', graph, []);
    expect(m.stations.map((s) => s.stage)).toEqual(['a/svc', 'b/svc']);
    expect(m.stations.map((s) => s.source)).toEqual([null, null]);
    expect(m.wires.map((w) => `${w.from}>${w.to}`)).toEqual(['s1-r>s0-l', 's0-r>s1-l']);
    expect(m.caption).toContain('Some Stages feed each other in a cycle');
    expect(promotionModel('svc', { nodes: [node('prod', 0, art)], edges: null } as Any, []).caption).toContain('1 Stage, one Stage');
  });
});
