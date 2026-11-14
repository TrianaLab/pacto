import { describe, it, expect } from 'vitest';
import { offeringsPhaseModel } from './offeringsFigure.ts';

const agg = {
  offeringCount: 4,
  offeringPhases: { requested: 1, provisioning: 2, ready: 10, failed: 3, deleting: 0, unknown: 4 },
  offeringPhaseConsumers: { requested: 1, provisioning: 2, ready: 6, failed: 3, deleting: 0, unknown: 2 },
  failedByOffering: [{ type: 'postgresql', services: 2 }, { type: 'redis', services: 1 }],
  failedOfferingsBeyond: 1,
};

describe('offeringsPhaseModel (SPEC 7 Offerings, B3)', () => {
  it('builds the stations from the fleet tally', () => {
    const m = offeringsPhaseModel(agg);
    expect(m.requests).toBe(20);
    expect(m.stations.map((s) => `${s.key}:${s.n}:${s.tone}`)).toEqual([
      'requested:1:info', 'provisioning:2:info', 'ready:10:ok', 'deleting:0:neutral', 'failed:3:err', 'unknown:4:neutral',
    ]);
    expect(m.wires.find((w) => w.id === 'wfail')?.tone).toBe('err');
  });

  it('hatches the not reported station only when it counts something', () => {
    expect(offeringsPhaseModel(agg).stations.find((s) => s.key === 'unknown')?.hatch).toBe(true);
    const none = offeringsPhaseModel({ ...agg, offeringPhases: { ...agg.offeringPhases, unknown: 0 } });
    expect(none.stations.find((s) => s.key === 'unknown')?.hatch).toBe(false);
  });

  it('names the offerings with most failures, the rest as N more, and what the Failed link lists', () => {
    const m = offeringsPhaseModel(agg);
    expect(m.failed.map((c) => `${c.name}:${c.n}`)).toEqual(['postgresql:2', 'redis:1']);
    expect(m.moreFailed).toBe(1);
    // The Failed list counts offerings, while the station counts requests.
    expect(m.failedLists).toBe(3);
  });

  it('generates the caption from the same tally, so it changes with it', () => {
    const m = offeringsPhaseModel(agg);
    expect(m.summary).toBe('Of 20 requests across 4 offerings, 10 are ready, 3 in progress, 3 failed and 0 being removed; 4 have no reported phase, so their readiness is unknown.');
    const other = offeringsPhaseModel({ ...agg, offeringCount: 1, offeringPhases: { ready: 1 } });
    expect(other.summary).toBe('Of 1 request across 1 offering, 1 is ready, 0 in progress, 0 failed and 0 being removed; 0 have no reported phase, so their readiness is unknown.');
    expect(offeringsPhaseModel({ offeringCount: 2 }).summary).toBe('No consumer has asked a provider of these 2 offerings for a resource yet.');
    expect(offeringsPhaseModel(null).failedLists).toBe(0);
  });
});
