<script lang="ts">
  /**
   * Columns over time buckets (#597): one series draws plain columns, more draw stacked
   * columns in the given order, `compact` draws the 44px tile sparkline. Honest about
   * unknown by construction: a bucket with a reason in `unknown` (or with no known value)
   * is an empty hatched band with the reason on an opaque plate, never a 0 column, and
   * its tooltip says it is not counted as 0. A real 0 is a 2px stub on the baseline.
   *
   * The SVG draws marks only. Every word (axis ticks, week labels, value labels, the
   * reason plate) is HTML placed from the same scale, and label spacing comes from the
   * measured widest label, never a font guess (SPEC 12, SPEC 24.1).
   */
  import { bandScale, niceMax, colPath, showTick, directLabelIndexes, runs, SERIES } from '../../lib/chart.ts';
  import { chartTip, type TipModel } from '../../lib/chartTip.ts';

  type Bucket = { label: string; short: string; partial?: boolean };
  type Series = { key: string; label: string; color?: string; texture?: 'dots'; values: (number | null)[] };

  let {
    buckets = [] as Bucket[],
    series = [] as Series[],
    unknown = [] as (string | null)[],
    caps = [] as boolean[],
    compact = false,
    tipOf = undefined as ((i: number) => TipModel) | undefined,
    height = 190,
    label = '',
  } = $props();

  let w = $state(0);
  let labelsEl: HTMLDivElement | undefined = $state();
  let widestX = $state(0);
  let widestY = $state(0);

  // ponytail: 320 until the container is measured (and in jsdom), so the first frame has geometry.
  const W = $derived(w || 320);
  const H = $derived(compact ? 44 : height);
  const stacked = $derived(series.length > 1);
  const n = $derived(buckets.length);
  const m = $derived(compact ? { l: 0, r: 0, t: 4, b: 2 } : { l: Math.max(16, widestY) + 6, r: 4, t: 14, b: 22 });
  const ph = $derived(H - m.t - m.b);
  const sc = $derived(bandScale(n, W - m.l - m.r));
  const bw = $derived(Math.min(compact ? 10 : 24, sc.band * (compact ? 0.5 : 0.55)));

  const colorOf = (s: Series, k: number) => (s.texture === 'dots' ? 'url(#viz-dots)' : s.color ?? `var(${SERIES[k % SERIES.length]})`);
  const reason = (i: number) => unknown[i] || (series.every((s) => s.values[i] == null) ? 'no records' : null);
  const totals = $derived(buckets.map((_, i) => (reason(i) ? null : series.reduce((t, s) => t + (s.values[i] ?? 0), 0))));
  const max = $derived(niceMax(Math.max(1, ...totals.filter((v): v is number => v != null))));
  const y = (v: number) => m.t + ph - (v / max) * ph;
  const cx = (i: number) => m.l + sc.mid(i);
  const ticks = $derived(compact ? [] : stacked ? [0, max / 2, max] : [0, max]);
  const peak = $derived(Math.max(...totals.map((v) => v ?? -1)));

  // Segments bottom to top, a 2px surface gap between them; the top one gets the rounded end.
  const columns = $derived(buckets.map((_, i) => {
    if (totals[i] == null) return [];
    let base = 0;
    const segs = series.map((s, k) => ({ s, k, v: s.values[i] ?? 0 })).filter((g) => g.v > 0);
    return segs.map((g, j) => {
      const top = y(base + g.v);
      const h = (g.v / max) * ph - (j ? 2 : 0);
      base += g.v;
      return { key: g.s.key, v: g.v, y: top, h, fill: colorOf(g.s, g.k), dots: g.s.texture === 'dots', top: j === segs.length - 1,
        // compact: the last and the peak column in the series colour, the rest neutral.
        tone: compact && !stacked && i !== n - 1 && totals[i] !== peak ? 'var(--c-neutral)' : null };
    });
  }));

  const valueIdx = $derived(compact ? [] : directLabelIndexes(totals.map((v) => (v ? v : null)), sc.band, widestY));
  // One plate per run of unknown buckets, centred on it, only where the run has room.
  const gaps = $derived(runs(totals.map((v) => v == null)).filter((g) => g.len * sc.band >= 40));
  const inSegment = $derived(!compact && stacked && n <= 6);

  function tip(i: number): TipModel {
    const r = reason(i);
    if (r) return { title: buckets[i].label, rows: [{ value: '', label: r, texture: 'hatch' }], foot: 'Not counted as 0' };
    if (tipOf) return tipOf(i);
    return {
      title: buckets[i].label,
      rows: series.map((s, k) => ({ value: String(s.values[i] ?? 0), label: s.label, ...(s.texture ? { texture: s.texture } : { color: colorOf(s, k) }) })),
      foot: buckets[i].partial ? 'Partial bucket' : '',
    };
  }

  // One measuring pass after the labels render: the widest week label spaces the ticks,
  // the widest value label sizes the left margin and the direct-label room.
  $effect(() => {
    void buckets; void max; void W;
    if (!labelsEl) return;
    const widest = (sel: string) => Math.max(0, ...Array.from(labelsEl!.querySelectorAll<HTMLElement>(sel), (e) => e.offsetWidth));
    widestX = widest('.cc-x');
    widestY = widest('.cc-y');
  });
</script>

<div class="cc" class:compact bind:clientWidth={w}>
  <svg width={W} height={H} role="group" aria-label={label || undefined}>
    {#each ticks as t (t)}
      <line class="cc-grid" x1={m.l} x2={W - m.r} y1={y(t)} y2={y(t)} stroke={t ? 'var(--chart-grid)' : 'var(--chart-axis)'} />
    {/each}
    {#each buckets as b, i (i)}
      {#if totals[i] == null}
        <rect class="cc-unknown" x={compact ? cx(i) - bw / 2 : cx(i) - sc.band / 2 + 2} y={m.t} width={compact ? bw : Math.max(0, sc.band - 4)} height={ph}
          fill="url(#viz-hatch)" opacity={compact ? 0.55 : 0.22} stroke="var(--c-neutral)" stroke-opacity={compact ? 1 : 0.6} rx="2" />
      {:else if totals[i] === 0}
        <rect class="cc-stub" x={cx(i) - bw / 2} y={m.t + ph - 2} width={bw} height="2" rx="1" fill="var(--chart-axis)" />
      {:else}
        {#each columns[i] as c (c.key)}
          <path class="cc-col" data-series={c.key} data-y={c.y} data-h={c.h}
            d={c.top ? colPath(cx(i) - bw / 2, c.y, bw, c.h, compact ? 2 : 4) : `M${cx(i) - bw / 2},${c.y}h${bw}v${c.h}h${-bw}Z`}
            fill={c.tone ?? c.fill} stroke={c.dots ? 'var(--c-neutral)' : undefined} />
        {/each}
        {#if caps[i]}
          <rect class="cc-cap" x={cx(i) - bw / 2} y={y(totals[i] ?? 0) - 7} width={bw} height="6" rx="1" fill="url(#viz-hatch)" stroke="var(--c-neutral)" />
        {/if}
      {/if}
    {/each}
    {#each buckets as b, i (i)}
      <rect class="cc-hit" x={cx(i) - sc.band / 2} y={compact ? 0 : m.t} width={sc.band} height={compact ? H : ph} use:chartTip={tip(i)} />
    {/each}
  </svg>

  {#if !compact}
    <div class="cc-labels" aria-hidden="true" bind:this={labelsEl}>
      {#each ticks as t (t)}
        <span class="cc-y" style:right="{W - m.l + 6}px" style:top="{y(t)}px">{t}</span>
      {/each}
      {#each gaps as g (g.start)}
        <span class="cc-plate" style:left="{m.l + sc.x(g.start) + (g.len * sc.band) / 2}px" style:top="{m.t + ph / 2}px"
          style:max-inline-size="{g.len * sc.band - 6}px">{reason(g.start)}</span>
      {/each}
      {#each buckets as b, i (i)}
        <span class="cc-x" class:off={!showTick(i, n, sc.band, widestX)} style:left="{Math.min(Math.max(cx(i), widestX / 2), W - widestX / 2)}px" style:top="{H - m.b + 4}px">{b.short}</span>
        {#if inSegment}
          {#each columns[i] as c (c.key)}
            {#if c.h >= 18 && bw >= 16}<span class="cc-in" style:left="{cx(i)}px" style:top="{c.y + c.h / 2}px">{c.v}</span>{/if}
          {/each}
        {:else if valueIdx.includes(i)}
          <span class="cc-val" style:left="{cx(i)}px" style:top="{y(totals[i] ?? 0) - (caps[i] ? 9 : 2)}px">{totals[i]}</span>
        {/if}
      {/each}
    </div>
  {/if}
</div>

<style>
  .cc { position: relative; inline-size: 100%; min-inline-size: 0; }
  /* max-inline-size: a percentage makes the SVG compressible, so its drawn width never
     props its container open before the container is measured. */
  svg { display: block; overflow: visible; max-inline-size: 100%; }
  .cc-hit { fill: transparent; outline: none; }
  .cc-hit:focus-visible { fill: var(--c-surface-inset); fill-opacity: 0.5; stroke: var(--c-accent); stroke-width: 2; }
  .cc-labels { position: absolute; inset: 0; pointer-events: none; font-size: var(--text-xs); color: var(--c-text-3); font-variant-numeric: tabular-nums; line-height: 1; }
  .cc-labels span { position: absolute; white-space: nowrap; }
  .cc-y { transform: translateY(-50%); }
  .cc-x { transform: translateX(-50%); }
  .cc-x.off { visibility: hidden; }
  .cc-val { transform: translate(-50%, -100%); color: var(--c-text); font-weight: 600; }
  .cc-in, .cc-plate { transform: translate(-50%, -50%); color: var(--c-text); background: var(--c-surface); border-radius: var(--radius-xs); padding: 1px 3px; }
  .cc-labels .cc-plate { white-space: normal; text-align: center; line-height: 1.2; }
</style>
