<script>
  import { renderReadinessDonut } from '../lib/charts.ts';
  import { setFilter } from '../lib/filters.svelte.ts';

  let { data } = $props();

  let container;
  let lastSig = '';

  $effect(() => {
    if (!container || !data) return;
    const sig = JSON.stringify(data) + (document.documentElement.getAttribute('data-theme') || '');
    if (sig === lastSig) return;
    lastSig = sig;
    renderReadinessDonut(container, data, {
      onSelect: (bucket) => setFilter('readinessStatus', bucket),
    });
  });
</script>

<div class="chart-container" bind:this={container}></div>

<style>
  .chart-container {
    width: 100%;
    min-height: 220px;
  }
</style>
