/**
 * The Change analysis figure's pure model (SPEC 7, flagship): the answer to "which
 * consumers does this change break, and does the gate let it through?", as lanes.
 *
 * It reads only what the backend computed over EVERY consumer -- byOutcome, refused,
 * direct, hidden, total and the ranked highlights -- and never `items`, so the figure is
 * the same on every page of the table. ImpactFigure.svelte renders what this returns;
 * the caption and the wires come from the same lanes, so copy cannot drift from the
 * drawing.
 */
import type { ProductImpact } from './api.ts';
import { outcomeSegments } from './distributions.ts';
import { countOf, formatCount, shortDigest } from './format.ts';
import type { FigTone, GlyphName, WireDef } from './figure.ts';
import type { Step } from './narrate.ts';

type Consumer = NonNullable<ProductImpact['consumers']['highlights']>[number];
export type Outcome = 'incompatible' | 'unknown' | 'compatible' | 'unclassified';
export const OUTCOMES: Outcome[] = ['incompatible', 'unknown', 'compatible', 'unclassified'];

/** outcomeOf mirrors pkg/impact outcomeOf: a refused guarantee breaks whatever the range says. */
export function outcomeOf(c: Pick<Consumer, 'unmet' | 'compatibilityVerdict'>): Outcome {
  if (c.unmet?.length || c.compatibilityVerdict === 'incompatible') return 'incompatible';
  if (c.compatibilityVerdict === 'unknown') return 'unknown';
  if (c.compatibilityVerdict === 'compatible') return 'compatible';
  return 'unclassified';
}

export function classificationLabel(c: string | undefined | null): string {
  const t = String(c || 'unknown').replace(/_/g, ' ').toLowerCase();
  return t.charAt(0).toUpperCase() + t.slice(1);
}

/** The release gate `pacto impact` exits on: blocked (1), unverified (2), clear (0). */
const IMPACT_GATE: Record<string, { cls: string; label: string; tone: FigTone; stamp: string; glyph: GlyphName; says: string }> = {
  blocked: { cls: 'badge-err', label: 'Blocked', tone: 'err', stamp: 'Blocked', glyph: 'err', says: 'The release gate blocks it' },
  unverified: { cls: 'badge-warn', label: 'Unverified', tone: 'warn', stamp: 'Needs a person', glyph: 'held', says: 'The release gate waits for a person' },
  clear: { cls: 'badge-ok', label: 'Clear', tone: 'ok', stamp: 'Clears', glyph: 'ok', says: 'The release gate lets it through' },
};
const CLASS_TONE: Record<string, FigTone> = { BREAKING: 'err', POTENTIAL_BREAKING: 'warn', NON_BREAKING: 'ok' };
const GLYPH: Record<Outcome, GlyphName> = { incompatible: 'err', unknown: 'unknown', compatible: 'ok', unclassified: 'none' };

interface LaneChip { key: string; label: string; href: string; ring: boolean }
export interface ImpactLane {
  key: Outcome | Effect | 'hidden' | 'coverage';
  label: string;
  tone: FigTone;
  hatch: boolean;
  glyph: GlyphName;
  n: number;
  /** Share of the total, 0 to 1; the hidden lane has none (it is in no total). */
  share: number;
  chips: LaneChip[];
  decides: boolean;
  ghost: boolean;
}

/** Which lanes decide the gate (SPEC 7 r3/r4): only those reach it, the others draw no wire. */
function decidingLanes(gate: string, classification: string, by: Record<Outcome, number>): Outcome[] {
  if (gate === 'blocked') return ['incompatible'];
  if (gate === 'unverified') {
    const waits = (['incompatible', 'unknown', 'unclassified'] as Outcome[]).filter((k) => by[k] > 0);
    if (waits.length) return waits;
    return by.compatible > 0 && classification === 'BREAKING' ? ['compatible'] : [];
  }
  return [];
}

export function impactModel(im: ProductImpact) {
  const cs = im.consumers;
  const total = cs.total ?? 0, direct = cs.direct ?? 0, hidden = cs.hidden ?? 0, refused = cs.refused ?? 0;
  const partial = (im.meta?.completeness ?? 'complete') !== 'complete';
  const unavailable = im.meta?.sourceCounts?.unavailable ?? 0;
  const by = Object.fromEntries(OUTCOMES.map((k) => [k, 0])) as Record<Outcome, number>;
  for (const b of cs.byOutcome ?? []) if (OUTCOMES.includes(b.key as Outcome)) by[b.key as Outcome] = b.count ?? 0;
  const segs = outcomeSegments(OUTCOMES.map((key) => ({ key, count: by[key] })));
  const gateKey = im.gate || '';
  const gate = IMPACT_GATE[gateKey] ?? null;
  const decides = decidingLanes(gateKey, im.classification, by);

  // The one ring of the page: the top-ranked blocking consumer, only when the gate blocks.
  const ringKey = gateKey === 'blocked' ? (cs.highlights ?? []).find((c) => c.blocking)?.service.key : undefined;
  const chipsOf = (k: Outcome) => (cs.highlights ?? []).filter((c) => outcomeOf(c) === k).slice(0, 4)
    .map((c) => ({ key: c.service.key, label: c.service.label || c.service.key, href: c.service.href || '', ring: c.service.key === ringKey }));

  const lanes: ImpactLane[] = [];
  OUTCOMES.forEach((k, i) => {
    if (k === 'unclassified' && !by[k]) return; // a shortfall lane only when there is one
    if (k === 'unclassified' && hidden) lanes.push(hiddenLane(hidden));
    const s = segs[i];
    lanes.push({ key: k, label: s.label, tone: s.tone as FigTone, hatch: !!s.hatch, glyph: GLYPH[k], n: by[k], share: total ? by[k] / total : 0, chips: chipsOf(k), decides: decides.includes(k), ghost: false });
  });
  if (hidden && !lanes.some((l) => l.ghost)) lanes.push(hiddenLane(hidden));
  const gateLane = lanes.findIndex((l) => l.decides);

  const deciding = lanes.filter((l) => l.decides).map((l) => l.label.toLowerCase());
  const gateWhy = deciding.length > 1 ? `Release gate, decided by the ${deciding.join(' and ')} lanes`
    : deciding.length ? 'Release gate, decided by this lane'
      : im.classification !== 'BREAKING' ? `Release gate, decided by the classification: it holds only breaking changes`
        : total ? 'Release gate, decided by incomplete evidence' : 'Release gate: no consumer depends on this service';

  const service = im.service?.label || im.service?.key || '';
  const from = im.oldRevision?.version || im.oldRevision?.label || '?';
  const to = im.newRevision?.version || im.newRevision?.label || '?';
  const atLeast = partial ? 'at least ' : '';
  const missing = unavailable ? `the ${countOf(unavailable, 'unavailable source')}` : 'sources Pacto cannot read';

  const wires: WireDef[] = [{ id: 'w-stack', from: 'next', to: 'split', style: 'proj', tone: 'accent', when: ['wide', 'mid', 'narrow'] }];
  lanes.forEach((l, k) => wires.push({
    id: `w-l${k}`, from: 'split', to: `ln-${k}`, style: l.ghost ? 'stale' : 'proj', tone: l.hatch || l.ghost ? 'neutral' : l.tone,
    col: { fs: 'b', ts: 'l' },
  }));
  if (gateLane >= 0) {
    const l = lanes[gateLane];
    wires.push({ id: 'w-gate', from: `ln-${gateLane}`, fs: 'b', to: 'gate', ts: 't', style: 'proj', tone: l.hatch ? 'neutral' : l.tone });
  }

  const laneIds = lanes.map((_, k) => k);
  const steps: Step[] = [
    { cap: `${to} leaves ${service}. This is a projection: nothing has been published.`, dur: 240, show: ['[data-m="next"]', 'w:w-stack'], sig: ['w-stack'] },
    { cap: `It reaches the ${countOf(total, 'consumer')} that depend on ${service}.`, dur: 160, show: ['[data-m="split"]'] },
    { cap: `Each consumer is sorted by whether the range it accepts includes ${to}.`, dur: 240, show: [...laneIds.map((k) => `[data-m="ln-${k}"]`), ...laneIds.map((k) => `w:w-l${k}`)], sig: laneIds.map((k) => `w-l${k}`) },
  ];
  if (gate) {
    steps.push({ cap: `${gate.says}${by.incompatible ? `: ${countOf(by.incompatible, 'consumer')} would break` : ''}.`, dur: 160, show: ['[data-m="gate"]', 'w:w-gate'], sig: ['w-gate'] });
  }

  const gateSentence = gate ? `${gate.says}.` : '';
  const figRef = gateLane >= 0
    ? `The gate is drawn under the ${deciding.join(' and ')} ${deciding.length > 1 ? 'lanes, which decide' : 'lane, which decides'} it; the other lanes do not reach it.`
    : gate ? 'No lane reaches the gate.' : '';
  const caption = `it breaks ${formatCount(by.incompatible)} of ${atLeast}${countOf(total, 'consumer')}`
    + (by.unknown ? `; ${formatCount(by.unknown)} ${by.unknown === 1 ? 'is' : 'are'} unknown` : '')
    + (by.unclassified ? ` and ${formatCount(by.unclassified)} unclassified` : '')
    + (hidden ? `; ${formatCount(hidden)} more you may not see ${hidden === 1 ? 'is' : 'are'} not counted` : '')
    + `. ${gateSentence}`
    + (partial ? ` Coverage is partial: consumers known only to ${missing} are missing, so every share ends open.` : '');
  const alt = `If ${service} ${to} is published: ${OUTCOMES.map((k) => `${by[k]} ${k}`).join(', ')} of ${atLeast}${total} consumers`
    + `; ${hidden} you may not see; gate ${gate ? gate.label.toLowerCase() : 'none'}; evidence ${partial ? 'partial' : 'complete'}.`;

  return {
    service, from, to, total, direct, indirect: Math.max(0, total - direct), hidden, refused, partial, missing, by,
    lanes, gateLane, gate, gateWhy, wires, steps, caption, figRef, alt,
    classification: { label: classificationLabel(im.classification), tone: CLASS_TONE[im.classification] ?? 'neutral' as FigTone },
    // The session key covers only what the steps animate: the pair and the lanes.
    key: { s: im.service?.key, f: im.oldRevision?.key, t: im.newRevision?.key, by, hidden, gate: gateKey },
  };
}

/** What an event does to a consumer (pkg/impact Effect*), in D3's lane order. */
export type Effect = 'breaks' | 'may-degrade' | 'unknown';
export const EFFECTS: Effect[] = ['breaks', 'may-degrade', 'unknown'];
/** effectOf mirrors the Go effect; anything else is unknown, never compatible. */
export function effectOf(c: { effect?: string | null }): Effect {
  return EFFECTS.includes(c.effect as Effect) ? (c.effect as Effect) : 'unknown';
}
export const EFFECT_META: Record<Effect, { label: string; tone: FigTone; hatch: boolean; glyph: GlyphName }> = {
  breaks: { label: 'Breaks', tone: 'err', hatch: false, glyph: 'err' },
  'may-degrade': { label: 'May degrade', tone: 'warn', hatch: false, glyph: 'warn' },
  unknown: { label: 'Unknown', tone: 'neutral', hatch: true, glyph: 'unknown' },
};
// An event has no version a consumer can refuse, so its gate never blocks (Decision 7).
const EVENT_GATE: Record<string, { cls: string; label: string; tone: FigTone; stamp: string; glyph: GlyphName; says: string }> = {
  unverified: { ...IMPACT_GATE.unverified, says: 'Needs a person' },
  clear: { ...IMPACT_GATE.clear, says: 'Clears' },
};

/** What happens to the service: "becomes unavailable", "is removed", "regressed at 1.2.0". */
function eventVerb(im: ProductImpact): string {
  const rev = im.event?.revision || '';
  const at = rev.startsWith('sha256:') ? `sha256:${shortDigest(rev, 12)}` : rev;
  if (im.event?.kind === 'removed') return 'is removed';
  if (im.event?.kind === 'regressed') return `regressed${at ? ` at ${at}` : ''}`;
  return 'becomes unavailable';
}
/** The event in words: "redis becomes unavailable". */
export function eventPhrase(im: ProductImpact): string {
  return `${im.service?.label || im.service?.key || ''} ${eventVerb(im)}`;
}

/** "a, b and c": no Oxford comma. */
const joinWords = (xs: string[]) => (xs.length > 1 ? `${xs.slice(0, -1).join(', ')} and ${xs.at(-1)}` : xs[0] ?? '');

/**
 * The event figure's model: "if redis becomes unavailable, who breaks?". Lanes by
 * effect (Breaks, May degrade, Unknown), the ghost of what the reader may not see and,
 * when the answer is partial, the hatched lane of the sources Pacto could not read.
 * Every non-empty consumer lane and that coverage lane send the gate to a person; the
 * gate sits under the first of them, or under the empty answer when it clears.
 */
export function eventModel(im: ProductImpact) {
  const cs = im.consumers;
  const total = cs.total ?? 0, direct = cs.direct ?? 0, hidden = cs.hidden ?? 0;
  const partial = (im.meta?.completeness ?? 'complete') !== 'complete';
  const unavailable = im.meta?.sourceCounts?.unavailable ?? 0;
  const by = Object.fromEntries(EFFECTS.map((k) => [k, 0])) as Record<Effect, number>;
  for (const b of cs.byOutcome ?? []) if (EFFECTS.includes(b.key as Effect)) by[b.key as Effect] = b.count ?? 0;
  const gateKey = im.gate === 'clear' ? 'clear' : im.gate ? 'unverified' : '';
  const gate = EVENT_GATE[gateKey] ?? null;
  const unverified = gateKey === 'unverified';
  const chipsOf = (k: Effect) => (cs.highlights ?? []).filter((c) => effectOf(c) === k).slice(0, 4)
    .map((c) => ({ key: c.service.key, label: c.service.label || c.service.key, href: c.uncontracted ? '' : c.service.href || '', ring: false }));

  const lanes: ImpactLane[] = EFFECTS.map((k) => ({
    key: k, label: EFFECT_META[k].label, tone: EFFECT_META[k].tone, hatch: EFFECT_META[k].hatch, glyph: EFFECT_META[k].glyph,
    n: by[k], share: total ? by[k] / total : 0, chips: chipsOf(k), decides: unverified && by[k] > 0, ghost: false,
  }));
  if (hidden) lanes.push(hiddenLane(hidden));
  if (partial) lanes.push({ key: 'coverage', label: 'Unread sources', tone: 'neutral', hatch: true, glyph: 'unknown', n: 0, share: 0, chips: [], decides: unverified, ghost: true });
  const gateLane = lanes.findIndex((l) => l.decides);
  const deciding = lanes.filter((l) => l.decides).map((l) => l.label.toLowerCase());

  const service = im.service?.label || im.service?.key || '';
  const phrase = eventPhrase(im);
  const atLeast = partial ? 'at least ' : '';
  const missing = unavailable ? `the ${countOf(unavailable, 'unavailable source')}` : 'sources Pacto cannot read';
  // "5 consumers break, 3 may degrade and 2 are unknown": the first count names consumers.
  const VERB: Record<Effect, [string, string]> = { breaks: ['breaks', 'break'], 'may-degrade': ['may degrade', 'may degrade'], unknown: ['is unknown', 'are unknown'] };
  const parts = EFFECTS.filter((k) => by[k]).map((k, i) => `${i ? formatCount(by[k]) : countOf(by[k], 'consumer')} ${VERB[k][by[k] === 1 ? 0 : 1]}`);
  // Even a complete answer only knows contracts and observed calls: the Go clear
  // reason's hedge is carried here.
  const caption = (total ? `${atLeast}${joinWords(parts)}` : partial ? 'no consumer is known to depend on it'
    : 'no contract declares a dependency on it and no source saw a caller; a service with no contract can still call it')
    + (hidden ? `; ${formatCount(hidden)} more you may not see ${hidden === 1 ? 'is' : 'are'} not counted` : '')
    + '.'
    + (partial ? ` Coverage is partial: consumers known only to ${missing} are missing.` : '');
  const figRef = gateLane < 0 ? (gate ? 'No lane reaches the gate.' : '')
    : deciding.length === 1 ? `The gate is drawn under the ${deciding[0]} lane, which sends it to a person.`
      : `The gate is drawn under the ${deciding[0]} lane; the ${joinWords(deciding)} lanes ${deciding.length === 2 ? 'both' : 'all'} send it to a person.`;
  const gateWhy = deciding.length > 1 ? `Gate, decided by the ${joinWords(deciding)} lanes`
    : deciding.length ? 'Gate, decided by this lane' : `Gate: nothing known depends on ${service}`;

  const wires: WireDef[] = [{ id: 'w-stack', from: 'subject', to: 'split', style: 'proj', tone: 'neutral', when: ['wide', 'mid', 'narrow'] }];
  lanes.forEach((l, k) => wires.push({
    id: `w-l${k}`, from: 'split', to: `ln-${k}`, style: l.ghost ? 'stale' : 'proj', tone: l.hatch || l.ghost ? 'neutral' : l.tone,
    col: { fs: 'b', ts: 'l' },
  }));
  if (gateLane >= 0) {
    const l = lanes[gateLane];
    wires.push({ id: 'w-gate', from: `ln-${gateLane}`, fs: 'b', to: 'gate', ts: 't', style: 'proj', tone: l.hatch ? 'neutral' : l.tone });
  }
  const laneIds = lanes.map((_, k) => k);
  const steps: Step[] = [
    { cap: `Suppose ${phrase}. This is a question: nothing has changed.`, dur: 240, show: ['[data-m="subject"]', 'w:w-stack'], sig: ['w-stack'] },
    { cap: `It reaches ${atLeast || 'the '}${countOf(total, 'consumer')} that depend on ${service}.`, dur: 160, show: ['[data-m="split"]'] },
    { cap: `Each consumer is sorted by whether every path to ${service} is required.`, dur: 240, show: [...laneIds.map((k) => `[data-m="ln-${k}"]`), ...laneIds.map((k) => `w:w-l${k}`)], sig: laneIds.map((k) => `w-l${k}`) },
  ];
  if (gate) steps.push({ cap: `${gate.says}${by.breaks ? `: ${countOf(by.breaks, 'consumer')} would break` : ''}.`, dur: 160, show: ['[data-m="gate"]', 'w:w-gate'], sig: ['w-gate'] });
  const alt = `If ${phrase}: ${EFFECTS.map((k) => `${by[k]} ${k}`).join(', ')} of ${atLeast}${total} consumers`
    + `; ${hidden} you may not see; gate ${gate ? gate.label.toLowerCase() : 'none'}; evidence ${partial ? 'partial' : 'complete'}.`;

  return {
    service, lead: `If ${phrase}`, verb: eventVerb(im), total, direct, indirect: Math.max(0, total - direct), hidden, refused: 0, partial, missing, by,
    lanes, gateLane, gate, gateWhy, wires, steps, caption, figRef, alt,
    key: { s: im.service?.key, e: im.event?.kind, r: im.event?.revision, by, hidden, gate: gateKey },
  };
}

/**
 * What an event answer with no listed consumer may say: never "no consumer is
 * affected", since a service with no contract can still call the subject, and a
 * partial or restricted answer only speaks for what the viewer can see.
 */
function noEventConsumer(im: ProductImpact): string {
  const hidden = im.consumers?.hidden ?? 0;
  if ((im.meta?.completeness ?? 'complete') === 'complete' && !hidden) return 'No consumer is known to depend on it.';
  return `No consumer you can see is known to depend on it${hidden ? `; ${formatCount(hidden)} you may not see ${hidden === 1 ? 'is' : 'are'} not counted` : ''}.`;
}

function hiddenLane(n: number): ImpactLane {
  return { key: 'hidden', label: 'Hidden from you', tone: 'neutral', hatch: true, glyph: 'unknown', n, share: 0, chips: [], decides: false, ghost: true };
}

/**
 * The card's lead line: the diff in one line. Who it reaches is the lanes' fact, drawn
 * once from the outcome tally (L3: one carrier per fact), so the line never restates
 * it from a second tally.
 */
export function answerText(im: ProductImpact): string {
  if (im.event) return `A question about the fleet as it is: nothing has changed.${im.consumers?.total ? '' : ` ${noEventConsumer(im)}`}`;
  const ch = im.changes;
  const what = !ch?.total ? 'No contract differences.' : `${countOf(ch.total, 'change')}, ${formatCount(ch.breaking ?? 0)} breaking.`;
  return im.consumers?.total ? what : `${what} No consumer is affected.`;
}
