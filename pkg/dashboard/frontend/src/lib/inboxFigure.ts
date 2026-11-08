/**
 * The Delivery Inbox figure's pure model (SPEC 7): "Where are changes stuck on their way
 * to production?". One flat neutral Station per destination environment, in the
 * promotion order the Stages declare (the server's byEnvironment, never the names);
 * under each, the queue waiting to enter it, one bar per kind on one shared scale (S9).
 *
 * Every count comes from byEnvironment, which is over every page of the inbox, so paging
 * the lists never changes the drawing. An arrow joins two neighbouring stations only
 * when the Stages of the second are fed by the first: across many services there is no
 * one order (SPEC 21), so the drawing claims only the links the Stages declare.
 */
import type { DeliveryInbox } from './api.ts';
import { countOf, formatCount } from './format.ts';
import { andList } from './delivery.ts';
import type { FigTone, GlyphName, WireDef } from './figure.ts';
import type { Step } from './narrate.ts';

type Env = NonNullable<DeliveryInbox['byEnvironment']>[number];
export type Kind = 'errored' | 'held' | 'frozen';
/** Canonical order (SPEC 2.2): err, warn, then frozen, which is information. */
export const KINDS: { key: Kind; tone: FigTone; glyph: GlyphName; word: string }[] = [
  { key: 'errored', tone: 'err', glyph: 'err', word: 'errored' },
  { key: 'held', tone: 'warn', glyph: 'held', word: 'held' },
  { key: 'frozen', tone: 'frost', glyph: 'frozen', word: 'frozen' },
];

/**
 * Stations per figure. ponytail: 7, not the spec's 8, because 8 stations and their 7
 * arrows are 22 marks, over the 20-mark cap (S2); the eighth slot is the "N more
 * environments" station. Tiers would order by name, which SPEC 21 forbids.
 */
export const MAX_STATIONS = 7;

export interface InboxStation {
  key: string;
  label: string;
  /** The environment this station filters the lists to; '' when no filter lists it. */
  env: string;
  /** The folded station's filter: every environment but these (the ones drawn). */
  except?: string[];
  errored: number;
  held: number;
  frozen: number;
}

export function inboxModel(byEnv: Env[]) {
  let envs = byEnv;
  let rest: Env[] = [];
  if (envs.length > MAX_STATIONS) {
    // Keep the most stuck, worst first, and show them in promotion order.
    const keep = new Set([...envs].sort((a, b) => b.errored - a.errored || b.held - a.held || b.frozen - a.frozen).slice(0, MAX_STATIONS - 1));
    rest = envs.filter((e) => !keep.has(e));
    envs = envs.filter((e) => keep.has(e));
  }
  const stations: InboxStation[] = envs.map((e) => ({
    key: e.environment || '-', label: e.environment || 'Stages you cannot read', env: e.environment,
    errored: e.errored, held: e.held, frozen: e.frozen,
  }));
  if (rest.length) {
    const sum = (k: Kind) => rest.reduce((n, e) => n + e[k], 0);
    stations.push({ key: '+', label: `${countOf(rest.length, 'more environment')}`, env: '', except: envs.map((e) => e.environment), errored: sum('errored'), held: sum('held'), frozen: sum('frozen') });
  }
  // One scale for the environments drawn one by one. The folded station sums many, so
  // it prints its counts without bars: on the same scale it would flatten every other.
  const max = Math.max(1, ...stations.filter((s) => s.key !== '+').flatMap((s) => KINDS.map((k) => s[k.key])));
  const fed = (i: number) => i + 1 < envs.length && !!envs[i].environment && (envs[i + 1].upstream ?? []).includes(envs[i].environment);
  const wires: WireDef[] = stations.slice(1).flatMap((_, i) => fed(i) ? [{
    id: `wi${i}`, from: `p${i}-r`, fs: 'c' as const, to: `p${i + 1}-l`, ts: 'c' as const, col: { from: `p${i}-b`, to: `p${i + 1}-t` },
    style: 'obs' as const, tone: 'neutral' as const, head: true,
  }] : []);

  const total = (k: Kind) => byEnv.reduce((n, e) => n + e[k], 0);
  // A2 (r5): a passive arrival reveals only err and warn: the errored bars, then the held.
  const steps: Step[] = [];
  for (const k of KINDS.slice(0, 2)) {
    const at = stations.map((s, i) => (s[k.key] && s.key !== '+' ? i : -1)).filter((i) => i >= 0);
    if (!at.length) continue;
    steps.push({
      cap: `${countOf(total(k.key), 'change')} ${k.word}: ${at.map((i) => `${formatCount(stations[i][k.key])} waiting to enter ${stations[i].label}`).join(', ')}.`,
      dur: 240, show: at.map((i) => `[data-m="qb${i}${k.key}"]`),
    });
  }
  // SPEC 21: a cycle of upstream links gives its environments no place; say so.
  const cyc = byEnv.filter((e) => e.cycle).map((e) => e.environment);
  const caption = [`${countOf(total('errored'), 'errored change')}, ${countOf(total('held'), 'held change')} and ${countOf(total('frozen'), 'frozen Stage')} wait for you.`,
    cyc.length ? `The Stages of ${andList(cyc, 3)} feed each other in a cycle, so ${cyc.length === 1 ? 'it is' : 'they are'} placed by name.` : ''].filter(Boolean).join(' ');
  return {
    stations, max, wires, steps, caption,
    figRef: [wires.length ? 'An arrow joins two environments when the Stages of the second are fed by the first.' : '',
      `The queues share one scale${rest.length ? `; the ${countOf(rest.length, 'more environment')} are counted, not drawn` : ''}.`].filter(Boolean).join(' '),
    alt: stations.map((s) => `waiting to enter ${s.label}: ${s.errored} errored, ${s.held} held, ${s.frozen} frozen`).join('; '),
    // The key covers only what the steps animate: errored and held per station.
    key: stations.map((s) => [s.key, s.errored, s.held]),
  };
}
