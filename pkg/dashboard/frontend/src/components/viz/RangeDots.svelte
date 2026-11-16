<script lang="ts">
  /**
   * Lead time per environment (#597): a dot at p50 and a line to p90 on one log time
   * axis, rows under group headers in the given order. Honest about unknown by
   * construction: a row with no joined promotion (p50 null) is an empty hatched band
   * with "unknown" on an opaque plate, never a dot at the axis start; a missing p90
   * draws no whisker and the tooltip says "p90 unknown". A known value under the axis
   * floor (a 0s lead is legal) sits at the axis start and says "0s" or "under 1s".
   *
   * The SVG draws marks only; names, group headers and tick labels are HTML placed from
   * the same scale, and names are elided by lib/elide.ts with the whole name in title.
   */
  import { logDomain, logScale, logTicks, durationText } from '../../lib/chart.ts';
  import { chartTip, type TipModel } from '../../lib/chartTip.ts';
  import { fitIdents } from '../../lib/elide.ts';

  type Group = { group: string };
  type Env = { name: string; p50: number | null; p90: number | null; foot?: string };
  type Row = Group | Env;

  let {
    rows = [] as Row[],
    tipOf = undefined as ((row: Env) => TipModel) | undefined,
    label = '',
  } = $props();

  let w = $state(0);
  let labelsEl: HTMLDivElement | undefined = $state();

  // ponytail: 320 until the container is measured (and in jsdom), so the first frame has geometry.
  const W = $derived(w || 320);
  // Under 480px each name gets its own line above its mark, so it is never cut to nothing.
  const stack = $derived(W < 480);
  const labW = $derived(stack ? 8 : Math.min(280, Math.max(110, W * 0.36)));
  const rowH = $derived(stack ? 42 : 26);
  const GROUP_H = 26;
  const m = { t: 6, b: 24, r: 16 };

  const envs = $derived(rows.filter((r): r is Env => 'name' in r));
  const dom = $derived(logDomain(envs.flatMap((e) => [e.p50, e.p50 == null ? null : e.p90])));
  const x = $derived(dom ? logScale(dom, [labW, W - m.r]) : () => labW);
  const ticks = $derived(dom ? logTicks(dom[0], dom[1], W) : []);

  // Each row's top, in order; the plot ends where the last row does.
  const layout = $derived.by(() => {
    let y = m.t;
    return rows.map((r) => { const top = y; y += 'group' in r ? GROUP_H : rowH; return top; });
  });
  const plotB = $derived(m.t + rows.reduce((t, r) => t + ('group' in r ? GROUP_H : rowH), 0));
  const H = $derived(plotB + m.b);
  const cy = (top: number) => (stack ? top + 30 : top + rowH / 2);

  function tip(e: Env): TipModel {
    if (tipOf) return tipOf(e);
    if (e.p50 == null) return { title: `Lead time to ${e.name}`, rows: [{ value: 'unknown', label: 'p50', texture: 'hatch' }], foot: e.foot || 'No promotion joined to its publish' };
    return {
      title: `Lead time to ${e.name}`,
      rows: [
        { value: durationText(e.p50), label: 'p50', color: 'var(--chart-1)' },
        { value: durationText(e.p90), label: 'p90', color: 'var(--chart-1)' },
      ],
      foot: e.foot,
    };
  }

  // One elision pass once the labels are laid out at this width.
  $effect(() => {
    void W; void rows;
    if (!labelsEl) return;
    const el = labelsEl;
    fitIdents(el);
    // Again once web fonts land: a name measured in the fallback font can still overflow.
    document.fonts?.ready.then(() => { if (el.isConnected) fitIdents(el); });
  });
</script>

<div class="rd" class:stack bind:clientWidth={w}>
  <svg width={W} height={H} role="group" aria-label={label || undefined}>
    {#each ticks as t (t.value)}
      <line class="chart-grid" x1={x(t.value)} x2={x(t.value)} y1={m.t} y2={plotB} />
    {/each}
    <line class="chart-axis" x1={labW} x2={W - m.r} y1={plotB} y2={plotB} />
    {#each rows as r, i (i)}
      {#if 'name' in r}
        {@const c = cy(layout[i])}
        {#if r.p50 == null}
          <rect class="rd-unknown" x={labW} y={c - 6} width={Math.max(0, W - m.r - labW)} height="12" rx="2"
            fill="url(#viz-hatch)" opacity="0.55" stroke="var(--c-neutral)" />
        {:else}
          {#if r.p90 != null}
            <g class="rd-whisker" stroke="var(--chart-1)" stroke-width="2" stroke-linecap="round" opacity="0.55">
              <line x1={x(r.p50)} x2={x(r.p90)} y1={c} y2={c} />
              <line x1={x(r.p90)} x2={x(r.p90)} y1={c - 5} y2={c + 5} />
            </g>
          {/if}
          <circle class="rd-dot" cx={x(r.p50)} cy={c} r="4.5" fill="var(--chart-1)" stroke="var(--chart-surface)" stroke-width="2" />
        {/if}
        <rect class="rd-hit" x="0" y={layout[i]} width={W} height={rowH} use:chartTip={tip(r)} />
      {/if}
    {/each}
  </svg>

  <div class="rd-labels" aria-hidden="true" bind:this={labelsEl}>
    {#each ticks as t (t.value)}
      <span class="rd-tick" style:left="{x(t.value)}px" style:top="{plotB + 6}px">{t.label}</span>
    {/each}
    {#each rows as r, i (i)}
      {#if 'group' in r}
        <span class="rd-group" style:top="{layout[i] + GROUP_H - 6}px" style:max-inline-size="{W}px">{r.group}</span>
      {:else}
        <span class="rd-name" title={r.name} style:top="{stack ? layout[i] + 4 : cy(layout[i])}px"
          style:inline-size="{stack ? W - 16 : labW - 18}px"><bdi class="fit">{r.name}</bdi></span>
        {#if r.p50 == null}
          <span class="rd-plate" style:left="{labW + 26}px" style:top="{cy(layout[i])}px">unknown</span>
        {/if}
      {/if}
    {/each}
  </div>
</div>

<style>
  .rd { position: relative; inline-size: 100%; min-inline-size: 0; }
  svg { display: block; overflow: visible; max-inline-size: 100%; }
  .rd-hit { fill: transparent; outline: none; }
  .rd-hit:focus-visible { fill: var(--c-surface-inset); fill-opacity: 0.5; stroke: var(--c-accent); stroke-width: 2; }
  .rd-labels { position: absolute; inset: 0; pointer-events: none; font-size: var(--text-xs); color: var(--c-text-3); line-height: 1.2; }
  .rd-labels span { position: absolute; white-space: nowrap; }
  .rd-tick { transform: translateX(-50%); font-variant-numeric: tabular-nums; }
  .rd-group {
    left: 0; transform: translateY(-100%); overflow: hidden; text-overflow: ellipsis;
    font: 600 var(--text-sm) / 1.2 var(--font-display); font-variant-caps: all-small-caps; letter-spacing: .05em; color: var(--c-text-2);
  }
  .rd-name { left: 10px; font-size: var(--text-sm); color: var(--c-text-2); }
  .rd:not(.stack) .rd-name { transform: translateY(-50%); }
  .rd-name .fit { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .rd-plate { transform: translateY(-50%); color: var(--c-text); background: var(--c-surface); border-radius: var(--radius-xs); padding: 1px 3px; }
</style>
