/**
 * Chart geometry for the Metrics charts (#597): scales, ticks, mark paths and label
 * placement. Pure and rune-free, so every primitive draws from the same numbers and a
 * test can pin them. Words are never drawn here: a primitive places HTML labels from
 * these scales (SPEC 12), and long names go through lib/elide.ts.
 */

/** Categorical series colours, in fixed order: colour follows the entity, never its rank. */
export const SERIES = ['--chart-1', '--chart-2'] as const;
/** The heatmap's ordinal ramp, least to worst (bins in lib/metrics.ts). */
export const HEAT = ['--chart-hm-1', '--chart-hm-2', '--chart-hm-3', '--chart-hm-4'] as const;

const NICE = [1, 1.6, 2, 2.5, 4, 5, 8, 10];

/** niceMax rounds a domain top up to the next nice step (1, 1.6, 2, 2.5, 4, 5, 8 x 10^k). */
export function niceMax(v: number): number {
  if (!(v > 0)) return 1;
  const p = 10 ** Math.floor(Math.log10(v));
  const step = NICE.find((s) => v <= s * p * (1 + 1e-9)) ?? 10;
  return Number((step * p).toPrecision(12));
}

/** bandScale splits a width into n equal bands: x is a band's start, mid its centre. */
export function bandScale(n: number, width: number) {
  const band = n > 0 ? width / n : width;
  return { band, x: (i: number) => band * i, mid: (i: number) => band * i + band / 2 };
}

export function linearScale([d0, d1]: [number, number], [r0, r1]: [number, number]) {
  return (v: number) => (d1 === d0 ? r0 : r0 + ((v - d0) / (d1 - d0)) * (r1 - r0));
}

/**
 * logDomain is the lead-time axis in seconds: from max(1s, fastest known / 1.6) to the
 * slowest known x 1.25. A 0s or sub-second lead is legal, so the floor keeps the axis
 * finite; logScale pins anything under it at the start. Null when nothing is known.
 */
export function logDomain(values: (number | null | undefined)[]): [number, number] | null {
  const known = values.filter((v): v is number => v != null && Number.isFinite(v) && v >= 0);
  if (!known.length) return null;
  const lo = Math.max(1, Math.min(...known) / 1.6);
  return [lo, Math.max(Math.max(...known) * 1.25, lo * 4)];
}

export function logScale([lo, hi]: [number, number], [r0, r1]: [number, number]) {
  const a = Math.log(lo), b = Math.log(hi);
  return (v: number) => r0 + ((Math.log(Math.max(v, lo)) - a) / (b - a)) * (r1 - r0);
}

const LOG_TICKS = [1, 10, 60, 600, 1_800, 3_600, 10_800, 21_600, 43_200, 86_400, 259_200, 604_800, 1_209_600, 2_592_000];

/** logTicks picks the round durations inside the domain; under 380px, every other one. */
export function logTicks(lo: number, hi: number, width: number): { value: number; label: string }[] {
  let ticks = LOG_TICKS.filter((v) => v >= lo && v <= hi);
  if (width < 380 && ticks.length > 4) ticks = ticks.filter((_, i) => i % 2 === 0);
  return ticks.map((value) => ({ value, label: durationText(value) }));
}

/** colPath is a column with a rounded data end (top) and a square baseline. */
export function colPath(x: number, y: number, w: number, h: number, r = 4): string {
  r = Math.min(r, h, w / 2);
  return `M${x},${y + h}V${y + r}Q${x},${y} ${x + r},${y}H${x + w - r}Q${x + w},${y} ${x + w},${y + r}V${y + h}Z`;
}

/** barPath is a horizontal bar with a rounded data end (right) and a square baseline. */
export function barPath(x: number, y: number, w: number, h: number, r = 4): string {
  r = Math.min(r, w, h / 2);
  return `M${x},${y}H${x + w - r}Q${x + w},${y} ${x + w},${y + r}V${y + h - r}Q${x + w},${y + h} ${x + w - r},${y + h}H${x}Z`;
}

/**
 * showTick says whether band i of n gets its label. Labels are spaced by the MEASURED
 * widest label plus an 8px gap, counted back from the last, which is always kept, so
 * placement never depends on a font guess (SPEC 24.1).
 */
export function showTick(i: number, n: number, band: number, widestLabel: number): boolean {
  const step = Math.max(1, Math.ceil((widestLabel + 8) / band));
  const back = n - 1 - i;
  return back === 0 || (back % step === 0 && back >= step);
}

/**
 * directLabelIndexes picks the values worth a direct label: the peak and the last known
 * value, never every mark. The peak goes when it is the last, sits next to it or its
 * label box would come within 8px of the last one's.
 */
export function directLabelIndexes(values: (number | null | undefined)[], band: number, labelWidth: number): number[] {
  let last = -1, peak = -1;
  values.forEach((v, i) => {
    if (v == null) return;
    last = i;
    if (peak < 0 || v > (values[peak] as number)) peak = i;
  });
  if (last < 0) return [];
  const gap = last - peak;
  return gap <= 1 || gap * band < labelWidth + 8 ? [last] : [peak, last];
}

/** durationText prints seconds as the two largest units: "45s", "12m", "3h 20m", "2d 4h". */
export function durationText(seconds: number | null | undefined): string {
  if (seconds == null) return 'unknown';
  if (seconds > 0 && seconds < 1) return 'under 1s';
  const units: [string, number][] = [['d', 86_400], ['h', 3_600], ['m', 60], ['s', 1]];
  const parts: string[] = [];
  let left = Math.max(0, Math.round(seconds));
  for (const [u, n] of units) {
    const v = Math.floor(left / n);
    left -= v * n;
    if (v || parts.length) parts.push(`${v}${u}`);
    if (parts.length === 2) break;
  }
  return parts.filter((x) => !x.startsWith('0')).join(' ') || '0s';
}
