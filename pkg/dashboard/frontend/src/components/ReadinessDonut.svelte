<script>
  import { renderReadinessDonut } from '../lib/charts.ts';
  import { setFilter } from '../lib/filters.svelte.ts';

  let { data } = $props();

  let container;

  $effect(() => {
    if (container && data) {
      renderReadinessDonut(container, data, {
        onSelect: (bucket) => setFilter('readinessStatus', bucket),
      });
    }
  });
</script>

<div class="chart-container" bind:this={container}></div>

<style>
  .chart-container {
    width: 100%;
    min-height: 220px;
  }
</style>
