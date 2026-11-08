/**
 * The one layout pass of a figure (C3), in the browser: it reads the boxes HTML flow
 * produced and never measures a word to place it. Lanes fold their chips into "N more"
 * (C8a) and wires are drawn between the anchors the grid placed (SPEC 3).
 */
import { anchorPoint, wirePath, type WidthClass, type WireDef, type Side } from './figure.ts';
import { fitIdents } from './elide.ts';
import { formatCount } from './format.ts';

/**
 * layoutLanes shows at most `cap` chips per lane, then folds chips from the end until
 * the last one shown fits its box. A lane with highlights always names one while its
 * chip box is visible: the first chip is elided (C6) into the box rather than folded,
 * unless the box is under 6rem. The "N more" link counts what is not named.
 */
export function layoutLanes(stage: Element, cap: number): void {
  for (const lane of stage.querySelectorAll<HTMLElement>('.lane')) {
    const box = lane.querySelector<HTMLElement>('.lane-chips');
    const more = lane.querySelector<HTMLElement>('.lane-more');
    if (!box) continue;
    const chips = [...box.children] as HTMLElement[];
    const visible = box.getClientRects().length > 0;
    const bdi = (c: HTMLElement) => c.querySelector<HTMLElement>('bdi');
    chips.forEach((c, i) => { c.hidden = !visible || i >= cap; c.style.maxWidth = ''; const b = bdi(c); if (b) b.style.maxWidth = ''; });
    const n = Number(more?.dataset.n ?? 0);
    const say = (shown: number) => {
      if (!more) return;
      more.textContent = shown ? (n > shown ? `${formatCount(n - shown)} more` : '') : n ? `List all ${formatCount(n)}` : '';
      more.hidden = !more.textContent;
    };
    // The link reserves its widest label, so folding never moves the chips' room.
    if (more) { more.style.minWidth = ''; say(0); more.style.minWidth = `${Math.ceil(more.getBoundingClientRect().width)}px`; }
    fitIdents(box);
    let shown = chips.filter((c) => !c.hidden).length;
    const right = () => box.getBoundingClientRect().right + 0.5;
    // A chip that does not fit is elided into the room left when that room is at least
    // 8rem (C8a), so a second name survives before it is folded.
    const squeeze = (c: HTMLElement) => {
      const room = Math.floor(right() - 0.5 - c.getBoundingClientRect().left);
      const b = bdi(c);
      if (room < 120 || !b?.classList.contains('fit')) return false;
      c.style.maxWidth = `${room}px`;
      b.style.maxWidth = '100%';
      fitIdents(c);
      return c.getBoundingClientRect().right <= right();
    };
    while (shown > 1 && chips[shown - 1].getBoundingClientRect().right > right() && !squeeze(chips[shown - 1])) {
      chips[shown - 1].style.maxWidth = '';
      chips[--shown].hidden = true;
    }
    if (shown === 1 && chips[0].getBoundingClientRect().right > right()) {
      const room = Math.floor(box.getBoundingClientRect().width);
      if (room >= 90) {
        chips[0].style.maxWidth = `${room}px`;
        const b = bdi(chips[0]);
        if (b) b.style.maxWidth = '100%';
        fitIdents(chips[0]);
      }
      if (room < 90 || chips[0].getBoundingClientRect().right > right()) { chips[0].hidden = true; shown = 0; }
    }
    say(shown);
  }
}

export interface DrawnWire { id: string; d: string; off: boolean; style: string; tone: string }

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
    out.d = wirePath(anchorPoint(a.getBoundingClientRect(), fs, sr), fs, anchorPoint(b.getBoundingClientRect(), ts, sr), ts);
    out.off = false;
    return out;
  });
}
