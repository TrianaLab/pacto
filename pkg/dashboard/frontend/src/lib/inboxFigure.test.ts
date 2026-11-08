import { describe, it, expect } from 'vitest';
import { inboxModel, KINDS, MAX_STATIONS } from './inboxFigure.ts';
import { timeline, sessionKey } from './narrate.ts';

const env = (environment: string, errored = 0, held = 0, frozen = 0, upstream: string[] = []) => ({ environment, errored, held, frozen, upstream });
const envs = (n: number) => Array.from({ length: n }, (_, i) => env(`env-${i}`, i % 3, (i * 7) % 5, i % 2, i ? [`env-${i - 1}`] : []));

describe('inboxModel (G3)', () => {
  it('caps the stations and keeps every count in some station', () => {
    for (const n of [1, 8, 40, 400]) {
      const all = envs(n);
      const m = inboxModel(all);
      expect(m.stations.length, `N=${n}`).toBeLessThanOrEqual(MAX_STATIONS);
      for (const k of KINDS) {
        expect(m.stations.reduce((a, s) => a + s[k.key], 0), `${k.key} N=${n}`).toBe(all.reduce((a, e) => a + e[k.key], 0));
      }
      // S2: stations and arrow wires (a path and a head each) stay within 20 marks.
      expect(m.stations.length + 2 * m.wires.length).toBeLessThanOrEqual(20);
    }
  });

  it('keeps the most stuck environments in promotion order and folds the rest into one station that lists exactly them', () => {
    const all = [env('a', 0, 0, 1), env('b', 5), env('c', 0, 1), env('d'), env('e', 2), env('f', 0, 0, 2), env('g', 1), env('h', 0, 3)];
    const m = inboxModel(all);
    expect(m.stations.map((s) => s.key)).toEqual(['b', 'c', 'e', 'f', 'g', 'h', '+']);
    expect(m.stations[6]).toMatchObject({ label: '2 more environments', env: '', frozen: 1, except: ['b', 'c', 'e', 'f', 'g', 'h'] });
    expect(m.figRef).toBe('The queues share one scale; the 2 more environments are counted, not drawn.');
    // The folded station is off the scale and out of the story.
    expect(inboxModel([...all, env('i', 99)]).max).toBe(99);
    expect(inboxModel([env('z', 1), ...Array.from({ length: 7 }, (_, i) => env(`y${i}`, 0, 0, 1))]).steps.flatMap((s) => s.show)).toEqual(['[data-m="qb0errored"]']);
  });

  it('draws an arrow only between neighbours the Stages link', () => {
    const m = inboxModel([env('test', 1, 0, 0), env('prod', 0, 1, 0, ['test']), env('sandbox', 0, 1)]);
    expect(m.wires.map((w) => [w.from, w.to, w.head])).toEqual([['p0-r', 'p1-l', true]]);
    expect(m.figRef).toContain('An arrow joins');
    expect(inboxModel([env('a', 1), env('b', 1)]).figRef).toBe('The queues share one scale.');
  });

  it('names the Stages the caller cannot read and never filters to them', () => {
    expect(inboxModel([env('', 0, 2)]).stations[0]).toMatchObject({ label: 'Stages you cannot read', env: '' });
  });

  it('shares one scale across every bar', () => {
    expect(inboxModel([env('a', 13, 4), env('b', 0, 0, 2)]).max).toBe(13);
    expect(inboxModel([env('a')]).max).toBe(1);
  });
});

describe('inboxModel narration (G2)', () => {
  it('reveals the errored bars, then the held, and never frozen', () => {
    const m = inboxModel([env('test', 1, 2), env('prod', 3, 0, 4, ['test'])]);
    expect(m.steps.map((s) => s.show)).toEqual([['[data-m="qb0errored"]', '[data-m="qb1errored"]'], ['[data-m="qb0held"]']]);
    expect(m.steps[0].cap).toBe('4 changes errored: 1 waiting to enter test, 3 waiting to enter prod.');
    expect(timeline(m.steps).total).toBeLessThanOrEqual(1200);
    for (const s of m.steps) expect(s.sig ?? []).toEqual([]);
  });

  it('stays still when only frozen Stages wait', () => {
    expect(inboxModel([env('prod', 0, 0, 3)]).steps).toEqual([]);
  });

  it('keys the story on errored and held only', () => {
    const a = inboxModel([env('prod', 1, 2, 0)]), b = inboxModel([env('prod', 1, 2, 9)]), c = inboxModel([env('prod', 2, 2, 0)]);
    expect(sessionKey(a.key)).toBe(sessionKey(b.key));
    expect(sessionKey(a.key)).not.toBe(sessionKey(c.key));
  });

  it('gives the three totals once, in words', () => {
    expect(inboxModel([env('test', 1, 2), env('prod', 0, 0, 1)]).caption).toBe('1 errored change, 2 held changes and 1 frozen Stage wait for you.');
  });

  it('says when a cycle of upstream links leaves environments placed by name (SPEC 21)', () => {
    const cyc = (e: ReturnType<typeof env>) => ({ ...e, cycle: true });
    expect(inboxModel([env('test', 1), cyc(env('a', 1, 0, 0, ['b'])), cyc(env('b', 1, 0, 0, ['a']))]).caption)
      .toBe('3 errored changes, 0 held changes and 0 frozen Stages wait for you. The Stages of a and b feed each other in a cycle, so they are placed by name.');
    expect(inboxModel([cyc(env('a', 1, 0, 0, ['a']))]).caption).toContain('The Stages of a feed each other in a cycle, so it is placed by name.');
  });
});
