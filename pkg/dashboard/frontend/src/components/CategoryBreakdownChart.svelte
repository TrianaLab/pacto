<script>
  import { renderCategoryStackedBars } from '../lib/charts.ts';
  import { setFilter } from '../lib/filters.svelte.ts';

  let { data = [] } = $props();

  let container;
  let lastSig = '';

  $effect(() => {
    if (!container || !data) return;
    const sig = JSON.stringify(data) + (document.documentElement.getAttribute('data-theme') || '');
    if (sig === lastSig) return;
    lastSig = sig;
    renderCategoryStackedBars(container, data, {
      onSelect: (category) => setFilter('category', category),
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
