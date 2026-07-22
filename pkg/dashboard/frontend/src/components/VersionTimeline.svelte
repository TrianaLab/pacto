<script>
  import { renderVersionTimeline } from '../lib/charts.ts';

  let { data, onSelect } = $props();

  let container;

  $effect(() => {
    if (container && data) {
      renderVersionTimeline(container, data, { onSelect });
    }
  });
</script>

<div class="chart-container" bind:this={container}></div>

<style>
  .chart-container {
    width: 100%;
    min-height: 100px;
  }
</style>
