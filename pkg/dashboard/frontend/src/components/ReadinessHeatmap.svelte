<script>
  import { formatCount } from '../lib/format.ts';
  import { renderHeatmap } from '../lib/charts.ts';
  import { currentTheme } from '../lib/theme.svelte.ts';
  import { setFilter } from '../lib/filters.svelte.ts';

  import { worstOwners } from '../lib/chartData.ts';

  let { data } = $props();

  let container;
  let lastSig = '';
  // The twenty worst teams, then Show all: every team is another 42px row.
  const LIMIT = 20;
  let all = $state(false);
  const view = $derived(all ? data : worstOwners(data, LIMIT));

  $effect(() => {
    if (!container || !view) return;
    const sig = `${currentTheme()}|${view.owners?.join(',')}|${view.categories?.join(',')}|${(view.cells ?? []).map((c) => `${c.owner}:${c.category}:${c.score}`).join(',')}`;
    if (sig === lastSig) return;
    lastSig = sig;
    renderHeatmap(container, view, {
      onSelectCell: (owner, category) => {
        setFilter('owner', owner);
        setFilter('category', category);
      },
    });
  });
</script>

<div class="chart-container" bind:this={container}></div>
{#if data?.owners?.length > LIMIT}
  <button type="button" class="btn btn-sm" aria-expanded={all} onclick={() => { all = !all; }} data-testid="heatmap-show-all">
    {all ? `Show the ${LIMIT} least ready teams` : `Show all ${formatCount(data.owners.length)} teams`}
  </button>
{/if}

<style>
  .chart-container {
    width: 100%;
    min-height: 220px;
  }
</style>
