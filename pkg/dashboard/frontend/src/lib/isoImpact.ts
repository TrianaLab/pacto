/**
 * The isometric impact diagram's pure model: the landing hero's 2:1 slab geometry and
 * the layout of ONE change's impact (the changed service on top, its direct consumers
 * below, the lifted summary sheet). IsoImpact.svelte only renders what this returns.
 *
 * It reads `consumers.highlights`, `direct`, `hidden` and `total`, which the backend
 * computes over EVERY consumer, and never `items`: the drawing is the same on every page.
 * The table beside it stays the full, accessible answer.
 */
import type { ProductImpact } from './api.ts';
import { formatCount } from './format.ts';

type Consumer = NonNullable<ProductImpact['consumers']['highlights']>[number];
export type Verdict = 'refused' | 'incompatible' | 'compatible' | 'unknown';
export type Tone = 'err' | 'warn' | 'ok' | 'neutral';

/** slab is a 2:1 diamond of half-width hw and thickness t, as the hero draws it. */
export function slab(cx: number, cy: number, hw: number, t: number) {
  const hh = hw / 2;
  const top = `M${cx} ${cy - hh} L${cx + hw} ${cy} L${cx} ${cy + hh} L${cx - hw} ${cy} Z`;
  return {
    top,
    left: `M${cx - hw} ${cy} L${cx} ${cy + hh} L${cx} ${cy + hh + t} L${cx - hw} ${cy + t} Z`,
    right: `M${cx} ${cy + hh} L${cx + hw} ${cy} L${cx + hw} ${cy + t} L${cx} ${cy + hh + t} Z`,
    edge: `${top} M${cx - hw} ${cy} V${cy + t} L${cx} ${cy + hh + t} L${cx + hw} ${cy + t} V${cy} M${cx} ${cy + hh} V${cy + hh + t}`,
  };
}

/** ringPath is the alarm ring around a slab: its top face, 12 units wider. */
export const ringPath = (cx: number, cy: number, hw: number) => slab(cx, cy, hw + 12, 0).top;

/** fit cuts text to n code points (never a lone surrogate), ending in an ellipsis. */
export function fit(text: string | undefined | null, n: number): string {
  const s = String(text ?? '');
  const a = Array.from(s);
  return a.length <= n ? s : a.slice(0, Math.max(1, n - 1)).join('') + '…';
}

// FIRST STRONG ISOLATE … POP DIRECTIONAL ISOLATE: a bidi control in one identity part
// cannot reorder the part next to it.
const iso = (s: string) => `\u2068${s}\u2069`;

// Characters that fit a width at a font size, sized for Verdana and DejaVu with a margin.
const budget = (w: number, fs: number, bold: boolean) => Math.floor(w / (fs * (bold ? 0.74 : 0.62)));

export function verdictOf(c: Pick<Consumer, 'unmet' | 'compatibilityVerdict'>): Verdict {
  if (c.unmet?.length) return 'refused';
  if (c.compatibilityVerdict === 'incompatible' || c.compatibilityVerdict === 'compatible') return c.compatibilityVerdict;
  return 'unknown';
}

export const TONE: Record<Verdict, Tone> = { refused: 'err', incompatible: 'err', compatible: 'ok', unknown: 'neutral' };
const GLYPH: Record<Tone, string> = { err: '✕', warn: '!', ok: '✓', neutral: '?' };

/** The release gate `pacto impact` exits on: blocked (1), unverified (2), clear (0). */
export const IMPACT_GATE: Record<string, { cls: string; label: string; tone: Tone }> = {
  blocked: { cls: 'badge-err', label: 'Blocked', tone: 'err' },
  unverified: { cls: 'badge-warn', label: 'Unverified', tone: 'warn' },
  clear: { cls: 'badge-ok', label: 'Clear', tone: 'ok' },
};

export function classificationLabel(c: string | undefined | null): string {
  const t = String(c || 'unknown').replace(/_/g, ' ').toLowerCase();
  return t.charAt(0).toUpperCase() + t.slice(1);
}

const STAMP: Record<string, Tone> = { BREAKING: 'err', POTENTIAL_BREAKING: 'warn', NON_BREAKING: 'ok' };
const HERO = { hw: 74, t: 14 };
const SMALL = { hw: 52, t: 10 };
const XS = 12.19; // --text-xs
const SM = 13.13; // --text-sm
const M = 12;

export function layoutImpact(im: ProductImpact, { maxDrawn = 8 } = {}) {
  const cs = im.consumers;
  const drawn = (cs.highlights ?? []).slice(0, maxDrawn);
  const completeness = im.meta?.completeness ?? 'complete';
  const n = drawn.length;
  const placeholder = n === 0 && completeness !== 'complete';
  const slots = n || (placeholder ? 1 : 0);
  const size = n >= 5 ? SMALL : HERO;
  const s = n <= 2 ? 352 : n <= 4 ? 190 : 120;
  const labelW = n <= 2 ? 200 : s - 8;
  const offs = slots === 1 ? [-176] : drawn.map((_, i) => (i - (n - 1) / 2) * s);
  const pad = Math.max(size.hw, labelW / 2);
  const left = Math.max(300, slots ? pad - Math.min(...offs) : 0);
  const right = Math.max(120, slots ? Math.max(...offs) + pad : 0);
  const X0 = left + M;
  const W = X0 + right + M;
  const Yn = 316;
  const Cy = Yn + 122;
  const y0 = Cy + size.hw / 2 + size.t + 20;
  const H = slots ? y0 + 68 + M : Yn + 85 + M + 20;

  const nameB = budget(labelW, XS, true);
  const ownerB = budget(labelW, XS, false);
  const chipB = budget(labelW - 16, XS, false);
  const labels = drawn.map((c) => c.service.label || c.service.key);
  const display = drawn.map((c, i) => (labels.filter((l) => l === labels[i]).length > 1 && c.service.domain ? `${labels[i]} · ${c.service.domain}` : labels[i]));

  let errI = 0;
  let ringed = false;
  const consumers = drawn.map((c, i) => {
    const verdict = verdictOf(c);
    const tone = TONE[verdict];
    const ring = im.gate === 'blocked' && !ringed && !!c.blocking;
    if (ring) ringed = true;
    const fullName = display[i];
    const said = verdict === 'refused' ? `${fullName} refuses a guarantee it requires, range ${c.compatibilityVerdict || 'unknown'}` : `${fullName} ${verdict}`;
    const accepts = c.compatibility ? `accepts ${c.compatibility}` : '';
    return {
      key: c.service.key, cx: X0 + offs[i], cy: Cy, hw: size.hw, t: size.t, y0,
      verdict, tone, glyph: GLYPH[tone], fullName,
      name: fit(fullName, nameB),
      range: c.compatibility ? fit(c.compatibility, chipB) : 'no range',
      rangeAbsent: !c.compatibility,
      owner: c.owner ? `owner ${iso(fit(c.owner, ownerB - 6))}` : 'no owner',
      title: `${verdict === 'refused' ? said : `${fullName}: ${verdict}`}. ${accepts || 'no declared range'}.${c.owner ? ` Owner ${c.owner}.` : ''}${c.note ? ` ${c.note}` : ''}`,
      said: [said, c.note, accepts].filter(Boolean).join(', '),
      flow: tone === 'err', ring, rise: tone === 'err' ? errI++ : -1,
    };
  });

  const service = im.service?.label || im.service?.key || '';
  const oldV = im.oldRevision?.version || '?';
  const newV = im.newRevision?.version || '?';
  const gate = IMPACT_GATE[im.gate];
  const gateWord = gate ? gate.label.toLowerCase() : 'unknown';
  const total = cs.total ?? 0;
  const direct = cs.direct ?? 0;
  const hidden = cs.hidden ?? 0;
  const more = Math.max(0, total - n);
  const both = `${formatCount(total)} · ${formatCount(direct)} direct`;

  const stampLabel = classificationLabel(im.classification);
  const stampTone = STAMP[im.classification] ?? 'neutral';
  const stampText = `${GLYPH[stampTone]} ${stampLabel.toUpperCase()}`;
  const stampFs = Array.from(stampText).length <= 12 ? SM : XS;
  const stampW = Math.round(Array.from(stampText).length * stampFs * 0.82) + 20;

  const counts = `${formatCount(total)} ${total === 1 ? 'consumer' : 'consumers'}, ${formatCount(direct)} direct`
    + (completeness !== 'complete' ? `, evidence ${completeness}` : '')
    + (hidden ? `, ${formatCount(hidden)} you may not see` : '')
    + (placeholder ? ', consumers unknown' : '');
  const entries = consumers.map((c) => c.said);
  if (more) entries.push(`${formatCount(more)} more in Affected consumers`);
  const label = `Impact of ${service} ${oldV} to ${newV}: ${stampLabel.toLowerCase()}. Release gate ${gateWord}. ${counts}.`
    + (entries.length ? ` ${entries.join('; ')}.` : '');

  return {
    W, H, X0, Yn, consumers, more, hidden, label,
    placeholder: placeholder ? { cx: X0 - 176, cy: Cy, hw: HERO.hw, t: HERO.t, y0, label: 'consumers unknown' } : null,
    sheet: {
      x: X0 - 105.6, y: Yn - 199, h: 196,
      title: fit(service, 14), fullTitle: service,
      change: fit(`${oldV} → ${newV}`, 14), fullChange: `${oldV} → ${newV}`,
      consumers: Array.from(both).length <= 14 ? both : formatCount(total),
      gate: { word: gateWord, tone: gate?.tone ?? 'neutral' },
      evidence: { word: completeness, tone: completeness === 'complete' ? 'ok' : 'warn' },
      stamp: { text: stampText, tone: stampTone, fs: stampFs, w: stampW, x: Math.max(8, 236 - stampW - 4), land: im.classification === 'BREAKING' },
    },
    leaders: {
      next: `${iso(fit(service, 13))} ${iso(fit(newV, 8))}`,
      prev: `${iso(fit(service, 13))} ${iso(fit(oldV, 8))}`,
      face: fit(newV, 8),
    },
  };
}
