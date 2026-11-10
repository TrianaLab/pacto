/**
 * The one layout pass of a figure (C3), in the browser: it reads the boxes HTML flow
 * produced and never measures a word to place it. Lanes fold their chips into "N more"
 * (C8a) and wires are drawn between the anchors the grid placed (SPEC 3).
 */
import { anchorPoint, wirePath, wirePoint, breakMark, arrowHead, BREAK_AT, planChips, type WidthClass, type WireDef, type Side } from './figure.ts';
import { fitIdents } from './elide.ts';
import { formatCount } from './format.ts';

/**
 * layoutLanes names at most `cap` chips per lane in the room the lane leaves them (C8a):
 * planChips shares that room, eliding (C6) the long names, and folds the rest into the
 * "N more" link, which counts what is not named. A lane with highlights names at least
 * one while its chip box is visible.
 */
export function layoutLanes(stage: Element, cap: number): void {
  for (const lane of stage.querySelectorAll<HTMLElement>('.lane')) {
    const box = lane.querySelector<HTMLElement>('.lane-chips');
    const list = lane.querySelector<HTMLElement>('.lane-list');
    const more = lane.querySelector<HTMLElement>('.lane-more');
    if (!box || !list) continue;
    const chips = [...box.children] as HTMLElement[];
    const visible = box.getClientRects().length > 0;
    const bdi = (c: HTMLElement) => c.querySelector<HTMLElement>('bdi');
    const n = Number(more?.dataset.n ?? 0);
    const say = (shown: number) => {
      if (!more) return;
      more.textContent = shown ? (n > shown ? `${formatCount(n - shown)} more` : '') : n ? `List all ${formatCount(n)}` : '';
      more.hidden = !more.textContent;
    };
    // The link reserves its widest label, so folding never moves the chips' room.
    if (more) { more.style.minWidth = ''; say(0); more.style.minWidth = `${Math.ceil(more.getBoundingClientRect().width)}px`; }
    // Natural widths: every candidate whole, free of the CSS cap.
    chips.forEach((c, i) => {
      c.hidden = !visible || i >= cap; c.style.maxWidth = '';
      const b = bdi(c); if (b) { b.style.maxWidth = 'none'; b.textContent = c.title || b.textContent; }
    });
    if (!visible) { say(0); continue; }
    const gap = parseFloat(getComputedStyle(list).columnGap) || 0;
    const pad = parseFloat(getComputedStyle(box).paddingLeft) + parseFloat(getComputedStyle(box).paddingRight);
    const reserve = more && !more.hidden ? more.getBoundingClientRect().width + gap : 0;
    const room = Math.floor(list.clientWidth - reserve - pad);
    const cand = chips.filter((c) => !c.hidden);
    const natural = cand.map((c) => c.getBoundingClientRect().width);
    const cgap = parseFloat(getComputedStyle(box).columnGap) || 0;
    // A chip with no readable elision (C6) folds: name one fewer and share the room again,
    // down to the first chip, which is always named.
    let plan = { shown: 0, widths: [] as number[] };
    for (let k = cap; k >= 1; k = Math.min(k, plan.shown) - 1) {
      plan = planChips(natural, room, k, cgap);
      cand.forEach((c, i) => {
        c.hidden = i >= plan.shown;
        const b = bdi(c);
        if (b) b.style.maxWidth = '100%';
        c.style.maxWidth = i < plan.shown ? `${plan.widths[i]}px` : '';
      });
      if (!fitIdents(box).length || plan.shown <= 1) break;
    }
    say(plan.shown);
  }
}

/**
 * spills says whether any box in a figure's stage holds more text than its width: it
 * spills past its edge, or a box that clips cuts it with no ellipsis. An identity that
 * ends in an ellipsis is elided on purpose (C6), and a visually hidden label is not
 * laid out, so neither counts.
 */
export function spills(stage: Element): boolean {
  for (const el of stage.querySelectorAll<HTMLElement>('*')) {
    if (el instanceof SVGElement || el.clientWidth < 2 || el.scrollWidth - el.clientWidth <= 1) continue;
    const cs = getComputedStyle(el);
    if (cs.display === 'inline' || cs.position === 'absolute' || cs.textOverflow === 'ellipsis' || /auto|scroll/.test(cs.overflowX)) continue;
    return true;
  }
  return false;
}

export interface DrawnWire {
  id: string; d: string; off: boolean; style: string; tone: string;
  /** The ✕ of a broken wire (C12), and the arrowhead of a "moves to" wire (C13b). */
  mark?: string; head?: string;
}

/** wirePaths places every wire of a figure for its current layout. */
export function wirePaths(stage: Element, wires: WireDef[], w: WidthClass): DrawnWire[] {
  const sr = stage.getBoundingClientRect();
  return wires.map((def) => {
    const d = w === 'col' && def.col ? { ...def, ...def.col } : def;
    const a = stage.querySelector(`[data-a="${d.from}"]`), b = stage.querySelector(`[data-a="${d.to}"]`);
    const out = { id: def.id, d: '', off: true, style: def.style, tone: def.tone };
    if ((def.when && !def.when.includes(w)) || !a || !b || !a.getClientRects().length || !b.getClientRects().length) return out;
    if (a.closest('[hidden]') || b.closest('[hidden]')) return out;
    const fs: Side = d.fs ?? 'r', ts: Side = d.ts ?? 'l';
    const p1 = anchorPoint(a.getBoundingClientRect(), fs, sr), p2 = anchorPoint(b.getBoundingClientRect(), ts, sr);
    return {
      ...out, off: false, d: wirePath(p1, fs, p2, ts),
      mark: def.style === 'broken' ? breakMark(wirePoint(p1, fs, p2, ts, BREAK_AT)) : undefined,
      head: def.head ? arrowHead(p1, fs, p2, ts) : undefined,
    };
  });
}
