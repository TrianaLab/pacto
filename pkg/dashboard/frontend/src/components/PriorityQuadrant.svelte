<script>
  import { renderPriorityQuadrant } from '../lib/charts.ts';

  let { data, onSelect } = $props();

  let container;
  let lastSig = '';

  $effect(() => {
    if (!container || !data) return;
    const sig = JSON.stringify(data);
    if (sig === lastSig) return;
    lastSig = sig;
    renderPriorityQuadrant(container, data, { onSelect });
  });
</script>

<div class="chart-container" bind:this={container}></div>

<style>
  .chart-container {
    width: 100%;
    min-height: 200px;
  }
</style>
