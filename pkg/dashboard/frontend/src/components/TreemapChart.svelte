<script>
  import { renderTreemap } from '../lib/charts.ts';

  let { data, onSelect = undefined } = $props();

  let container;
  let lastSig = '';

  $effect(() => {
    if (!container || !data) return;
    const sig = JSON.stringify(data) + (document.documentElement.getAttribute('data-theme') || '');
    if (sig === lastSig) return;
    lastSig = sig;
    renderTreemap(container, data, { onSelect });
  });
</script>

<div class="chart-container" bind:this={container}></div>

<style>
  .chart-container {
    width: 100%;
    min-height: 260px;
  }
</style>
