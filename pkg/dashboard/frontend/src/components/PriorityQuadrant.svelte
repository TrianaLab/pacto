<script>
  import { renderPriorityQuadrant } from '../lib/charts.ts';
  import { currentTheme } from '../lib/theme.svelte.ts';

  let { data, onSelect } = $props();

  let container;
  let lastSig = '';

  $effect(() => {
    if (!container || !data) return;
    const sig = `${currentTheme()}|${data.length}|${data.map((d) => `${d.name}:${d.x}:${d.y}:${d.status}`).join(',')}`;
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
