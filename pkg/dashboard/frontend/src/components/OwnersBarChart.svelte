<script>
  import { renderOwnerBars } from '../lib/charts.ts';
  import { ownerUrl } from '../lib/router.ts';

  let { data = [] } = $props();

  let container;
  let lastSig = '';

  $effect(() => {
    if (!container || !data) return;
    const sig = JSON.stringify(data) + (document.documentElement.getAttribute('data-theme') || '');
    if (sig === lastSig) return;
    lastSig = sig;
    renderOwnerBars(container, data, {
      onSelect: (key) => {
        location.hash = ownerUrl(key);
      },
    });
  });
</script>

<div class="chart-container" bind:this={container}></div>

<style>
  .chart-container {
    width: 100%;
    min-height: 200px;
    margin-bottom: var(--sp-4);
  }
</style>
