<script>
  import { renderCategoryStackedBars } from '../lib/charts.ts';
  import { setFilter } from '../lib/filters.svelte.ts';

  let { data = [] } = $props();

  let container;

  $effect(() => {
    if (container && data) {
      renderCategoryStackedBars(container, data, {
        onSelect: (category) => setFilter('category', category),
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
