import { describe, it, expect } from 'vitest';
import { offeringModel, revisionStackModel, serviceStackModel, sourceModel, targetModel, validationModel, MAX_PROVIDER_MARKS } from './entityFigures.ts';
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
    expect(offeringModel(providers(1), { ready: 1 }).wires.find((w) => w.id === 'wfail')!.tone).toBe('neutral');
    // Unknown is never ok-toned.
    expect(m.stations.find((s) => s.key === 'unknown')!.tone).not.toBe('ok');
    expect(m.summary).toBe('Of 10 requests, 6 are ready, 1 in progress, 2 failed and 0 being removed; 1 has no reported phase, so their readiness is unknown.');
  });
  it('says when nobody asked for a resource, and stays within 20 marks', () => {
    const m = offeringModel(providers(40), undefined);
    expect(m.summary).toBe('No consumer has asked a provider for a resource yet.');
    const nodes = MAX_PROVIDER_MARKS + 1 + m.stations.length;
    const wireMarks = m.wires.reduce((n, w) => n + 1 + (w.head ? 1 : 0), 0);
    expect(nodes + wireMarks).toBeLessThanOrEqual(20);
  });
});
