<script>
  // The two fleet figures (509-S12): the declared <-> observed match over what Pacto can
  // observe, and contract completeness over the judged services, each with its weekly
  // trend when the stats repository has one. The Overview puts them above the map and
  // Metrics in its headline row; the root takes no box, so the two tiles join the row
  // of whichever page holds them. Every figure says its base; unknown is a word, never 0.
  import DistributionBar from './viz/DistributionBar.svelte';
  import LineChart from './viz/LineChart.svelte';
  import { matchTile, completenessTile, matchTrend, completenessTrend, trendHidden, TREND_RESTRICTED } from '../lib/fleetHeadline.ts';
  import { buckets as bucketLabels } from '../lib/metrics.ts';
  import { MISSING_SECTIONS } from '../lib/fleetMap/modes.ts';

  // `stats` is the /api/stats answer whose weekly buckets carry the trend, null when
  // there is none (no stats repository, or it could not be read).
  let {
    match = null, contracts = null, stats = null,
    gapsShown = false, onmatch = undefined, oncompleteness = undefined, level = 2,
  } = $props();

  const hidden = $derived(!!stats && trendHidden(stats));
  const buckets = $derived(stats && !hidden ? bucketLabels(stats) : []);
  const matchSpark = $derived(stats && !hidden ? matchTrend(stats) : []);
  const completeSpark = $derived(stats && !hidden ? completenessTrend(stats) : []);
  const trendNote = $derived(hidden ? TREND_RESTRICTED : '');

  const m = $derived(matchTile(match));
  const c = $derived(completenessTile(contracts, MISSING_SECTIONS));
  const drawn = (vs) => buckets.length > 1 && vs.length === buckets.length && vs.some((v) => v != null);
  const tiles = $derived([
    { key: 'match', title: 'Declared ↔ observed match', t: m, spark: matchSpark, act: onmatch, pressed: gapsShown, actLabel: 'Show the gaps on the map' },
    { key: 'completeness', title: 'Contract completeness', t: c, spark: completeSpark, act: oncompleteness, pressed: undefined, actLabel: 'Open the checklist' },
  ]);
</script>

<div class="ff" data-testid="fleet-figures">
  {#each tiles as x (x.key)}
    <div class="ff-tile" data-testid="figure-{x.key}">
      <svelte:element this={level ? `h${level}` : 'span'} class="ff-label">
        {#if x.act}
          <button type="button" class="ff-act" aria-pressed={x.pressed} title={x.actLabel} onclick={x.act}>{x.title}</button>
        {:else}{x.title}{/if}
      </svelte:element>
      <p class="ff-value" class:ff-unknown={x.t.unknown} data-testid="figure-{x.key}-value">{x.t.value}{#if x.t.note}<small>{x.t.note}</small>{/if}</p>
      {#if x.t.sub}<p class="ff-sub" data-testid="figure-{x.key}-sub">{x.t.sub}</p>{/if}
      {#if drawn(x.spark)}
        <div class="ff-fig" data-testid="figure-{x.key}-trend">
          <LineChart compact {buckets} series={[{ key: x.key, label: x.title, color: 'var(--c-accent)', values: x.spark }]} label={`${x.title} per week, ${buckets.length} weeks`} />
        </div>
      {:else if trendNote}
        <p class="ff-sub" data-testid="figure-{x.key}-trend-note">{trendNote}</p>
      {/if}
      {#if x.t.total}
        <div class="ff-bar" data-nofilter><DistributionBar label={`${x.title} by state`} total={x.t.total} segments={x.t.segments} /></div>
      {/if}
    </div>
  {/each}
</div>

<style>
  .ff { display: contents; }
  .ff-tile {
    display: flex; flex-direction: column; gap: var(--sp-1); min-inline-size: 0;
    padding: var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-md); background: var(--c-surface);
  }
  .ff-label { margin: 0; font: var(--role-label-font); font-variant-caps: all-small-caps; letter-spacing: .05em; color: var(--c-text-2); }
  .ff-act {
    padding: 0; border: 0; background: none; font: inherit; color: var(--c-accent); text-align: start; cursor: pointer;
    text-decoration: underline; text-underline-offset: 2px; min-block-size: 24px;
  }
  .ff-act[aria-pressed='true'] { color: var(--c-text); }
  .ff-value { margin: 0; font-size: var(--text-2xl); font-weight: 700; font-variant-numeric: tabular-nums; overflow-wrap: anywhere; }
  .ff-value small { margin-inline-start: var(--sp-2); font-size: var(--text-sm); font-weight: 400; color: var(--c-text-2); }
  .ff-unknown { color: var(--c-text-2); }
  .ff-sub { margin: 0; font-size: var(--text-sm); color: var(--c-text-2); overflow-wrap: anywhere; }
  .ff-fig, .ff-bar { min-inline-size: 0; }
  @media (pointer: coarse) { .ff-act { min-block-size: 44px; } }
</style>
