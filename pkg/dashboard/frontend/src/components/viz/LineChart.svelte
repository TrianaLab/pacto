<script lang="ts">
  /**
   * Lines over time buckets (#597): the coverage chart (up to four series on ONE y axis,
   * never two) and, with `compact`, the 44px tile sparkline. A missing value breaks the
   * line, it never drops to 0; a bucket where every series is missing is an empty hatched
   * band; a partial point is a hollow hatched marker. The legend lives outside.
   *
   * The plot is one keyboard stop: hover or Left / Right move a crosshair, and the shared
   * chart tooltip (plus a polite live region) reads that bucket. Words are HTML.
   */
  import { bandScale, niceMax, showTick, runs } from '../../lib/chart.ts';
  import { tipState, hideTip, tipLabel, TIP_ID, type TipModel } from '../../lib/chartTip.ts';

  type Bucket = { label: string; short: string; partial?: boolean };
  type Series = { key: string; label: string; color: string; values: (number | null)[]; partial?: boolean[] };

  let {
    buckets = [] as Bucket[],
    series = [] as Series[],
    compact = false,
    tipOf = undefined as ((i: number) => TipModel) | undefined,
    height = 200,
    label = '',
  } = $props();

  let w = $state(0);
  let labelsEl: HTMLDivElement | undefined = $state();
  let hitEl: SVGRectElement | undefined = $state();
  let widestX = $state(0);
  let widestY = $state(0);
  let cur = $state(-1);

  // ponytail: 320 until the container is measured (and in jsdom), so the first frame has geometry.
  const W = $derived(w || 320);
  const H = $derived(compact ? 44 : height);
  const n = $derived(buckets.length);
  const m = $derived(compact ? { l: 4, r: 6, t: 6, b: 4 } : { l: Math.max(16, widestY) + 6, r: 14, t: 12, b: 22 });
  const ph = $derived(H - m.t - m.b);
  const sc = $derived(bandScale(n, W - m.l - m.r));
  const known = $derived(series.flatMap((s) => s.values.filter((v): v is number => v != null)));
  // Full plots start at 0; a sparkline reads the shape, so it hugs the known range.
  const lo = $derived(compact && known.length ? Math.min(...known) * 0.8 : 0);
  const hi = $derived(compact ? Math.max(lo + 1, ...known.map((v) => v * 1.05)) : niceMax(Math.max(1, ...known)));
  const y = (v: number) => m.t + ph - ((v - lo) / (hi - lo)) * ph;
  const x = (i: number) => m.l + sc.mid(i);
  const ticks = $derived(compact ? [] : [0, hi / 2, hi]);
  const empty = (i: number) => series.every((s) => s.values[i] == null);
  // A gap is one hatched band over its run of empty buckets, with one plate where it has room.
  const gaps = $derived(runs(buckets.map((_, i) => empty(i))));

  // A gap, never a drop to 0: each run of known values is its own M segment.
  const path = (s: Series) => s.values.map((v, i) => (v == null ? '' : `${i && s.values[i - 1] != null ? 'L' : 'M'}${x(i)},${y(v)}`)).join('');
  const lastIdx = (s: Series) => s.values.reduce<number>((l, v, i) => (v == null ? l : i), -1);

  function tip(i: number): TipModel {
    if (empty(i)) return { title: buckets[i].label, rows: [{ value: '', label: 'no records', texture: 'hatch' }], foot: 'Not counted as 0' };
    if (tipOf) return tipOf(i);
    return { title: buckets[i].label, rows: series.map((s) => ({ value: s.values[i] == null ? 'unknown' : String(s.values[i]), label: s.label, color: s.color })) };
  }

  const show = (i: number, px?: number, py?: number) => {
    cur = i;
    const r = hitEl!.getBoundingClientRect();
    tipState.set({ model: tip(i), x: px ?? r.left + x(i) - m.l, y: py ?? r.top, owner: hitEl! });
  };
  const hide = () => { cur = -1; hideTip(); };
  const at = (e: PointerEvent) => {
    const r = hitEl!.getBoundingClientRect();
    show(Math.max(0, Math.min(n - 1, Math.floor((e.clientX - r.left) / sc.band))), e.clientX, e.clientY);
  };
  function key(e: KeyboardEvent) {
    if (e.key === 'Escape') return hide();
    if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return;
    e.preventDefault();
    show(Math.max(0, Math.min(n - 1, (cur < 0 ? n - 1 : cur) + (e.key === 'ArrowRight' ? 1 : -1))));
  }

  $effect(() => {
    void buckets; void hi; void W;
    if (!labelsEl) return;
    const widest = (sel: string) => Math.max(0, ...Array.from(labelsEl!.querySelectorAll<HTMLElement>(sel), (e) => e.offsetWidth));
    widestX = widest('.lc-x');
    widestY = widest('.lc-y');
  });
</script>

<div class="lc" class:compact bind:clientWidth={w}>
  <svg width={W} height={H}>
    {#if !compact}
      <g class="lc-yaxis">
        {#each ticks as t (t)}
          <line x1={m.l} x2={W - m.r} y1={y(t)} y2={y(t)} stroke={t ? 'var(--chart-grid)' : 'var(--chart-axis)'} />
        {/each}
      </g>
    {/if}
    {#each gaps as g (g.start)}
      <rect class="lc-unknown" x={compact ? x(g.start) - 5 : m.l + sc.x(g.start) + 2} y={m.t} width={compact ? sc.band * (g.len - 1) + 10 : Math.max(0, g.len * sc.band - 4)} height={ph}
        fill="url(#viz-hatch)" opacity={compact ? 0.55 : 0.22} stroke="var(--c-neutral)" rx="2" />
    {/each}
    {#if cur >= 0 && !compact}
      <line class="lc-cross" x1={x(cur)} x2={x(cur)} y1={m.t} y2={m.t + ph} stroke="var(--chart-axis)" opacity="1" />
    {/if}
    {#each series as s (s.key)}
      <path class="lc-line" d={path(s)} fill="none" stroke={s.color} stroke-width="2" stroke-linejoin="round" stroke-linecap="round" />
      {#each s.values as v, i (i)}
        {#if v != null && s.partial?.[i]}
          <circle class="lc-partial" cx={x(i)} cy={y(v)} r="4" fill="url(#viz-hatch)" stroke={s.color} stroke-width="2" />
        {:else if v != null && i === lastIdx(s)}
          <circle class="lc-end" cx={x(i)} cy={y(v)} r="4" fill={s.color} stroke="var(--chart-surface)" stroke-width="2" />
        {/if}
        {#if v != null && i === cur && !compact}
          <circle class="lc-dot" cx={x(i)} cy={y(v)} r="4" fill={s.color} stroke="var(--chart-surface)" stroke-width="2" />
        {/if}
      {/each}
    {/each}
    <!-- One keyboard stop that reads like an image: the arrows move the crosshair. -->
    <!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
    <rect class="lc-hit" x={m.l} y={compact ? 0 : m.t} width={Math.max(0, W - m.l - m.r)} height={compact ? H : ph}
      tabindex="0" role="img" aria-label={label} aria-describedby={TIP_ID} bind:this={hitEl}
      onpointermove={at} onpointerleave={hide} onfocus={() => show(cur < 0 ? n - 1 : cur)} onblur={hide} onkeydown={key} />
  </svg>
  <span class="visually-hidden" aria-live="polite">{cur >= 0 ? tipLabel(tip(cur)) : ''}</span>

  {#if !compact}
    <div class="lc-labels" aria-hidden="true" bind:this={labelsEl}>
      {#each ticks as t (t)}
        <span class="lc-y" style:right="{W - m.l + 6}px" style:top="{y(t)}px">{t}</span>
      {/each}
      {#each gaps.filter((g) => g.len * sc.band >= 40) as g (g.start)}
        <span class="lc-plate" style:left="{m.l + sc.x(g.start) + (g.len * sc.band) / 2}px" style:top="{m.t + ph / 2}px"
          style:max-inline-size="{g.len * sc.band - 6}px">no records</span>
      {/each}
      {#each buckets as b, i (i)}
        <span class="lc-x" class:off={!showTick(i, n, sc.band, widestX)} style:left="{Math.min(Math.max(x(i), widestX / 2), W - widestX / 2)}px" style:top="{H - m.b + 4}px">{b.short}</span>
      {/each}
    </div>
  {/if}
</div>

<style>
  .lc { position: relative; inline-size: 100%; min-inline-size: 0; }
  /* max-inline-size: a percentage makes the SVG compressible, so its drawn width never
     props its container open before the container is measured. */
  svg { display: block; overflow: visible; max-inline-size: 100%; }
  .lc-hit { fill: transparent; outline: none; }
  .lc-hit:focus-visible { stroke: var(--c-accent); stroke-width: 2; }
  .lc-labels { position: absolute; inset: 0; pointer-events: none; font-size: var(--text-xs); color: var(--c-text-3); font-variant-numeric: tabular-nums; line-height: 1; }
  .lc-labels span { position: absolute; white-space: nowrap; }
  .lc-y { transform: translateY(-50%); }
  .lc-x { transform: translateX(-50%); }
  .lc-x.off { visibility: hidden; }
  .lc-labels .lc-plate {
    transform: translate(-50%, -50%); color: var(--c-text); background: var(--c-surface); border-radius: var(--radius-xs);
    padding: 1px 3px; white-space: normal; text-align: center; line-height: 1.2;
  }
</style>
