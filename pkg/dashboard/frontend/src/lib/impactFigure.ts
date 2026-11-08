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
import { countOf, formatCount } from './format.ts';
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
export const IMPACT_GATE: Record<string, { cls: string; label: string; tone: FigTone; stamp: string; glyph: GlyphName; says: string }> = {
  blocked: { cls: 'badge-err', label: 'Blocked', tone: 'err', stamp: 'Blocked', glyph: 'err', says: 'The release gate blocks it' },
  unverified: { cls: 'badge-warn', label: 'Unverified', tone: 'warn', stamp: 'Needs a person', glyph: 'held', says: 'The release gate waits for a person' },
  clear: { cls: 'badge-ok', label: 'Clear', tone: 'ok', stamp: 'Clears', glyph: 'ok', says: 'The release gate lets it through' },
};
const CLASS_TONE: Record<string, FigTone> = { BREAKING: 'err', POTENTIAL_BREAKING: 'warn', NON_BREAKING: 'ok' };
const GLYPH: Record<Outcome, GlyphName> = { incompatible: 'err', unknown: 'unknown', compatible: 'ok', unclassified: 'none' };

export interface LaneChip { key: string; label: string; href: string; ring: boolean }
export interface ImpactLane {
  key: Outcome | 'hidden';
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

function hiddenLane(n: number): ImpactLane {
  return { key: 'hidden', label: 'Hidden from you', tone: 'neutral', hatch: true, glyph: 'unknown', n, share: 0, chips: [], decides: false, ghost: true };
}

/** The answer in one line: the diff and who it reaches, from the complete tallies. */
export function answerText(im: ProductImpact): string {
  const ch = im.changes;
  const what = !ch?.total ? 'No contract differences.' : `${countOf(ch.total, 'change')}, ${formatCount(ch.breaking ?? 0)} breaking.`;
  const cs = im.consumers;
  if (!cs?.total) return `${what} No consumer is affected.`;
  const v = (k: string) => (cs.byVerdict ?? []).find((b) => b.key === k)?.count ?? 0;
  const to = im.newRevision?.version || im.newRevision?.label || 'the new revision';
  const parts = [
    v('incompatible') ? `${formatCount(v('incompatible'))} ${v('incompatible') === 1 ? 'does' : 'do'} not accept ${to}` : '',
    cs.refused ? `${formatCount(cs.refused)} ${cs.refused === 1 ? 'requires' : 'require'} a guarantee ${to} does not offer` : '',
    v('unknown') ? `${formatCount(v('unknown'))} unknown` : '',
  ].filter(Boolean);
  return `${what} ${countOf(cs.total, 'consumer')} affected${parts.length ? `: ${parts.join(', ')}` : ''}.`;
}
