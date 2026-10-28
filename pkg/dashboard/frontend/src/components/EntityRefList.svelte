<script>
  import EntityLink from './EntityLink.svelte';

  // Renders a list of product EntityRefs as canonical EntityLink rows. Every rich
  // entity page's related-entity previews (revisions, deployments, dependencies,
  // dependents, targets, contributed entities, owner's entities) route through this
  // one list, so related-entity linking is never reinvented.
  // showKind=false where the section title already names the kind (a narrow row has no
  // room to say it twice).
  let { items = [], showStatus = true, showKind = true, clip = false } = $props();
</script>

<ul class="ref-list" class:ref-clip={clip}>
  {#each items as ref (ref.kind + '::' + ref.key)}
    <li class="ref-row"><EntityLink {ref} {showStatus} {showKind} /></li>
  {/each}
</ul>

<style>
  .ref-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-1); }
  .ref-row {
    padding: var(--sp-1) var(--sp-2); border-radius: var(--radius-xs);
  }
  .ref-row:hover { background: var(--c-surface-hover); }
  /* clip: a long name keeps one line and clips; the whole name is in its title and in
     the link's accessible name. Only for names whose start tells them apart (a target
     leads with its scope); a revision's version is at the end, so revisions wrap.
     Three classes beat EntityIdentity's own label rule. */
  .ref-clip .ref-row :global(.entity-link) { display: flex; max-width: 100%; }
  .ref-clip .ref-row :global(.entity-identity) { display: flex; flex-wrap: nowrap; max-width: 100%; }
  .ref-clip .ref-row :global(.ei-kind), .ref-clip .ref-row :global(.ei-status) { flex-shrink: 0; }
  .ref-clip .ref-row :global(.ei-label) { min-width: 0; max-width: 100%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
</style>
