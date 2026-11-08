/**
 * Narration (SPEC 6): a finite retelling inside one figure, of a real state change or of
 * the answer to something the reader did. WAAPI only, no dependency.
 *
 * The final frame is the DOM as rendered: a narration only adds animations that fill
 * backwards from hidden, so a figure that never plays (final mode, jsdom, a second
 * figure, an answer already told this session) is complete from frame 1. Every
 * animation carries the id prefix `narr:`, so finishNarrations() and the e2e harness
 * can end them all at once.
 */
import { dur, prefersReducedMotion } from './motion.ts';

/** --narrate-step, --narrate-hold and --narrate-max in styles/tokens.css, mirrored. */
export const NARRATE = { step: 240, hold: 120, max: 1200, steps: 5, walkSteps: 8 } as const;

export interface Step {
  /** The step in words: the reduced-motion step-through shows it, screen readers read the list. */
  cap: string;
  dur: number;
  hold?: number;
  /** Marks revealed in this step: selectors inside the stage, or `w:<id>` for a wire. */
  show?: string[];
  /** Wires a signal runs along in this step. */
  sig?: string[];
}

/** timeline places each step and enforces the Ration amendment (A1, A1b). */
export function timeline(steps: Step[], { walk = false } = {}): { at: number[]; total: number } {
  const cap = walk ? NARRATE.walkSteps : NARRATE.steps;
  if (steps.length > cap) throw new Error(`narration has ${steps.length} steps, over ${cap}`);
  const at: number[] = [];
  let t = 0;
  steps.forEach((s, i) => {
    if (s.dur > NARRATE.step) throw new Error(`step "${s.cap}" lasts ${s.dur}ms, over ${NARRATE.step}`);
    if (i) t += s.hold ?? NARRATE.hold;
    at.push(t);
    t += s.dur;
  });
  if (t > NARRATE.max) throw new Error(`narration lasts ${t}ms, over ${NARRATE.max}`);
  return { at, total: t };
}

/** No narration ever starts: reduced motion, or the gates' html[data-narrate="final"]. */
export function finalMode(): boolean {
  return prefersReducedMotion() || (typeof document !== 'undefined' && document.documentElement.dataset.narrate === 'final');
}

/** sessionKey hashes the model fields a narration animates (FNV-1a), never asOf. */
export function sessionKey(model: unknown): string {
  const s = JSON.stringify(model);
  let h = 2166136261;
  for (let i = 0; i < s.length; i++) h = Math.imul(h ^ s.charCodeAt(i), 16777619);
  return 'narr:' + (h >>> 0).toString(36);
}

const TOLD = 'pacto-narrated';

/** claimAutoplay is true once per session per key: an unchanged answer is told once. */
export function claimAutoplay(key: string): boolean {
  try {
    const told: string[] = JSON.parse(sessionStorage.getItem(TOLD) || '[]');
    if (told.includes(key)) return false;
    sessionStorage.setItem(TOLD, JSON.stringify([...told, key].slice(-200)));
    return true;
  } catch {
    return false; // no storage: stay still rather than replay on every visit
  }
}

const EASE = 'cubic-bezier(0.16, 1, 0.3, 1)';

/**
 * A token that walks (SPEC 6.2 r3): the element `sel` and, per step, the slot it stands
 * in during that step; the last slot is where it rests, which is where it is rendered.
 */
export interface TokenWalk { sel: string; slots: string[] }

/** tokenOffsets places the token in each slot as a translation from where it rests. */
export function tokenOffsets(slots: { left: number; top: number }[]): string[] {
  const end = slots[slots.length - 1];
  return slots.map((r) => (r === end ? 'none' : `translate(${Math.round(r.left - end.left)}px, ${Math.round(r.top - end.top)}px)`));
}

/**
 * play runs the steps over the marks under `stage` and the wires in `wires`. It returns
 * the animations it started (none in jsdom, which has no Element.animate). A rail walk
 * (`walk`) may take one step per station reached (A1b); its token travels them in one
 * animation, one keyframe per step.
 */
export function play(stage: Element, wires: Element | null, steps: Step[], id: string, { walk = false, token = null as TokenWalk | null } = {}): Animation[] {
  if (typeof Element === 'undefined' || !Element.prototype.animate) return [];
  const { at, total } = timeline(steps, { walk });
  const out: Animation[] = [];
  const opts = (delay: number, d: number, fill: FillMode = 'backwards', easing = EASE) =>
    ({ duration: dur(d), delay: dur(delay), fill, easing, id: `narr:${id}` });
  steps.forEach((s, i) => {
    for (const sel of s.show ?? []) {
      for (const el of resolve(stage, wires, sel)) out.push(el.animate([{ opacity: 0 }, { opacity: 1 }], opts(at[i], s.dur)));
    }
    for (const wid of s.sig ?? []) {
      const p = wires?.querySelector<SVGPathElement>(`path.sig[data-wid="${wid}"]`);
      if (!p || !p.getTotalLength || getComputedStyle(p).display === 'none') continue;
      const L = p.getTotalLength();
      p.style.strokeDasharray = `18 ${L + 18}`;
      out.push(p.animate([{ strokeDashoffset: 18, opacity: 1 }, { strokeDashoffset: -L, opacity: 1 }], opts(at[i], s.dur, 'none', 'linear')));
    }
  });
  const tok = token && stage.querySelector(token.sel);
  const slots = token ? token.slots.map((sel) => stage.querySelector(sel)) : [];
  if (tok && slots.length > 1 && slots.every(Boolean) && total) {
    const moves = tokenOffsets(slots.map((el) => el!.getBoundingClientRect()));
    const kf = moves.map((transform, j) => ({ transform, offset: j === moves.length - 1 ? 1 : at[j] / total }));
    out.push(tok.animate(kf, opts(0, total, 'backwards', 'linear')));
  }
  return out;
}

/** resolve finds a step's targets: a wire (`w:<id>`, its path and end dot) or stage marks. */
export function resolve(stage: Element, wires: Element | null, sel: string): Element[] {
  if (sel.startsWith('w:')) return wires ? [...wires.querySelectorAll(`[data-wid="${sel.slice(2)}"]:not(.sig)`)] : [];
  return [...stage.querySelectorAll(sel)];
}

/** finishNarrations jumps every narration on the page to its final frame. */
export function finishNarrations(): void {
  if (typeof document === 'undefined' || !document.getAnimations) return;
  for (const a of document.getAnimations()) if (String(a.id).startsWith('narr:')) a.finish();
}
