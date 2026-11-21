/**
 * The fleet map's camera: world and screen transforms, the semantic zoom levels
 * and the animated moves between views. Ported from the approved mockup
 * (docs/superpowers/mockups/fleet-graph-semantic-zoom.html, view math).
 *
 * The zoom is read as the on-screen distance between two neighbouring
 * services, `gap = GAP_WORLD * k`, so a small fleet opens further down the
 * ladder than a big one: the level follows what the reader can see.
 */
import { MOTION, prefersReducedMotion } from '../motion';

/** Nearest-neighbour distance between two services, in world units (the server's mapGapWorld). */
export const GAP_WORLD = 14;
/** Node radius in world units (the server's mapNodeR). */
export const NODE_R = 2.6;
/** On-screen gaps where Services, Names and Details begin. */
export const L1 = 14;
export const L2 = 28;
export const L3 = 110;
/** The four levels, in order, as the HUD ladder names them. */
export const LEVEL_NAMES = ['Groups', 'Services', 'Names', 'Details'] as const;
/** Symmetric margin around a fitted view, so the content is centred on the canvas. */
export const FIT_MARGIN = 40;
/** The largest zoom: neighbours 240px apart. */
const MAX_GAP = 240;

export interface View { k: number; tx: number; ty: number }
export interface Bounds { minX: number; minY: number; maxX: number; maxY: number }
/** The canvas in CSS pixels, and the width an open rail covers on its right. */
export interface Viewport { w: number; h: number; rail?: number }

export const gapOf = (k: number): number => GAP_WORLD * k;
export const kForGap = (g: number): number => g / GAP_WORLD;

/** level is the semantic zoom level for an on-screen gap: 0 Groups, 1 Services, 2 Names, 3 Details. */
export function level(gap: number): 0 | 1 | 2 | 3 {
  return gap >= L3 ? 3 : gap >= L2 ? 2 : gap >= L1 ? 1 : 0;
}

/** smooth is a smoothstep over log(x) between a and b, for crossfading two levels. */
export function smooth(x: number, a: number, b: number): number {
  const t = Math.min(1, Math.max(0, (Math.log(x) - Math.log(a)) / (Math.log(b) - Math.log(a))));
  return t * t * (3 - 2 * t);
}

export const toScreen = (v: View, x: number, y: number): [number, number] => [x * v.k + v.tx, y * v.k + v.ty];
export const toWorld = (v: View, x: number, y: number): [number, number] => [(x - v.tx) / v.k, (y - v.ty) / v.k];

/** The width the canvas shows content in: an open rail covers its right side. */
const usable = (vp: Viewport) => Math.max(1, vp.w - (vp.rail ?? 0));

/** viewFor centres world point (cx, cy) in the usable canvas at zoom k. */
export function viewFor(vp: Viewport, cx: number, cy: number, k: number): View {
  return { k, tx: usable(vp) / 2 - cx * k, ty: vp.h / 2 - cy * k };
}

/** fitK is the zoom that frames the bounds in the usable canvas, inside the margin. */
export function fitK(b: Bounds, vp: Viewport): number {
  const w = Math.max(1, b.maxX - b.minX), h = Math.max(1, b.maxY - b.minY);
  return Math.max(1e-4, Math.min((usable(vp) - 2 * FIT_MARGIN) / w, (vp.h - 2 * FIT_MARGIN) / h));
}

/** fitView frames the bounds in the measured canvas minus the rail. */
export function fitView(b: Bounds, vp: Viewport): View {
  return viewFor(vp, (b.minX + b.maxX) / 2, (b.minY + b.maxY) / 2, fitK(b, vp));
}

/** The zoom range: half the fitted zoom out, neighbours 240px apart in. */
export function clampK(k: number, fit: number): number {
  return Math.min(kForGap(MAX_GAP), Math.max(fit * 0.5, k));
}

/** zoomAt scales the view by f about screen point (sx, sy): the world point under it stays put. */
export function zoomAt(v: View, sx: number, sy: number, f: number, fit: number): View {
  const k = clampK(v.k * f, fit);
  const [wx, wy] = toWorld(v, sx, sy);
  return { k, tx: sx - wx * k, ty: sy - wy * k };
}

/**
 * interpolate moves between two views as a zoom about the one point both share
 * on screen (or a plain pan when the zoom does not change). t is eased.
 */
export function interpolate(a: View, b: View, t: number): View {
  const e = 1 - Math.pow(1 - t, 3);
  const k = Math.exp(Math.log(a.k) + (Math.log(b.k) - Math.log(a.k)) * e);
  const u = Math.abs(b.k - a.k) > 1e-9 ? (k - a.k) / (b.k - a.k) : e;
  return { k, tx: a.tx + (b.tx - a.tx) * u, ty: a.ty + (b.ty - a.ty) * u };
}

/**
 * Camera holds the current view and at most one animated move. With reduced
 * motion every move jumps.
 */
export class Camera {
  view: View = { k: 1, tx: 0, ty: 0 };
  private anim: { t0: number; from: View; to: View } | null = null;
  constructor(private reduced: () => boolean = prefersReducedMotion) {}

  /** go moves to target, animated unless told not to or the reader asked for less motion. */
  go(target: View, animate = true, now = performance.now()): void {
    if (!animate || this.reduced()) { this.view = target; this.anim = null; return; }
    this.anim = { t0: now, from: { ...this.view }, to: target };
  }

  /** set replaces the view at once, cancelling a move (drag, wheel, pinch). */
  set(v: View): void { this.view = v; this.anim = null; }

  get moving(): boolean { return this.anim !== null; }

  /** step advances a move to time now; it reports whether the move continues. */
  step(now: number): boolean {
    if (!this.anim) return false;
    const t = Math.min(1, (now - this.anim.t0) / MOTION.reveal);
    this.view = t >= 1 ? { ...this.anim.to } : interpolate(this.anim.from, this.anim.to, t);
    if (t >= 1) this.anim = null;
    return this.anim !== null;
  }
}
