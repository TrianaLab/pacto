<script>
  import EntityLink from './EntityLink.svelte';
  import { formatDate } from '../lib/dateFormat.ts';

  // Renders recent/related evidence as navigable rows (requirement D/K): the
  // evidenced target plus when it was observed. Reused by the overview and the
  // service page so evidence rendering is not reinvented.
  let { items = [] } = $props();
</script>

<ul class="evi-list">
  {#each items as ev, i (i)}
    <li class="evi-row">
      <EntityLink ref={ev.target} showStatus={false} />
      {#if ev.at}<span class="evi-at">{formatDate(ev.at)}</span>{/if}
    </li>
  {/each}
</ul>

<style>
  .evi-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-1); }
  .evi-row { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; padding: var(--sp-1) var(--sp-2); border-radius: var(--radius-xs); }
  .evi-row:hover { background: var(--c-surface-hover); }
  .evi-at { color: var(--c-text-3); font-size: var(--text-xs); margin-left: auto; }
</style>
