<script>
  import EntityLink from './EntityLink.svelte';
  import IdentityBadge from './IdentityBadge.svelte';
  import { differenceLabel, differenceTone, provenanceLabel } from '../lib/entityLabels.ts';

  // Renders the observed/differences relationship summary (NeighborhoodEdge items):
  // the reconciliation of each EXPECTED (declared) dependency against OBSERVED runtime
  // traffic, read verbatim from the backend edge (ADR-3). `direction` picks which end
  // to link ('to' for a dependency, 'from' for a dependent).
  let { items = [], direction = 'to' } = $props();
</script>

<ul class="rel-list">
  {#each items as e, i (e.id ?? i)}
    <li class="rel">
      <EntityLink ref={direction === 'from' ? e.from : e.to} showStatus={false} />
      <IdentityBadge label={differenceLabel(e.difference)} tone={differenceTone(e.difference)} />
      {#if e.provenance}<span class="rel-prov">{provenanceLabel(e.provenance)}</span>{/if}
      {#if e.stale}<span class="rel-stale">stale</span>{/if}
    </li>
  {/each}
</ul>

<style>
  .rel-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-2); }
  .rel { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; }
  .rel-prov { font-size: var(--text-xs); color: var(--c-text-3); text-transform: uppercase; letter-spacing: 0.03em; }
  .rel-stale { font-size: var(--text-xs); color: var(--c-warn); }
</style>
