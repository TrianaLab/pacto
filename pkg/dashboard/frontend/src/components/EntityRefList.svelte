<script>
  import EntityLink from './EntityLink.svelte';

  // Renders a list of product EntityRefs as canonical EntityLink rows. Every rich
  // entity page's related-entity previews (revisions, deployments, dependencies,
  // dependents, targets, contributed entities, owner's entities) route through this
  // one list, so related-entity linking is never reinvented.
  let { items = [], showStatus = true } = $props();
</script>

<ul class="ref-list">
  {#each items as ref (ref.kind + '::' + ref.key)}
    <li class="ref-row"><EntityLink {ref} {showStatus} /></li>
  {/each}
</ul>

<style>
  .ref-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-1); }
  .ref-row {
    padding: var(--sp-1) var(--sp-2); border-radius: var(--radius-xs);
  }
  .ref-row:hover { background: var(--c-surface-hover); }
</style>
