import { describe, it, expect } from 'vitest';
import { autonomyModel } from './autonomyFigure.ts';

const byLevel = [{ level: 'observe', stages: 2 }, { level: 'advise', stages: 10 }, { level: 'auto-nonbreaking', stages: 0 }, { level: 'autonomous', stages: 1 }, { level: '', stages: 3 }];

describe('autonomyModel', () => {
  it('draws one card per level on one shared scale and keeps no level apart (S9)', () => {
    const m = autonomyModel(byLevel, { count: 3, of: 5, untyped: 2 });
    expect(m.cards.map((c) => [c.label, c.n])).toEqual([['Observe', 2], ['Advise', 10], ['Auto non-breaking', 0], ['Autonomous', 1]]);
    expect(m.max).toBe(10);
    expect(m.none).toBe(3);
    expect(m.approvals).toEqual({ person: 2, auto: 3, unrecorded: 2 });
    expect(m.caption).toBe('16 Stages: 2 observe, 10 advise, 0 auto non-breaking, 1 autonomous and 3 that report no level. 3 of the 5 promotions in the Stages\' last 20 operations ran with no person. 2 operations carry no type, so who approved them is not recorded; that is never counted as automatic.');
  });

  it('says when nothing was promoted', () => {
    const m = autonomyModel(byLevel.slice(0, 4), { count: 0, of: 0, untyped: 1 });
    expect(m.caption).toContain('No promotion succeeded');
    expect(m.caption).toContain('1 operation carries no type, so who approved it is not recorded');
    expect(m.none).toBe(0);
  });
});
