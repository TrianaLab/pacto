<script>
  import { renderVersionTimeline } from '../lib/charts.ts';
  import { currentTheme } from '../lib/theme.svelte.ts';

  let { data, onSelect } = $props();

  let container;
  let lastSig = '';

  $effect(() => {
    if (!container || !data) return;
    const sig = JSON.stringify(data) + currentTheme();
    if (sig === lastSig) return;
    lastSig = sig;
    renderVersionTimeline(container, data, { onSelect });
  });
</script>

<div class="chart-container" bind:this={container}></div>

<style>
  .chart-container {
    width: 100%;
    min-height: 100px;
  }
</style>
