<script>
  import { renderTreemap } from '../lib/charts.ts';

  let { data, onSelect = undefined } = $props();

  let container;

  $effect(() => {
    if (container && data) {
      renderTreemap(container, data, { onSelect });
    }
  });
</script>

<div class="chart-container" bind:this={container}></div>

<style>
  .chart-container {
    width: 100%;
    min-height: 260px;
  }
</style>
