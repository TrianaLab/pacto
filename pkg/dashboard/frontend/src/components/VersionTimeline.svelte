<script>
  import { renderVersionTimeline } from '../lib/charts.ts';
  import { binTimeline } from '../lib/chartData.ts';
  import { currentTheme } from '../lib/theme.svelte.ts';

  let { data, onSelect } = $props();

  let container;
  let lastSig = '';

  $effect(() => {
    if (!container || !data) return;
    // A cheap signature: the versions and their classes, never a JSON of the whole list.
    const sig = `${currentTheme()}|${data.length}|${data.map((d) => `${d.version}:${d.classification}:${d.at}`).join(',')}`;
    if (sig === lastSig) return;
    lastSig = sig;
    renderVersionTimeline(container, binTimeline(data), { onSelect });
  });
</script>

<div class="chart-container" bind:this={container}></div>

<style>
  .chart-container {
    width: 100%;
    min-height: 130px;
  }
</style>
