/**
 * Hit testing on the fleet map: a uniform grid over world positions, so
 * finding the node under the pointer reads a few cells, never every node.
 */
import { GAP_WORLD } from './view';

export interface Point { x: number; y: number }
export interface Circle extends Point { r: number }

/** Cell size: two neighbour gaps. */
const CELL = GAP_WORLD * 2;

export class Grid<T extends Point> {
  private cells = new Map<string, T[]>();
  constructor(items: Iterable<T>) {
    for (const it of items) {
      const k = `${Math.floor(it.x / CELL)},${Math.floor(it.y / CELL)}`;
      const a = this.cells.get(k);
      if (a) a.push(it); else this.cells.set(k, [it]);
    }
  }

  /** nearest is the accepted item closest to (wx, wy) within radius r, or null. */
  nearest(wx: number, wy: number, r: number, accept: (t: T) => boolean = () => true): T | null {
    let best: T | null = null, bd = r * r;
    for (let gx = Math.floor((wx - r) / CELL); gx <= Math.floor((wx + r) / CELL); gx++) {
      for (let gy = Math.floor((wy - r) / CELL); gy <= Math.floor((wy + r) / CELL); gy++) {
        for (const it of this.cells.get(`${gx},${gy}`) ?? []) {
          const d = (it.x - wx) ** 2 + (it.y - wy) ** 2;
          if (d < bd && accept(it)) { bd = d; best = it; }
        }
      }
    }
    return best;
  }
}

/** circleAt is the first circle containing (wx, wy), or null. */
export function circleAt<T extends Circle>(cs: readonly T[], wx: number, wy: number): T | null {
  for (const c of cs) if ((wx - c.x) ** 2 + (wy - c.y) ** 2 < c.r * c.r) return c;
  return null;
}
