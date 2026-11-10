import { describe, it, expect } from 'vitest';
import { evidenceModel, offeringModel, revisionStackModel, serviceStackModel, sourceModel, targetModel, useDetailModel, validationModel, MAX_EVIDENCE_MARKS, MAX_PROVIDER_MARKS } from './entityFigures.ts';
import { timeline, sessionKey } from './narrate.ts';
import { MAX_STACK_LAYERS } from './figure.ts';

const ref = (k: string) => ({ key: k, label: k });
const label = (r: { label?: string }) => r.label ?? '';

describe('validationModel (G2, G3)', () => {
  const passed = [{ name: 'structural', status: 'pass' }, { name: 'cross-field', status: 'pass' }];
  it('asks whether every check passed when they did, and stays still', () => {
    const m = validationModel(passed);
    expect(m.question).toBe('Did this revision pass every check?');
    expect(m.autoplay).toBe(false);
    expect(m.summary).toBe('Every check passed: 2 checks.');
    expect(m.wires.map((w) => [w.style, w.head])).toEqual([['obs', true]]);
  });
  it('names where it stopped, draws not run hatched and walks every station (A1b)', () => {
    const m = validationModel([...passed, { name: 'policy', status: 'fail', reason: 'missing owner' }, { name: 'configuration-refs', status: 'skip' }, { name: 'environments', status: 'skip', unverified: true }]);
    expect(m.question).toBe('Where did validation stop?');
    expect(m.stations.map((s) => `${s.label}|${s.tone}${s.hatch ? '/hatch' : ''}|${s.sub}`)).toEqual([
      'Structure|ok|passed', 'Cross-field|ok|passed', 'Policy|err|failed: missing owner',
      'Configuration references|neutral/hatch|not run', 'Environments|neutral/hatch|not verified',
    ]);
    expect(m.wires.map((w) => w.style)).toEqual(['obs', 'obs', 'proj', 'proj']);
    expect(m.steps).toHaveLength(5);
    // No signal runs into a stage that did not run.
    expect(m.steps.map((s) => s.sig)).toEqual([[], ['wf0'], ['wf1'], [], []]);
    expect(timeline(m.steps, { walk: true }).total).toBeLessThanOrEqual(1200);
    expect(m.autoplay).toBe(true);
    expect(m.summary).toContain('Validation stopped at Policy (failed: missing owner). 2 checks did not run');
  });
  it('says what did not run when nothing failed', () => {
    const m = validationModel([{ name: 'structural', status: 'pass' }, { name: 'policy', status: 'skip', reason: 'no policies' }]);
    expect(m.summary).toBe('1 check did not run or could not be verified; that is never a pass.');
    expect(m.stations[1].sub).toBe('not run: no policies');
    expect(m.autoplay).toBe(false);
  });
  it('keys the session on the stages only', () => {
    expect(sessionKey(validationModel(passed).key)).toBe(sessionKey(validationModel(passed.map((s) => ({ ...s, reason: 'x' }))).key));
  });
  it('words an unverified stage with its reason', () => {
    expect(validationModel([{ name: 'x', status: 'skip', unverified: true, reason: 'offline' }]).stations[0].sub).toBe('not verified: offline');
    expect(validationModel([{ name: 'x', status: 'fail' }]).stations[0].sub).toBe('failed');
  });
});

describe('serviceStackModel (G3)', () => {
  for (const n of [1, 8, 40, 400, 4000]) {
    it(`draws at most ${MAX_STACK_LAYERS} layers and folds the rest of ${n} into the plinth`, () => {
      const items = Array.from({ length: Math.min(n, 200) }, (_, i) => ref(`r${i}`));
      const m = serviceStackModel({ total: n, items }, 'r1', label);
      expect(m.layers.length).toBe(Math.min(n, MAX_STACK_LAYERS));
      expect(m.earlier + m.layers.length).toBe(n);
      expect(m.wires.some((w) => w.from === 'plinth')).toBe(n > MAX_STACK_LAYERS);
      expect(m.oldest).toBe(n <= 200 ? `r${n - 1}` : '');
    });
  }
  it('tones only the current revision accent and names the newest', () => {
    const m = serviceStackModel({ total: 3, items: [ref('a'), ref('b'), ref('c')] }, 'b', label);
    expect(m.layers.map((l) => l.tone)).toEqual(['neutral', 'accent', 'neutral']);
    expect(m.rows.map((r) => r.note)).toEqual(['newest', 'current', '']);
    expect(m.alt).toBe('3 revisions; the newest is a.');
    expect(serviceStackModel(undefined, '', label).alt).toBe('0 revisions.');
  });
});

describe('revisionStackModel', () => {
  it('draws this revision between its neighbours, the neighbours dashed', () => {
    const m = revisionStackModel({ key: 'c', version: '2.0.0' }, ref('1.9.0'), ref('2.1.0'), label);
    expect(m.rows.map((r) => `${r.note}:${r.label}`)).toEqual(['next:2.1.0', 'this revision:2.0.0', 'previous:1.9.0']);
    expect(m.layers.map((l) => `${l.tone}${l.dash ? '/dash' : ''}`)).toEqual(['neutral/dash', 'accent', 'neutral/dash']);
    expect(m.wires.map((w) => w.style)).toEqual(['proj', 'obs', 'proj']);
    expect(m.summary).toBe('Previous 1.9.0. Next 2.1.0.');
  });
  it('draws no neighbour it does not have', () => {
    const m = revisionStackModel({ key: 'c', label: 'svc 1.0.0' }, null, undefined, label);
    expect(m.rows.map((r) => r.label)).toEqual(['svc 1.0.0']);
    expect(m.summary).toBe('No previous revision. No newer revision.');
  });
});

describe('targetModel and sourceModel (SPEC 2.1)', () => {
  it('styles the link by its state, missing when unknown', () => {
    expect(['exact', 'inferred', 'ambiguous', 'unresolved', undefined].map((s) => targetModel(s).style)).toEqual(['obs', 'proj', 'stale', 'broken', 'broken']);
    expect(targetModel('ambiguous').wires[0].tone).toBe('warn');
    expect(targetModel('bogus').words).toContain('missing');
  });
  it('styles the source wire by its health; an unavailable source is broken', () => {
    expect(['available', 'stale', 'partial', 'unavailable', 'weird'].map((h) => `${sourceModel(h).style}|${sourceModel(h).tone}`))
      .toEqual(['obs|ok', 'stale|warn', 'stale|neutral', 'broken|err', 'stale|neutral']);
    expect(sourceModel('weird').hatch).toBe(true);
  });
  it('never tones a source ok unless it is current (G3: unknown is never ok)', () => {
    for (const h of ['stale', 'partial', 'unavailable', '', 'weird', undefined]) expect(sourceModel(h).tone).not.toBe('ok');
    expect(sourceModel('partial').hatch).toBe(true);
  });
});

describe('offeringModel (G3, S2)', () => {
  const providers = (n: number) => ({ total: n, items: Array.from({ length: Math.min(n, 25) }, (_, i) => ref(`p${i}`)) });
  for (const n of [1, 2, 3, 40]) {
    it(`draws at most ${MAX_PROVIDER_MARKS} provider marks for ${n}`, () => {
      const m = offeringModel(providers(n), {});
      expect(m.named.length + (m.more ? 1 : 0)).toBe(Math.min(n, MAX_PROVIDER_MARKS));
      expect(m.named.length + m.more).toBe(n);
    });
  }
  it('counts requests per phase on one denominator and keeps empty phases at 0, neutral', () => {
    const m = offeringModel(providers(1), { requested: 1, ready: 6, failed: 2, unknown: 1 });
    expect(m.requests).toBe(10);
    expect(m.stations.map((s) => `${s.label}|${s.n}|${s.share}|${s.tone}${s.hatch ? '/hatch' : ''}`)).toEqual([
      'Requested|1|10%|info', 'Provisioning|0|0%|neutral', 'Ready|6|60%|ok', 'Deleting|0|0%|neutral', 'Failed|2|20%|err', 'Not reported|1|10%|neutral/hatch',
    ]);
    expect(m.wires.find((w) => w.id === 'wfail')!.tone).toBe('err');
    // The consumers each station's link lists are their own unit: six Ready requests can be one consumer.
    expect(m.stations.map((s) => s.consumers)).toEqual([0, 0, 0, 0, 0, 0]);
    expect(offeringModel(providers(1), { ready: 6 }, { ready: 1 }).stations.find((s) => s.key === 'ready')).toMatchObject({ n: 6, consumers: 1 });
    expect(offeringModel(providers(1), { ready: 1 }).wires.find((w) => w.id === 'wfail')!.tone).toBe('neutral');
    // Unknown is never ok-toned.
    expect(m.stations.find((s) => s.key === 'unknown')!.tone).not.toBe('ok');
    expect(m.summary).toBe('Of 10 requests, 6 are ready, 1 in progress, 2 failed and 0 being removed; 1 has no reported phase, so their readiness is unknown.');
  });
  it('says when nobody asked for a resource', () => {
    expect(offeringModel(providers(40), undefined).summary).toBe('No consumer has asked a provider for a resource yet.');
  });
  // Marks as G1 counts them: provider marks, the hub, the five track stations, Not
  // reported only when it has requests, and every drawn wire with its arrowhead.
  for (const unknown of [0, 3]) {
    for (const n of [1, 2, 40]) {
      it(`stays within 20 marks with ${n} providers and ${unknown} requests not reported`, () => {
        const m = offeringModel(providers(n), { ready: 1, unknown });
        const nodes = m.named.length + (m.more ? 1 : 0) + 1 + 5 + (unknown ? 1 : 0);
        const wireMarks = m.wires.filter((w) => w.from !== 'pm' || m.more).reduce((k, w) => k + 1 + (w.head ? 1 : 0), 0);
        expect(nodes + wireMarks).toBeLessThanOrEqual(20);
        expect(m.named.length + m.more).toBe(n);
      });
    }
  }
  it('draws direction on every wire that moves a request: the hub into the track and the track (C13b)', () => {
    const m = offeringModel(providers(2), {});
    expect(m.wires.filter((w) => w.head).map((w) => w.id)).toEqual(['whub', 'wt0', 'wt1', 'wt2', 'wfail']);
    // Providers belong to the offering: no arrowhead.
    expect(m.wires.filter((w) => /^p(\d|m)$/.test(w.from)).every((w) => !w.head)).toBe(true);
  });
  it('never tones Not reported ok, at any count (G3)', () => {
    for (const unknown of [0, 1, 400]) {
      const st = offeringModel(providers(1), { unknown }).stations.find((s) => s.key === 'unknown')!;
      expect(st.tone).not.toBe('ok');
      expect(st.hatch).toBe(unknown > 0);
    }
  });
});

describe('useDetailModel (A6, G2)', () => {
  for (const phase of ['Requested', 'Provisioning', 'Ready', 'Failed', 'Deleting']) {
    it(`tells a ${phase} request in exactly one step, with no signal`, () => {
      const m = useDetailModel({ phase }, 'at Nov 5, 23:13');
      expect(m.steps).toHaveLength(1);
      expect(m.steps.flatMap((s) => s.sig ?? [])).toEqual([]);
      const cur = m.stations.findIndex((s) => s.label === phase);
      expect(m.steps[0].show).toEqual([`[data-m="f${cur}"]`]);
      expect(m.steps[0].cap).toBe(`${phase}: reported at Nov 5, 23:13.`);
      // Earlier stations are at rest and neutral; nothing past the current one is reached.
      expect(m.stations.slice(0, cur).every((s) => s.tone === 'neutral' && s.sub === 'came before')).toBe(true);
      expect(m.stations.slice(cur + 1).every((s) => !s.reached)).toBe(true);
      expect(timeline(m.steps).total).toBeLessThanOrEqual(1200);
    });
  }
  it('tones the current station by its phase and never says ok for unknown', () => {
    expect(useDetailModel({ phase: 'Ready' }, 'x').stations.map((s) => s.tone)).toEqual(['neutral', 'neutral', 'ok']);
    expect(useDetailModel({ phase: 'Failed' }, 'x').stations.map((s) => s.label)).toEqual(['Requested', 'Provisioning', 'Failed']);
    const unk = useDetailModel({}, 'x');
    expect(unk.steps).toEqual([]);
    expect(unk.stations.every((s) => s.tone === 'neutral' && s.hatch)).toBe(true);
    expect(unk.wires.every((w) => w.style === 'proj')).toBe(true);
    expect(unk.alt).toBe('unknown.');
  });
  it('keys the session on the phase and the request, never the time', () => {
    expect(sessionKey(useDetailModel({ phase: 'Ready', environment: 'prod' }, 'at 1').key)).toBe(sessionKey(useDetailModel({ phase: 'Ready', environment: 'prod', asOf: 'later' }, 'at 2').key));
    expect(useDetailModel({ phase: 'Ready', environment: 'prod', entry: 'db', verified: false }, 'x').summary).toContain('not signed');
  });
});

describe('evidenceModel (G3)', () => {
  const src = (k: string, status?: string) => ({ key: k, label: k, status });
  it('names at most three sources and styles each wire by its state, never ok for unknown', () => {
    const m = evidenceModel({ total: 40, items: [src('a', 'unavailable'), src('b', 'stale'), src('c'), src('d', 'available')] }, { available: 39 });
    expect(m.marks).toHaveLength(MAX_EVIDENCE_MARKS);
    expect(m.marks.map((mk) => `${mk.style}|${mk.tone}`)).toEqual(['broken|err', 'stale|warn', 'stale|neutral']);
    expect(m.more).toBe(37);
    expect(m.restCurrent).toBe(false);
    expect(m.wires.find((w) => w.id === 'wem')!.style).toBe('stale');
  });
  it('draws the rest solid only when every one of them is current', () => {
    const m = evidenceModel({ total: 5, items: [src('a', 'available')] }, { available: 5 });
    expect(m.restCurrent).toBe(true);
    expect(m.wires.find((w) => w.id === 'wem')!.style).toBe('obs');
    expect(evidenceModel(undefined, undefined).wires).toEqual([]);
  });
});

describe('serviceStackModel current revision', () => {
  it('names the revision in use when it is folded into the plinth', () => {
    const items = Array.from({ length: 7 }, (_, i) => ref(`r${i}`));
    const m = serviceStackModel({ total: 7, items }, 'r6', label, items[6]);
    expect(m.currentFolded).toBe(true);
    expect(m.current).toBe('r6');
    expect(m.alt).toContain('the current r6 among them');
    expect(serviceStackModel({ total: 7, items }, 'r1', label, items[1]).currentFolded).toBe(false);
  });
});
